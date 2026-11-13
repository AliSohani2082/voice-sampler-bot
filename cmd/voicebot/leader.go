package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// leaderChunk keeps each message well under Telegram's 4096-character limit (len counts bytes, which is conservative).
const leaderChunk = 3500

const (
	askPeriod        = "Leaderboard for which period?"
	leaderToday      = "Top recorders today:"
	leaderWeek       = "Top recorders this week:"
	leaderRangeLine  = "Counting %s to %s (Asia/Tehran)"
	leaderEmptyToday = "No pairs have been recorded yet today."
	leaderEmptyWeek  = "No pairs have been recorded yet this week."
	// Written label-first so a contributor with exactly one pair does not read
	// as "1 pairs", without needing a singular form of every string.
	leaderRowShort  = "%d. %s — pairs: %d"
	leaderRowLong   = "%d. %s — pairs: %d · recordings: %d · minutes: %.1f"
	leaderYou       = "You are #%d, with pairs: %d"
	leaderYouNone   = "You have no completed pairs in this range yet."
	leaderAdminNote = "Admins are not ranked."
	leaderAnonymous = "A participant"
)

// leaderBounds returns the half-open [from, to) window for a leaderboard period,
// on the same Asia/Tehran day boundaries /download uses. The week is the current
// calendar week to date, starting Saturday — unlike /download's "This week",
// which is a rolling seven days.
func leaderBounds(kind string, now time.Time) (from, to time.Time, ok bool) {
	n := now.In(exportLoc)
	today := time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, exportLoc)
	to = today.AddDate(0, 0, 1)
	switch kind {
	case "d":
		return today, to, true
	case "w":
		// Days since Saturday: Saturday 0, Sunday 1, ... Friday 6.
		return today.AddDate(0, 0, -((int(today.Weekday()) + 1) % 7)), to, true
	}
	return from, to, false
}

// leaderRow is one contributor's line on the board.
type leaderRow struct {
	Name       string
	Pairs      int
	Recordings int
	Seconds    int
}

// leaderName labels a contributor: the name they declared when onboarding, else
// whatever Telegram last reported. Contributors from before onboarding existed
// fall past the first case, and those the bot has not seen since have no name
// stored at all — they are shown anonymously rather than as a numeric id, which
// the whole class would otherwise be able to read off the board. Both fallbacks
// resolve themselves: the Telegram name returns on their next message and the
// declared one once they onboard.
func leaderName(ctx context.Context, first, last, tgFirst, tgUsername string) string {
	if first != "" || last != "" {
		return truncate(first+" "+last, 32)
	}
	if tgFirst != "" {
		return truncate(tgFirst, 32)
	}
	if tgUsername != "" {
		return truncate("@"+tgUsername, 32)
	}
	return translate(ctx, leaderAnonymous)
}

// leaderRows ranks contributors by completed pairs in the window. Admins are
// excluded, as they are from /stats and /users. A limit of 0 returns every contributor.
func (a *app) leaderRows(ctx context.Context, from, to time.Time, limit int) ([]leaderRow, error) {
	rows, err := a.db.Query(ctx, `SELECT p.user_id,
		  coalesce(pr.first_name,'') AS first_name,
		  coalesce(pr.last_name,'') AS last_name,
		  u.tg_first_name, u.tg_username,
		  count(DISTINCT p.id) AS pairs,
		  count(s.pair_id) AS recordings,
		  coalesce(sum(s.duration_seconds),0) AS seconds
		FROM pairs p
		JOIN users u ON u.user_id=p.user_id
		LEFT JOIN profiles pr ON pr.user_id=p.user_id
		LEFT JOIN samples s ON s.pair_id=p.id
		WHERE p.status='complete' AND p.completed_at>=$1 AND p.completed_at<$2 AND p.user_id<>ALL($3)
		GROUP BY p.user_id, pr.first_name, pr.last_name, u.tg_first_name, u.tg_username
		ORDER BY pairs DESC, seconds DESC, p.user_id
		LIMIT nullif($4,0)`, from, to, a.adminIDs(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []leaderRow
	for rows.Next() {
		var userID int64
		var first, last, tgFirst, tgUsername string
		var r leaderRow
		if err := rows.Scan(&userID, &first, &last, &tgFirst, &tgUsername, &r.Pairs, &r.Recordings, &r.Seconds); err != nil {
			return nil, err
		}
		r.Name = leaderName(ctx, first, last, tgFirst, tgUsername)
		out = append(out, r)
	}
	return out, rows.Err()
}

// leaderRank is the asking user's own standing, so someone outside the listed rows
// still sees where they are. Not found is the ordinary "nothing in this window".
//
// The window repeats leaderRows' ordering exactly, so the number reported here is
// the position the board shows. rank() would instead hand tied contributors a
// shared number and tell the one listed second that they are first; the samples
// join is what makes the seconds tiebreak available, and forces count(DISTINCT)
// because the join multiplies each pair's row.
func (a *app) leaderRank(ctx context.Context, userID int64, from, to time.Time) (rank, pairs int, found bool, err error) {
	err = a.db.QueryRow(ctx, `SELECT rank, pairs FROM (
		  SELECT p.user_id,
		         count(DISTINCT p.id) AS pairs,
		         row_number() OVER (ORDER BY count(DISTINCT p.id) DESC, coalesce(sum(s.duration_seconds),0) DESC, p.user_id) AS rank
		  FROM pairs p
		  LEFT JOIN samples s ON s.pair_id=p.id
		  WHERE p.status='complete' AND p.completed_at>=$1 AND p.completed_at<$2 AND p.user_id<>ALL($3)
		  GROUP BY p.user_id) t WHERE user_id=$4`, from, to, a.adminIDs(), userID).Scan(&rank, &pairs)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, 0, false, nil
	}
	if err != nil {
		return 0, 0, false, err
	}
	return rank, pairs, true, nil
}

// leaderboard renders the board for one period, short or detailed.
func (a *app) leaderboard(ctx context.Context, userID int64, period string, detailed bool) error {
	from, to, ok := leaderBounds(period, time.Now())
	if !ok {
		return a.send(ctx, userID, unrecognizedOption, nil)
	}
	rows, err := a.leaderRows(ctx, from, to, 0) // everyone who submitted in the period
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		if period == "d" {
			return a.send(ctx, userID, leaderEmptyToday, nil)
		}
		return a.send(ctx, userID, leaderEmptyWeek, nil)
	}
	var head strings.Builder
	if period == "d" {
		head.WriteString(translate(ctx, leaderToday))
	} else {
		head.WriteString(translate(ctx, leaderWeek))
	}
	// A day needs no range line: its header already says "today".
	if detailed && period == "w" {
		head.WriteString("\n" + fmt.Sprintf(translate(ctx, leaderRangeLine),
			from.Format("2006-01-02"), to.AddDate(0, 0, -1).Format("2006-01-02")))
	}
	// Everyone is listed, so the board can pass Telegram's message limit; split it into several messages.
	chunks := []string{}
	b := &head
	for i, r := range rows {
		var line string
		if detailed {
			line = fmt.Sprintf(translate(ctx, leaderRowLong), i+1, r.Name, r.Pairs, r.Recordings, float64(r.Seconds)/60)
		} else {
			line = fmt.Sprintf(translate(ctx, leaderRowShort), i+1, r.Name, r.Pairs)
		}
		if b.Len()+len(line)+1 > leaderChunk {
			chunks = append(chunks, b.String())
			b = &strings.Builder{}
		} else if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString(line)
	}
	if detailed {
		if a.cfg.fullAdmins[userID] {
			b.WriteString("\n\n" + translate(ctx, leaderAdminNote))
		} else {
			rank, pairs, found, err := a.leaderRank(ctx, userID, from, to)
			if err != nil {
				return err
			}
			if found {
				b.WriteString("\n\n" + fmt.Sprintf(translate(ctx, leaderYou), rank, pairs))
			} else {
				b.WriteString("\n\n" + translate(ctx, leaderYouNone))
			}
		}
	}
	chunks = append(chunks, b.String())
	for _, c := range chunks {
		if err := a.send(ctx, userID, c, nil); err != nil {
			return err
		}
	}
	return nil
}
