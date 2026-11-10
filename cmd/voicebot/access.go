package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
)

// Callback data: "acc:req" from the requester, "acc:ok:<id>" / "acc:no:<id>" from an admin (well under 64 bytes).
const requestData = "acc:req"

var htmlEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")

func decisionData(approve bool, id int64) string {
	verb := "no"
	if approve {
		verb = "ok"
	}
	return "acc:" + verb + ":" + strconv.FormatInt(id, 10)
}

func parseDecision(data string) (approve bool, id int64, ok bool) {
	parts := strings.Split(data, ":")
	if len(parts) != 3 || parts[0] != "acc" || (parts[1] != "ok" && parts[1] != "no") {
		return false, 0, false
	}
	id, err := strconv.ParseInt(parts[2], 10, 64)
	return parts[1] == "ok", id, err == nil && id > 0
}

func fullName(u tgUser) string {
	return strings.TrimSpace(u.FirstName + " " + u.LastName)
}

// accessRequestText is the HTML shown to admins. Telegram has no font sizes, so the ID goes in a <code> span.
func accessRequestText(u tgUser) string {
	text := fmt.Sprintf("<b>Access request</b>\n<a href=\"tg://user?id=%d\">%s</a>", u.ID, htmlEscaper.Replace(fullName(u)))
	if u.Username != "" {
		text += "\n@" + htmlEscaper.Replace(u.Username)
	}
	return text + fmt.Sprintf("\n<code>ID %d</code>", u.ID)
}

func (a *app) isAllowed(ctx context.Context, id int64) (bool, error) {
	if a.cfg.allowed[id] {
		return true, nil
	}
	var ok bool
	err := a.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM access_requests WHERE user_id=$1 AND status='approved')`, id).Scan(&ok)
	return ok, err
}

// promptAccess answers a non-allowlisted user. A pending or denied request is never re-sent to the admins.
func (a *app) promptAccess(ctx context.Context, userID int64) error {
	var status string
	if err := a.db.QueryRow(ctx, `SELECT status FROM access_requests WHERE user_id=$1`, userID).Scan(&status); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	switch status {
	case "pending":
		return a.send(ctx, userID, "Your access request is pending. You will be notified here when an admin answers.", nil)
	case "denied":
		return a.send(ctx, userID, "Your access request was denied.", nil)
	}
	return a.send(ctx, userID, "You are not on the allowlist. Do you want to request access?", [][]map[string]string{{button("Request access", requestData)}})
}

func (a *app) handleAccess(ctx context.Context, q *callback) error {
	answer := func(text string, alert bool) {
		_ = a.call(ctx, "answerCallbackQuery", map[string]any{"callback_query_id": q.ID, "text": text, "show_alert": alert}, nil)
	}
	if q.Data == requestData {
		answer("", false)
		if ok, err := a.isAllowed(ctx, q.From.ID); ok || err != nil {
			return err
		}
		return a.requestAccess(ctx, q.From)
	}
	if !a.cfg.admins[q.From.ID] {
		answer("Only admins can do this.", true)
		return nil
	}
	approve, id, ok := parseDecision(q.Data)
	if !ok {
		answer("", false)
		return nil
	}
	status, outcome := "denied", "❌ Denied by "
	if approve {
		status, outcome = "approved", "✅ Approved by "
	}
	var details string
	err := a.db.QueryRow(ctx, `UPDATE access_requests SET status=$2,decided_at=now() WHERE user_id=$1 AND status='pending' RETURNING details`, id, status).Scan(&details)
	if errors.Is(err, pgx.ErrNoRows) {
		answer("This request was already handled.", true)
		return a.call(ctx, "editMessageReplyMarkup", map[string]any{"chat_id": q.Message.Chat.ID, "message_id": q.Message.MessageID, "reply_markup": map[string]any{"inline_keyboard": [][]string{}}}, nil)
	}
	answer("", false)
	if err != nil {
		return err
	}
	edit := map[string]any{"chat_id": q.Message.Chat.ID, "message_id": q.Message.MessageID, "parse_mode": "HTML", "reply_markup": map[string]any{"inline_keyboard": [][]string{}}}
	method, field := "editMessageText", "text"
	if len(q.Message.Photo) > 0 {
		method, field = "editMessageCaption", "caption"
	}
	edit[field] = details + "\n\n" + outcome + htmlEscaper.Replace(fullName(q.From))
	if err := a.call(ctx, method, edit, nil); err != nil {
		log.Printf("edit admin message: %v", err)
	}
	if approve {
		return a.send(ctx, id, "Your access request was approved. Send /start to begin.", nil)
	}
	return a.send(ctx, id, "Sorry, your access request was denied.", nil)
}

func (a *app) requestAccess(ctx context.Context, u tgUser) error {
	// The insert is the dedupe: only the press that creates the row notifies admins.
	tag, err := a.db.Exec(ctx, `INSERT INTO access_requests(user_id,details) VALUES($1,$2) ON CONFLICT DO NOTHING`, u.ID, accessRequestText(u))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return a.promptAccess(ctx, u.ID)
	}
	sent := 0
	for admin := range a.cfg.admins {
		if err := a.sendAccessRequest(ctx, admin, u); err != nil {
			log.Printf("access request to admin %d: %v", admin, err)
			continue
		}
		sent++
	}
	if sent == 0 {
		_, _ = a.db.Exec(ctx, `DELETE FROM access_requests WHERE user_id=$1 AND status='pending'`, u.ID)
		_ = a.send(ctx, u.ID, "I couldn't reach an admin right now. Please try again later.", nil)
		return errors.New("no admin reachable")
	}
	return a.send(ctx, u.ID, "Your request was sent to the admins. You will be notified here when they answer.", nil)
}

// sendAccessRequest sends the details with the profile photo, or as plain text when there is no photo or it fails.
func (a *app) sendAccessRequest(ctx context.Context, admin int64, u tgUser) error {
	p := map[string]any{"chat_id": admin, "parse_mode": "HTML", "reply_markup": map[string]any{"inline_keyboard": [][]map[string]string{{
		button("Accept", decisionData(true, u.ID)), button("Deny", decisionData(false, u.ID)),
	}}}}
	var photos struct {
		Photos [][]struct {
			FileID string `json:"file_id"`
		} `json:"photos"`
	}
	if err := a.call(ctx, "getUserProfilePhotos", map[string]any{"user_id": u.ID, "limit": 1}, &photos); err == nil && len(photos.Photos) > 0 && len(photos.Photos[0]) > 0 {
		sizes := photos.Photos[0]
		p["photo"], p["caption"] = sizes[len(sizes)-1].FileID, accessRequestText(u)
		if err := a.call(ctx, "sendPhoto", p, nil); err == nil {
			return nil
		}
		delete(p, "photo")
		delete(p, "caption")
	}
	p["text"] = accessRequestText(u)
	return a.call(ctx, "sendMessage", p, nil)
}
