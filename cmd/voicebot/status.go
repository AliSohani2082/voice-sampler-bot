package main

import (
	"context"
	"fmt"
	"strings"
	"time"
)

const (
	statusDays      = 30
	statusLongPairs = 100
	statusChunk     = 3500 // stay well under Telegram's 4096-character message limit
)

// pairLinkPrefixLen hex characters of a pair ID fit in a tappable /p_<prefix> command.
const pairLinkPrefixLen = 12

// isStatusLong reports whether the /status arguments ask for the long form ("l" or "long").
func isStatusLong(text string) bool {
	f := strings.Fields(text)
	if len(f) < 2 {
		return false
	}
	arg := strings.ToLower(f[1])
	return arg == "l" || arg == "long"
}

func (a *app) status(ctx context.Context, userID int64, long bool) error {
	if long {
		return a.statusLong(ctx, userID)
	}
	rows, err := a.db.Query(ctx, `SELECT (created_at AT TIME ZONE 'UTC')::date, count(*) FROM pairs WHERE user_id=$1 AND status='complete' GROUP BY 1 ORDER BY 1 DESC LIMIT $2`, userID, statusDays)
	if err != nil {
		return err
	}
	defer rows.Close()
	counts := map[string]int{}
	var days []string
	total := 0
	for rows.Next() {
		var d time.Time
		var n int
		if err := rows.Scan(&d, &n); err != nil {
			return err
		}
		k := d.Format("2006-01-02")
		counts[k] = n
		days = append(days, k)
		total += n
	}
	if err := rows.Err(); err != nil {
		return err
	}
	today := time.Now().UTC().Format("2006-01-02")
	if _, ok := counts[today]; !ok {
		days = append([]string{today}, days...)
	}
	var b strings.Builder
	b.WriteString(translate(ctx, "Your submitted voices (UTC days):") + "\n")
	for _, d := range days {
		label := d
		if d == today {
			label += " (" + translate(ctx, "today") + ")"
		}
		fmt.Fprintf(&b, "%s: %d "+translate(ctx, "voices")+" · %d "+translate(ctx, "pairs")+"\n", label, counts[d]*2, counts[d])
	}
	fmt.Fprintf(&b, "\n"+translate(ctx, "Total (last %d days): %d voices · %d pairs")+"\n", statusDays, total*2, total)
	b.WriteString(translate(ctx, "Use /status l to see each pair's text."))
	return a.send(ctx, userID, b.String(), nil)
}

func (a *app) statusLong(ctx context.Context, userID int64) error {
	rows, err := a.db.Query(ctx, `SELECT p.id,p.created_at,coalesce(max(s.transcript) FILTER (WHERE s.kind='sleepy'),'') FROM pairs p LEFT JOIN samples s ON s.pair_id=p.id WHERE p.user_id=$1 AND p.status='complete' GROUP BY p.id,p.created_at ORDER BY p.created_at DESC,p.id DESC LIMIT $2`, userID, statusLongPairs)
	if err != nil {
		return err
	}
	defer rows.Close()
	var chunks []string
	var b strings.Builder
	flush := func() {
		if b.Len() > 0 {
			chunks = append(chunks, strings.TrimRight(b.String(), "\n"))
			b.Reset()
		}
	}
	day := ""
	n := 0
	for rows.Next() {
		var id, text string
		var created time.Time
		if err := rows.Scan(&id, &created, &text); err != nil {
			return err
		}
		n++
		text = strings.Join(strings.Fields(text), " ")
		if r := []rune(text); len(r) > 120 {
			text = string(r[:120]) + "…"
		}
		d := created.UTC().Format("2006-01-02")
		var entry strings.Builder
		if d != day {
			day = d
			entry.WriteString("\n" + d + "\n")
		}
		fmt.Fprintf(&entry, "%s  %s\n/p_%s\n", created.UTC().Format("15:04"), text, id[:pairLinkPrefixLen])
		if b.Len()+entry.Len() > statusChunk {
			flush()
		}
		b.WriteString(entry.String())
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if n == 0 {
		return a.send(ctx, userID, "No submitted pairs yet. Ready to record your first pair?", actionButtons())
	}
	flush()
	chunks[0] = translate(ctx, "Your submitted pairs (tap a /p_ link to open, edit or delete):") + "\n" + chunks[0]
	for _, c := range chunks {
		if err := a.send(ctx, userID, c, nil); err != nil {
			return err
		}
	}
	return nil
}

// showPairByLink opens the pair for a /p_<prefix> command.
func (a *app) showPairByLink(ctx context.Context, userID int64, command string) error {
	prefix := strings.TrimPrefix(command, "/p_")
	if len(prefix) != pairLinkPrefixLen || strings.Trim(prefix, "0123456789abcdef") != "" {
		return a.send(ctx, userID, "Pair not found.", nil)
	}
	var id string
	err := a.db.QueryRow(ctx, `SELECT id FROM pairs WHERE user_id=$1 AND status='complete' AND id LIKE $2 LIMIT 1`, userID, prefix+"%").Scan(&id)
	if err != nil {
		return a.send(ctx, userID, "Pair not found.", nil)
	}
	return a.showPair(ctx, userID, id)
}
