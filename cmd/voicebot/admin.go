package main

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"
)

// adminIDs lists every full admin so queries can exclude them (non-nil: a nil slice would encode as SQL NULL).
func (a *app) adminIDs() []int64 {
	ids := make([]int64, 0, len(a.cfg.fullAdmins))
	for id := range a.cfg.fullAdmins {
		ids = append(ids, id)
	}
	return ids
}

func (a *app) adminStats(ctx context.Context, to int64) error {
	var users, pairs, drafts, recordings int
	var bytes int64
	err := a.db.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM users WHERE user_id<>ALL($1)),
		(SELECT count(*) FROM pairs WHERE status='complete'),
		(SELECT count(*) FROM pairs WHERE status='draft'),
		(SELECT count(*) FROM samples s JOIN pairs p ON p.id=s.pair_id WHERE p.status='complete'),
		(SELECT coalesce(sum(s.file_size_bytes),0) FROM samples s JOIN pairs p ON p.id=s.pair_id WHERE p.status='complete')`, a.adminIDs()).Scan(&users, &pairs, &drafts, &recordings, &bytes)
	if err != nil {
		return err
	}
	return a.send(ctx, to, fmt.Sprintf("Users: %d\nCompleted pairs: %d\nUnfinished pairs: %d\nRecordings: %d\nStored audio: %.1f MiB", users, pairs, drafts, recordings, float64(bytes)/(1024*1024)), nil)
}

func (a *app) adminUsers(ctx context.Context, to int64, page int) error {
	if page < 0 || page > 100000 {
		return a.send(ctx, to, "Page out of range.", nil)
	}
	rows, err := a.db.Query(ctx, `SELECT user_id FROM users WHERE user_id<>ALL($1) ORDER BY user_id LIMIT 11 OFFSET $2`, a.adminIDs(), page*10)
	if err != nil {
		return err
	}
	defer rows.Close()
	var buttons [][]map[string]string
	n := 0
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return err
		}
		if n < 10 {
			buttons = append(buttons, []map[string]string{button(strconv.FormatInt(id, 10), "admin:user:"+strconv.FormatInt(id, 10)+":0")})
		}
		n++
	}
	if err := rows.Err(); err != nil {
		return err
	}
	var nav []map[string]string
	if page > 0 {
		nav = append(nav, button("Previous", "admin:users:"+strconv.Itoa(page-1)))
	}
	if n > 10 {
		nav = append(nav, button("Next", "admin:users:"+strconv.Itoa(page+1)))
	}
	if len(nav) > 0 {
		buttons = append(buttons, nav)
	}
	if len(buttons) == 0 {
		return a.send(ctx, to, "No users yet.", nil)
	}
	return a.send(ctx, to, "Users:", buttons)
}

func (a *app) adminUserPairs(ctx context.Context, to, userID int64, page int) error {
	if page < 0 || page > 100000 {
		return a.send(ctx, to, "Page out of range.", nil)
	}
	var exists bool
	if err := a.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE user_id=$1 AND user_id<>ALL($2))`, userID, a.adminIDs()).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return a.send(ctx, to, "User not found.", nil)
	}
	rows, err := a.db.Query(ctx, `SELECT id,created_at FROM pairs WHERE user_id=$1 AND status='complete' ORDER BY created_at DESC,id DESC LIMIT 11 OFFSET $2`, userID, page*10)
	if err != nil {
		return err
	}
	defer rows.Close()
	var buttons [][]map[string]string
	n := 0
	for rows.Next() {
		var id string
		var created time.Time
		if err := rows.Scan(&id, &created); err != nil {
			return err
		}
		if n < 10 {
			buttons = append(buttons, []map[string]string{button(created.UTC().Format("2006-01-02 15:04 UTC")+" · "+id[:8], "admin:pair:"+strconv.FormatInt(userID, 10)+":"+id)})
		}
		n++
	}
	if err := rows.Err(); err != nil {
		return err
	}
	var nav []map[string]string
	if page > 0 {
		nav = append(nav, button("Previous", fmt.Sprintf("admin:user:%d:%d", userID, page-1)))
	}
	if n > 10 {
		nav = append(nav, button("Next", fmt.Sprintf("admin:user:%d:%d", userID, page+1)))
	}
	if len(nav) > 0 {
		buttons = append(buttons, nav)
	}
	label, action := "Add to allowlist", "allow"
	if a.cfg.allowed[userID] {
		label, action = "Remove from allowlist", "unallow"
	}
	buttons = append(buttons, []map[string]string{button(label, fmt.Sprintf("admin:%s:%d:0", action, userID))})
	buttons = append(buttons, []map[string]string{button("Back to users", "admin:users:0")})
	return a.send(ctx, to, fmt.Sprintf("User %d · submitted pairs", userID), buttons)
}

func (a *app) adminPair(ctx context.Context, to, userID int64, id string) error {
	p, err := a.getPair(ctx, userID, id)
	if err != nil {
		return a.send(ctx, to, "Pair not found.", nil)
	}
	text := fmt.Sprintf("User %d · Pair %s · %s\nSleepy (%ds): %s\nAwake (%ds): %s", userID, p.ID[:8], p.CreatedAt.UTC().Format("2006-01-02 15:04 UTC"), p.Sleepy.Duration, p.Sleepy.Transcript, p.Awake.Duration, p.Awake.Transcript)
	return a.send(ctx, to, text, [][]map[string]string{
		{button("Play sleepy", fmt.Sprintf("admin:play:%d:%s:sleepy", userID, id)), button("Play awake", fmt.Sprintf("admin:play:%d:%s:awake", userID, id))},
		{button("Back to user", fmt.Sprintf("admin:user:%d:0", userID))},
	})
}

func (a *app) handleAdminCallback(ctx context.Context, to int64, data string) error {
	parts := strings.Split(data, ":")
	if len(parts) == 3 && parts[1] == "users" {
		page, err := strconv.Atoi(parts[2])
		if err == nil {
			return a.adminUsers(ctx, to, page)
		}
	}
	if len(parts) >= 4 {
		userID, err := strconv.ParseInt(parts[2], 10, 64)
		if err != nil || userID <= 0 || a.cfg.fullAdmins[userID] {
			return a.send(ctx, to, "Invalid user.", nil)
		}
		switch parts[1] {
		case "allow", "unallow":
			if len(parts) == 4 {
				if err := a.setAllowed(ctx, to, parts[2], parts[1] == "allow"); err != nil {
					return err
				}
				return a.adminUserPairs(ctx, to, userID, 0)
			}
		case "user":
			if len(parts) == 4 {
				page, err := strconv.Atoi(parts[3])
				if err == nil {
					return a.adminUserPairs(ctx, to, userID, page)
				}
			}
		case "pair":
			if len(parts) == 4 {
				return a.adminPair(ctx, to, userID, parts[3])
			}
		case "play":
			if len(parts) == 5 && (parts[4] == "sleepy" || parts[4] == "awake") {
				p, err := a.getPair(ctx, userID, parts[3])
				if err != nil {
					return a.send(ctx, to, "Pair not found.", nil)
				}
				fileID := p.Sleepy.FileID
				if parts[4] == "awake" {
					fileID = p.Awake.FileID
				}
				if fileID == "" {
					return a.send(ctx, to, "Recording unavailable.", nil)
				}
				return a.call(ctx, "sendVoice", map[string]any{"chat_id": to, "voice": fileID}, nil)
			}
		}
	}
	return a.send(ctx, to, "That button is no longer valid. Use /users.", nil)
}

func (a *app) setAllowed(ctx context.Context, to int64, rawID string, allowed bool) error {
	id, err := strconv.ParseInt(rawID, 10, 64)
	if err != nil || id <= 0 {
		return a.send(ctx, to, "Use /allow ID or /unallow ID with a positive numeric Telegram user ID.", nil)
	}
	if a.cfg.fullAdmins[id] {
		return a.send(ctx, to, "The admin is always allowed.", nil)
	}
	tx, err := a.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `INSERT INTO allowlist(user_id,allowed) VALUES($1,$2) ON CONFLICT(user_id) DO UPDATE SET allowed=$2`, id, allowed); err != nil {
		return err
	}
	if allowed {
		if _, err := tx.Exec(ctx, `INSERT INTO users(user_id) VALUES($1) ON CONFLICT DO NOTHING`, id); err != nil {
			return err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	if allowed {
		a.cfg.allowed[id] = true
		return a.send(ctx, to, fmt.Sprintf("User %d added to the allowlist and /users. They must send /start to the bot before it can deliver messages to them.", id), nil)
	}
	delete(a.cfg.allowed, id)
	return a.send(ctx, to, fmt.Sprintf("User %d removed from the allowlist.", id), nil)
}

func (a *app) broadcast(ctx context.Context, to int64, message string) error {
	if message == "" || len([]rune(message)) > 4096 {
		return a.send(ctx, to, "Use /broadcast followed by a message of up to 4096 characters.", nil)
	}
	rows, err := a.db.Query(ctx, `SELECT user_id FROM users WHERE user_id<>ALL($1) ORDER BY user_id`, a.adminIDs())
	if err != nil {
		return err
	}
	var recipients []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		recipients = append(recipients, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	sent, failed := 0, 0
	for _, id := range recipients {
		if !a.cfg.allowed[id] {
			continue
		}
		if err := a.send(withLanguage(ctx, "en"), id, message, nil); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			log.Printf("broadcast to %d: %v", id, err)
			failed++
		} else {
			sent++
		}
	}
	return a.send(ctx, to, fmt.Sprintf("Broadcast finished: %d sent, %d failed. Users who have not opened a private chat cannot receive messages yet.", sent, failed), nil)
}
