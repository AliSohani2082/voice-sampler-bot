package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

type voice struct {
	FileID       string `json:"file_id"`
	FileUniqueID string `json:"file_unique_id"`
	Duration     int    `json:"duration"`
	MimeType     string `json:"mime_type"`
	FileSize     int64  `json:"file_size"`
}

type tgUser struct {
	ID        int64  `json:"id"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	Username  string `json:"username"`
}

type chat struct {
	ID   int64  `json:"id"`
	Type string `json:"type"`
}

type message struct {
	MessageID int    `json:"message_id"`
	From      tgUser `json:"from"`
	Chat      chat   `json:"chat"`
	Text      string `json:"text"`
	Voice     *voice `json:"voice"`
	Photo     []any  `json:"photo"`
}

type callback struct {
	ID      string   `json:"id"`
	From    tgUser   `json:"from"`
	Message *message `json:"message"`
	Data    string   `json:"data"`
}

type update struct {
	UpdateID int64     `json:"update_id"`
	Message  *message  `json:"message"`
	Callback *callback `json:"callback_query"`
}

func (a *app) call(ctx context.Context, method string, params any, out any) error {
	body, err := json.Marshal(params)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.apiURL+method, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := a.http.Do(req)
	if err != nil {
		return fmt.Errorf("Telegram request failed: %s", strings.ReplaceAll(err.Error(), a.cfg.token, "[redacted]"))
	}
	defer resp.Body.Close()
	var envelope struct {
		OK          bool            `json:"ok"`
		Description string          `json:"description"`
		Result      json.RawMessage `json:"result"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 25*1024*1024)).Decode(&envelope); err != nil {
		return err
	}
	if !envelope.OK {
		return fmt.Errorf("telegram %s: %s", method, envelope.Description)
	}
	if out != nil {
		return json.Unmarshal(envelope.Result, out)
	}
	return nil
}

func (a *app) send(ctx context.Context, chatID int64, text string, buttons [][]map[string]string) error {
	p := map[string]any{"chat_id": chatID, "text": translate(ctx, text)}
	if len(buttons) > 0 {
		for _, row := range buttons {
			for _, b := range row {
				b["text"] = translate(ctx, b["text"])
			}
		}
		p["reply_markup"] = map[string]any{"inline_keyboard": buttons}
	}
	return a.call(ctx, "sendMessage", p, nil)
}

func button(label, data string) map[string]string {
	return map[string]string{"text": label, "callback_data": data}
}

func (a *app) registerMenu(ctx context.Context) error {
	commands := []map[string]string{
		{"command": "start", "description": "Choose language / انتخاب زبان"},
		{"command": "new", "description": "Record a pair / ضبط جفت صدا"},
		{"command": "my", "description": "View my pairs / دیدن جفت‌ها"},
		{"command": "help", "description": "How it works / راهنما"},
		{"command": "cancel", "description": "Cancel current pair / لغو جفت فعلی"},
	}
	if err := a.call(ctx, "setMyCommands", map[string]any{"commands": commands}, nil); err != nil {
		return err
	}
	return a.call(ctx, "setChatMenuButton", map[string]any{"menu_button": map[string]string{"type": "commands"}}, nil)
}

func (a *app) poll(ctx context.Context) {
	var offset int64
	if err := a.db.QueryRow(ctx, `SELECT offset_value FROM bot_state WHERE id=1`).Scan(&offset); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		log.Printf("read update offset: %v", err)
	}
	lastCleanup := time.Now()
	for ctx.Err() == nil {
		if time.Since(lastCleanup) > time.Minute {
			if err := a.processDeletions(ctx); err != nil {
				log.Printf("deletion cleanup: %v", err)
			}
			lastCleanup = time.Now()
		}
		var updates []update
		p := map[string]any{"offset": offset, "timeout": 40, "allowed_updates": []string{"message", "callback_query"}}
		if err := a.call(ctx, "getUpdates", p, &updates); err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Printf("getUpdates: %v", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(3 * time.Second):
			}
			continue
		}
		for _, u := range updates {
			if err := a.handle(ctx, u); err != nil {
				log.Printf("update %d: %v", u.UpdateID, err)
				var userID int64
				if u.Message != nil {
					userID = u.Message.From.ID
				}
				if u.Callback != nil {
					userID = u.Callback.From.ID
				}
				if ok, _ := a.isAllowed(ctx, userID); ok {
					_ = a.send(ctx, userID, "I couldn't finish that action. Please try again or use /start for help.", nil)
				}
			}
			offset = u.UpdateID + 1
			if _, err := a.db.Exec(ctx, `INSERT INTO bot_state(id,offset_value) VALUES(1,$1) ON CONFLICT(id) DO UPDATE SET offset_value=$1`, offset); err != nil {
				log.Printf("persist update offset: %v", err)
			}
		}
	}
}

func (a *app) handle(ctx context.Context, u update) error {
	if u.Callback != nil {
		q := u.Callback
		if q.Message == nil || q.Message.Chat.Type != "private" || q.Message.Chat.ID != q.From.ID {
			_ = a.call(ctx, "answerCallbackQuery", map[string]any{"callback_query_id": q.ID}, nil)
			return nil
		}
		if strings.HasPrefix(q.Data, "acc:") {
			return a.handleAccess(ctx, q) // answers the callback itself, so admins can get an alert
		}
		_ = a.call(ctx, "answerCallbackQuery", map[string]any{"callback_query_id": q.ID}, nil)
		if ok, err := a.isAllowed(ctx, q.From.ID); err != nil {
			return err
		} else if !ok {
			return a.promptAccess(ctx, q.From.ID)
		}
		language, err := a.getLanguage(ctx, q.From.ID)
		if err != nil {
			return err
		}
		ctx = withLanguage(ctx, language)
		return a.handleCallback(ctx, q.From.ID, q.Data)
	}
	if u.Message == nil {
		return nil
	}
	m := u.Message
	if m.Chat.Type != "private" || m.Chat.ID != m.From.ID {
		return nil
	}
	if ok, err := a.isAllowed(ctx, m.From.ID); err != nil {
		return err
	} else if !ok {
		return a.promptAccess(ctx, m.From.ID)
	}
	language, err := a.getLanguage(ctx, m.From.ID)
	if err != nil {
		return err
	}
	ctx = withLanguage(ctx, language)
	return a.handleMessage(ctx, m)
}
