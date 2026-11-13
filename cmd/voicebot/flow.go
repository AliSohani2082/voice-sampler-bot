package main

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"
)

const welcome = "Hi! 👋 This bot helps collect voice samples for Professor Zahedi's (زاهدی) Statistical Machine Learning class. Each student is asked to record 20 pairs: one sleepy and one awake voice note per pair, about 5 seconds each. At the end of the semester, these recordings will form a dataset for a machine learning project. Say the same sentence in both notes and type it once for the pair. Notes over 10 seconds are rejected. Your audio and text stay stored until you delete a pair. Created by @AliSohani2082. Choose an option below or open the Menu beside the message field."

func actionButtons() [][]map[string]string {
	return [][]map[string]string{{button("Record a pair", "action:new"), button("My pairs", "action:my")}, {button("Help", "action:help")}}
}

func (a *app) handleMessage(ctx context.Context, m *message) error {
	userID := m.From.ID
	if strings.HasPrefix(m.Text, "/") {
		// Everything after the command word is the argument text; the registry
		// decides what it means. strings.Cut also makes a bare "/" harmless.
		head, rest, _ := strings.Cut(m.Text, " ")
		name := strings.ToLower(strings.TrimPrefix(strings.SplitN(head, "@", 2)[0], "/"))
		if strings.HasPrefix(name, "p_") { // tappable link from /status l
			return a.showPairByLink(ctx, userID, "/"+name)
		}
		return a.runCommand(ctx, userID, name, rest)
	}
	s, err := a.getSession(ctx, userID)
	if err != nil {
		return err
	}
	// A command waiting for a typed argument takes this message as its answer.
	// Checked before the recording stages, whose prefixes await: never matches.
	if strings.HasPrefix(s.Stage, awaitPrefix) {
		return a.resumePending(ctx, userID, s, m.Text)
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
		if s.Stage == "text:sleepy" { // Finish drafts started before the pair transcript flow changed.
			cmd, err := a.db.Exec(ctx, `UPDATE samples s SET transcript=$1 FROM pairs p WHERE s.pair_id=p.id AND p.id=$2 AND p.user_id=$3 AND p.status='draft' AND s.kind='sleepy'`, transcript, s.PairID, userID)
			if err != nil {
				return err
			}
			if cmd.RowsAffected() != 1 {
				return fmt.Errorf("sleepy sample unavailable")
			}
			s.Stage = "voice:awake"
			if err := a.putSession(ctx, userID, s); err != nil {
				return err
			}
			return a.send(ctx, userID, "Now record your awake voice. Say the same sentence as in the sleepy note. Aim for 5 seconds; 10 seconds maximum.", nil)
		}
		if err := a.saveTranscript(ctx, userID, s.PairID, transcript); err != nil {
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
	if a.cfg.fullAdmins[userID] && strings.HasPrefix(data, "admin:") {
		return a.handleAdminCallback(ctx, userID, data)
	}
	// An argument prompt's buttons replay the whole command line, so they go
	// straight back through the one dispatch path — which re-checks adminOnly,
	// since only the admin: prefix above is gated before here.
	if strings.HasPrefix(data, cmdPrefix) {
		if parts := strings.SplitN(data, ":", 3); len(parts) == 3 {
			return a.runCommand(ctx, userID, parts[1], strings.ReplaceAll(parts[2], ".", " "))
		}
		return a.send(ctx, userID, unknownCommand, actionButtons())
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
	// lang:, action: and dl: predate the command registry. They stay as thin
	// forwarders so buttons already sitting in users' chat histories keep working.
	case "lang":
		if len(parts) == 2 && (parts[1] == "en" || parts[1] == "fa") {
			return a.runCommand(ctx, userID, "start", parts[1])
		}
	case "action":
		// Named explicitly, so a forged action:stats cannot reach the admin check.
		if len(parts) == 2 && (parts[1] == "new" || parts[1] == "my" || parts[1] == "help") {
			return a.runCommand(ctx, userID, parts[1], "")
		}
	case "dl": // always exports the pressing user's own data
		if len(parts) == 2 {
			return a.runCommand(ctx, userID, "download", parts[1])
		}
	case "newyes":
		if len(parts) == 2 && parts[1] == s.PairID && s.Stage != "" && !strings.HasPrefix(s.Stage, "edit") {
			// The same gate /new applies. Reachable without it only by replaying a
			// Start over button from before onboarding existed, which must not
			// create a pair with no details attached.
			if handled, _, err := requireProfile(a, ctx, userID, cmdArgs{}); err != nil || handled {
				return err
			}
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
		key := newObjectKey(userID, s.PairID, kind)
		if s.Candidate.MimeType == "" {
			s.Candidate.MimeType = "audio/ogg"
		}
		if err := a.saveVoice(ctx, userID, s.PairID, kind, key, s.Candidate); err != nil {
			return err
		}
		a.kickUploads() // audio is copied to storage in the background
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
		if kind == "sleepy" {
			s.Stage = "voice:awake"
			if err := a.putSession(ctx, userID, s); err != nil {
				return err
			}
			return a.send(ctx, userID, "Now record your awake voice. Say the same sentence as in the sleepy note. Aim for 5 seconds; 10 seconds maximum.", nil)
		}
		s.Stage = "text:pair"
		if err := a.putSession(ctx, userID, s); err != nil {
			return err
		}
		return a.send(ctx, userID, "Type the sentence you said in both recordings.", nil)
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
		if (parts[0] == "editvoice" && (len(parts) != 3 || (parts[2] != "sleepy" && parts[2] != "awake"))) || (parts[0] == "edittext" && len(parts) != 2 && (len(parts) != 3 || (parts[2] != "sleepy" && parts[2] != "awake"))) {
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
			s.Stage = "edittext:pair"
		}
		s.PairID, s.Candidate, s.Nonce = parts[1], nil, ""
		if err := a.putSession(ctx, userID, s); err != nil {
			return err
		}
		if parts[0] == "editvoice" {
			return a.send(ctx, userID, fmt.Sprintf(translate(ctx, "Send a replacement %s Telegram voice note. Aim for 5 seconds; 10 seconds maximum."), translateKind(ctx, parts[2])), nil)
		}
		return a.send(ctx, userID, "Type the corrected sentence for both recordings.", nil)
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
	// Checked first: a collection's row carries no pair, so the draft cleanup
	// below would try to delete an empty pair id.
	if strings.HasPrefix(s.Stage, awaitPrefix) {
		if err := a.clearSession(ctx, userID); err != nil {
			return err
		}
		return a.send(ctx, userID, canceled, nil)
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
	text := fmt.Sprintf(translate(ctx, "Pair %s · %s\nSleepy: %ds · Awake: %ds\nTranscript: %s"), p.ID[:8], p.CreatedAt.UTC().Format("2006-01-02 15:04 UTC"), p.Sleepy.Duration, p.Awake.Duration, p.Sleepy.Transcript)
	if p.Sleepy.Transcript != p.Awake.Transcript {
		text = fmt.Sprintf(translate(ctx, "Pair %s · %s\nSleepy (%ds): %s\nAwake (%ds): %s\n\nBoth recordings should say the same sentence."), p.ID[:8], p.CreatedAt.UTC().Format("2006-01-02 15:04 UTC"), p.Sleepy.Duration, p.Sleepy.Transcript, p.Awake.Duration, p.Awake.Transcript)
	}
	return a.send(ctx, userID, text, [][]map[string]string{
		{button("Play sleepy", "play:"+id+":sleepy"), button("Play awake", "play:"+id+":awake")},
		{button("Replace sleepy voice", "editvoice:"+id+":sleepy"), button("Replace awake voice", "editvoice:"+id+":awake")},
		{button("Edit pair transcript", "edittext:"+id)},
		{button("Delete pair", "delete:"+id)},
	})
}
