package main

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
)

const welcome = "Hi! 👋 This bot helps collect voice samples for Professor Zahedi's (زاهدی) Statistical Machine Learning class. Each student is asked to record 20 pairs: one sleepy and one awake voice note per pair, about 5 seconds each. At the end of the semester, these recordings will form a dataset for a machine learning project. Say the same sentence in both notes and type what you said after each. Notes over 10 seconds are rejected. Your audio and text stay stored until you delete a pair. Created by @AliSohani2082. Choose an option below or open the Menu beside the message field."

func actionButtons() [][]map[string]string {
	return [][]map[string]string{{button("Record a pair", "action:new"), button("My pairs", "action:my")}, {button("Help", "action:help")}}
}

func (a *app) handleMessage(ctx context.Context, m *message) error {
	userID := m.From.ID
	if strings.HasPrefix(m.Text, "/") {
		command := strings.SplitN(strings.Fields(m.Text)[0], "@", 2)[0]
		if a.cfg.adminID != 0 && userID == a.cfg.adminID {
			switch command {
			case "/stats":
				return a.adminStats(ctx)
			case "/users":
				return a.adminUsers(ctx, 0)
			case "/broadcast":
				return a.broadcast(ctx, strings.TrimSpace(strings.TrimPrefix(m.Text, strings.Fields(m.Text)[0])))
			case "/allow", "/unallow":
				return a.setAllowed(ctx, strings.TrimSpace(strings.TrimPrefix(m.Text, strings.Fields(m.Text)[0])), command == "/allow")
			}
		}
		switch command {
		case "/start":
			return a.send(ctx, userID, "Choose your language / زبان خود را انتخاب کنید:", [][]map[string]string{{button("English", "lang:en"), button("فارسی", "lang:fa")}})
		case "/help":
			return a.send(ctx, userID, welcome, actionButtons())
		case "/new":
			return a.startPair(ctx, userID, false)
		case "/my":
			return a.listPairs(ctx, userID, 0)
		case "/cancel":
			return a.cancel(ctx, userID)
		default:
			return a.send(ctx, userID, "Choose an option below or open the Menu for commands.", actionButtons())
		}
	}
	s, err := a.getSession(ctx, userID)
	if err != nil {
		return err
	}
	if s.Stage == "" {
		return a.send(ctx, userID, "Ready for another pair?", actionButtons())
	}
	if strings.HasPrefix(s.Stage, "voice:") || strings.HasPrefix(s.Stage, "editvoice:") {
		if m.Voice == nil {
			return a.send(ctx, userID, "Please send a Telegram voice message, not a file or text. Aim for 5 seconds.", nil)
		}
		if m.Voice.Duration > 10 || m.Voice.Duration < 1 {
			return a.send(ctx, userID, "That recording must be between 1 and 10 seconds. Please try again and aim for 5 seconds.", nil)
		}
		if m.Voice.FileSize > maxVoiceBytes {
			return a.send(ctx, userID, "That recording is too large for Telegram's bot download limit. Please record a shorter note.", nil)
		}
		s.Candidate = m.Voice
		s.Nonce = randomID()
		s.Stage = strings.Replace(s.Stage, "voice:", "confirm:", 1)
		s.Stage = strings.Replace(s.Stage, "editvoice:", "editconfirm:", 1)
		if err := a.putSession(ctx, userID, s); err != nil {
			return err
		}
		return a.send(ctx, userID, fmt.Sprintf(translate(ctx, "This voice note is %d seconds. Submit it or record a new one?"), m.Voice.Duration), [][]map[string]string{{button("Submit", "accept:"+s.Nonce), button("Record a new one", "retry:"+s.Nonce)}})
	}
	if strings.HasPrefix(s.Stage, "text:") || strings.HasPrefix(s.Stage, "edittext:") {
		transcript := strings.TrimSpace(m.Text)
		if transcript == "" || len([]rune(transcript)) > 1000 {
			return a.send(ctx, userID, "Please type what you said (up to 1,000 characters).", nil)
		}
		kind := strings.TrimPrefix(strings.TrimPrefix(s.Stage, "edittext:"), "text:")
		if err := a.saveTranscript(ctx, userID, s.PairID, kind, transcript); err != nil {
			return err
		}
		if strings.HasPrefix(s.Stage, "edittext:") {
			if err := a.clearSession(ctx, userID); err != nil {
				return err
			}
			if err := a.send(ctx, userID, "Transcript updated. Please keep both recordings in the pair on the same sentence.", nil); err != nil {
				return err
			}
			return a.showPair(ctx, userID, s.PairID)
		}
		if kind == "sleepy" {
			s.Stage = "voice:awake"
			if err := a.putSession(ctx, userID, s); err != nil {
				return err
			}
			return a.send(ctx, userID, "Now record your awake voice. Say the same sentence as in the sleepy note. Aim for 5 seconds; 10 seconds maximum.", nil)
		}
		if err := a.completePair(ctx, userID, s.PairID); err != nil {
			return err
		}
		if err := a.clearSession(ctx, userID); err != nil {
			return err
		}
		return a.send(ctx, userID, "Pair saved. Record another pair or view your pairs below.", actionButtons())
	}
	return a.send(ctx, userID, "Use the Submit or Record a new one button under your last recording.", nil)
}

func (a *app) handleCallback(ctx context.Context, userID int64, data string) error {
	if a.cfg.adminID != 0 && userID == a.cfg.adminID && strings.HasPrefix(data, "admin:") {
		return a.handleAdminCallback(ctx, data)
	}
	parts := strings.Split(data, ":")
	if len(parts) == 0 {
		return nil
	}
	s, err := a.getSession(ctx, userID)
	if err != nil {
		return err
	}
	switch parts[0] {
	case "lang":
		if len(parts) != 2 || (parts[1] != "en" && parts[1] != "fa") {
			break
		}
		if err := a.setLanguage(ctx, userID, parts[1]); err != nil {
			return err
		}
		ctx = withLanguage(ctx, parts[1])
		return a.send(ctx, userID, welcome, actionButtons())
	case "action":
		if len(parts) != 2 {
			break
		}
		switch parts[1] {
		case "new":
			return a.startPair(ctx, userID, false)
		case "my":
			return a.listPairs(ctx, userID, 0)
		case "help":
			return a.send(ctx, userID, welcome, actionButtons())
		}
	case "newyes":
		if len(parts) == 2 && parts[1] == s.PairID && s.Stage != "" && !strings.HasPrefix(s.Stage, "edit") {
			return a.startPair(ctx, userID, true)
		}
	case "keep":
		if len(parts) == 1 {
			return a.send(ctx, userID, "Okay. Continue your current pair using the latest prompt, or use /cancel to discard it.", nil)
		}
	case "accept", "retry":
		if len(parts) != 2 || s.Nonce == "" || parts[1] != s.Nonce || s.Candidate == nil || !(strings.HasPrefix(s.Stage, "confirm:") || strings.HasPrefix(s.Stage, "editconfirm:")) {
			return a.send(ctx, userID, "That recording choice has expired. Follow the latest prompt.", nil)
		}
		kind := strings.TrimPrefix(strings.TrimPrefix(s.Stage, "editconfirm:"), "confirm:")
		editing := strings.HasPrefix(s.Stage, "editconfirm:")
		if parts[0] == "retry" {
			s.Candidate, s.Nonce = nil, ""
			if editing {
				s.Stage = "editvoice:" + kind
			} else {
				s.Stage = "voice:" + kind
			}
			if err := a.putSession(ctx, userID, s); err != nil {
				return err
			}
			return a.send(ctx, userID, fmt.Sprintf(translate(ctx, "Send a new %s voice note. Aim for 5 seconds."), translateKind(ctx, kind)), nil)
		}
		key, actualSize, err := a.storeVoice(ctx, userID, s.PairID, kind, s.Candidate)
		if err != nil {
			return err
		}
		s.Candidate.FileSize = actualSize
		if s.Candidate.MimeType == "" {
			s.Candidate.MimeType = "audio/ogg"
		}
		if err := a.saveVoice(ctx, userID, s.PairID, kind, key, s.Candidate); err != nil {
			_ = a.s3.RemoveObject(ctx, a.cfg.bucket, key, minio.RemoveObjectOptions{})
			return err
		}
		s.Candidate, s.Nonce = nil, ""
		if editing {
			if err := a.clearSession(ctx, userID); err != nil {
				return err
			}
			if err := a.processDeletions(ctx); err != nil {
				log.Printf("old audio cleanup: %v", err)
			}
			if err := a.send(ctx, userID, "Recording replaced. Please keep both recordings in the pair on the same sentence. Edit its transcript if the wording changed.", nil); err != nil {
				return err
			}
			return a.showPair(ctx, userID, s.PairID)
		}
		s.Stage = "text:" + kind
		if err := a.putSession(ctx, userID, s); err != nil {
			return err
		}
		return a.send(ctx, userID, fmt.Sprintf(translate(ctx, "Type exactly what you said in that %s recording."), translateKind(ctx, kind)), nil)
	case "pair":
		if len(parts) == 2 {
			return a.showPair(ctx, userID, parts[1])
		}
	case "page":
		if len(parts) == 2 {
			n, err := strconv.Atoi(parts[1])
			if err == nil && n >= 0 {
				return a.listPairs(ctx, userID, n)
			}
		}
	case "editvoice", "edittext":
		if len(parts) != 3 || (parts[2] != "sleepy" && parts[2] != "awake") {
			break
		}
		if _, err := a.getPair(ctx, userID, parts[1]); err != nil {
			return a.send(ctx, userID, "Pair not found.", nil)
		}
		if s.Stage != "" && !strings.HasPrefix(s.Stage, "edit") {
			return a.send(ctx, userID, "Finish or /cancel your current pair first.", nil)
		}
		if parts[0] == "editvoice" {
			s.Stage = "editvoice:" + parts[2]
		} else {
			s.Stage = "edittext:" + parts[2]
		}
		s.PairID, s.Candidate, s.Nonce = parts[1], nil, ""
		if err := a.putSession(ctx, userID, s); err != nil {
			return err
		}
		if parts[0] == "editvoice" {
			return a.send(ctx, userID, fmt.Sprintf(translate(ctx, "Send a replacement %s Telegram voice note. Aim for 5 seconds; 10 seconds maximum."), translateKind(ctx, parts[2])), nil)
		}
		return a.send(ctx, userID, fmt.Sprintf(translate(ctx, "Type the corrected transcript for the %s recording."), translateKind(ctx, parts[2])), nil)
	case "play":
		if len(parts) != 3 || (parts[2] != "sleepy" && parts[2] != "awake") {
			break
		}
		p, err := a.getPair(ctx, userID, parts[1])
		if err != nil {
			return a.send(ctx, userID, "Pair not found.", nil)
		}
		fileID := p.Sleepy.FileID
		if parts[2] == "awake" {
			fileID = p.Awake.FileID
		}
		if fileID == "" {
			return a.send(ctx, userID, "Recording unavailable.", nil)
		}
		return a.call(ctx, "sendVoice", map[string]any{"chat_id": userID, "voice": fileID}, nil)
	case "delete", "deleteyes":
		if len(parts) != 2 {
			break
		}
		if _, err := a.getPair(ctx, userID, parts[1]); err != nil {
			return a.send(ctx, userID, "Pair not found.", nil)
		}
		if parts[0] == "delete" {
			return a.send(ctx, userID, "Permanently delete this pair and both audio files?", [][]map[string]string{{button("Delete permanently", "deleteyes:"+parts[1]), button("Keep it", "pair:"+parts[1])}})
		}
		if err := a.markDeleting(ctx, userID, parts[1]); err != nil {
			return err
		}
		if s.PairID == parts[1] {
			_ = a.clearSession(ctx, userID)
		}
		if err := a.processDeletions(ctx); err != nil {
			log.Printf("deletion pending: %v", err)
		}
		return a.send(ctx, userID, "Pair deleted. If storage was temporarily unavailable, cleanup will retry when the bot restarts.", nil)
	}
	return a.send(ctx, userID, "That button is no longer valid. Use /my to see your pairs.", nil)
}

func (a *app) startPair(ctx context.Context, userID int64, confirmed bool) error {
	s, err := a.getSession(ctx, userID)
	if err != nil {
		return err
	}
	if s.Stage != "" && !strings.HasPrefix(s.Stage, "edit") && s.PairID != "" {
		if !confirmed {
			return a.send(ctx, userID, "Starting a new pair will discard your unfinished pair. Start over?", [][]map[string]string{{button("Start over", "newyes:"+s.PairID), button("Keep current pair", "keep")}})
		}
		if err := a.markDeleting(ctx, userID, s.PairID); err != nil {
			return err
		}
		if err := a.processDeletions(ctx); err != nil {
			log.Printf("abandoned draft cleanup: %v", err)
		}
	}
	id := randomID()
	if _, err := a.db.Exec(ctx, `INSERT INTO pairs(id,user_id,status) VALUES($1,$2,'draft')`, id, userID); err != nil {
		return err
	}
	if err := a.putSession(ctx, userID, session{Stage: "voice:sleepy", PairID: id}); err != nil {
		return err
	}
	return a.send(ctx, userID, "First, send a sleepy voice note. Say a sentence of your choice, aim for 5 seconds, and keep it under 10 seconds. You will say the same sentence in the awake note.", nil)
}

func (a *app) cancel(ctx context.Context, userID int64) error {
	s, err := a.getSession(ctx, userID)
	if err != nil {
		return err
	}
	if s.Stage == "" {
		return a.send(ctx, userID, "Nothing to cancel.", nil)
	}
	if !strings.HasPrefix(s.Stage, "edit") && s.PairID != "" {
		if err := a.markDeleting(ctx, userID, s.PairID); err != nil {
			return err
		}
		if err := a.processDeletions(ctx); err != nil {
			log.Printf("draft cleanup: %v", err)
		}
	}
	if err := a.clearSession(ctx, userID); err != nil {
		return err
	}
	return a.send(ctx, userID, "Stopped. Choose what to do next.", actionButtons())
}

func (a *app) listPairs(ctx context.Context, userID int64, page int) error {
	if page > 100000 {
		return a.send(ctx, userID, "Page out of range.", nil)
	}
	rows, err := a.db.Query(ctx, `SELECT id,created_at FROM pairs WHERE user_id=$1 AND status='complete' ORDER BY created_at DESC,id DESC LIMIT 11 OFFSET $2`, userID, page*10)
	if err != nil {
		return err
	}
	defer rows.Close()
	buttons := [][]map[string]string{}
	n := 0
	for rows.Next() {
		var id string
		var created time.Time
		if err := rows.Scan(&id, &created); err != nil {
			return err
		}
		if n < 10 {
			buttons = append(buttons, []map[string]string{button(created.UTC().Format("2006-01-02 15:04 UTC")+" · "+id[:8], "pair:"+id)})
		}
		n++
	}
	if err := rows.Err(); err != nil {
		return err
	}
	nav := []map[string]string{}
	if page > 0 {
		nav = append(nav, button("Previous", "page:"+strconv.Itoa(page-1)))
	}
	if n > 10 {
		nav = append(nav, button("Next", "page:"+strconv.Itoa(page+1)))
	}
	if len(nav) > 0 {
		buttons = append(buttons, nav)
	}
	if len(buttons) == 0 {
		return a.send(ctx, userID, "No submitted pairs yet. Ready to record your first pair?", actionButtons())
	}
	return a.send(ctx, userID, "Your submitted pairs:", buttons)
}

func (a *app) showPair(ctx context.Context, userID int64, id string) error {
	p, err := a.getPair(ctx, userID, id)
	if err != nil {
		return a.send(ctx, userID, "Pair not found.", nil)
	}
	text := fmt.Sprintf(translate(ctx, "Pair %s · %s\nSleepy (%ds): %s\nAwake (%ds): %s\n\nBoth recordings should say the same sentence."), p.ID[:8], p.CreatedAt.UTC().Format("2006-01-02 15:04 UTC"), p.Sleepy.Duration, p.Sleepy.Transcript, p.Awake.Duration, p.Awake.Transcript)
	return a.send(ctx, userID, text, [][]map[string]string{
		{button("Play sleepy", "play:"+id+":sleepy"), button("Play awake", "play:"+id+":awake")},
		{button("Replace sleepy voice", "editvoice:"+id+":sleepy"), button("Edit sleepy text", "edittext:"+id+":sleepy")},
		{button("Replace awake voice", "editvoice:"+id+":awake"), button("Edit awake text", "edittext:"+id+":awake")},
		{button("Delete pair", "delete:"+id)},
	})
}
