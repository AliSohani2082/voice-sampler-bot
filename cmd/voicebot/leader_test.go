package main

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestLeaderBoundsDay(t *testing.T) {
	// 2026-11-10 21:30 UTC is 2026-11-11 01:00 in Tehran (+03:30), as in
	// TestExportBounds, so "today" there is already the 11th.
	now := time.Date(2026, 11, 10, 21, 30, 0, 0, time.UTC)
	from, to, ok := leaderBounds("d", now)
	if !ok ||
		from.In(exportLoc).Format("2006-01-02 15:04") != "2026-11-11 00:00" ||
		to.In(exportLoc).Format("2006-01-02 15:04") != "2026-11-12 00:00" {
		t.Errorf("day: from=%v to=%v ok=%v", from, to, ok)
	}
	for _, bad := range []string{"", "x", "today", "week", "all", "D", "W", "dw"} {
		if _, _, ok := leaderBounds(bad, now); ok {
			t.Errorf("%q accepted as a period", bad)
		}
	}
}

// The week is the calendar week to date, starting Saturday — deliberately unlike
// /download's rolling seven days. Walking two weeks covers every weekday.
func TestLeaderBoundsWeekStartsSaturday(t *testing.T) {
	start := time.Date(2026, 11, 1, 12, 0, 0, 0, exportLoc)
	for i := range 14 {
		now := start.AddDate(0, 0, i)
		from, to, ok := leaderBounds("w", now)
		if !ok {
			t.Fatalf("%v: not ok", now)
		}
		day := now.Format("2006-01-02")
		if got := from.In(exportLoc).Weekday(); got != time.Saturday {
			t.Errorf("%s (%v): week starts on %v", day, now.Weekday(), got)
		}
		if got := to.In(exportLoc).Format("2006-01-02"); got != now.AddDate(0, 0, 1).Format("2006-01-02") {
			t.Errorf("%s: window ends %s, want tomorrow", day, got)
		}
		// Tehran has had no daylight saving since 2022, so a day is 24 hours.
		days := int(to.Sub(from).Hours() / 24)
		if want := int(now.Weekday()+1)%7 + 1; days != want {
			t.Errorf("%s (%v): %d days, want %d", day, now.Weekday(), days, want)
		}
		switch now.Weekday() {
		case time.Saturday:
			if days != 1 {
				t.Errorf("%s: a week that just started covers %d days", day, days)
			}
		case time.Friday:
			if days != 7 {
				t.Errorf("%s: a full week covers %d days", day, days)
			}
		}
	}
}

// The board shows the name a participant declared when onboarding. The fallbacks
// only cover contributors from before onboarding existed, and only until their
// next /new.
func TestLeaderName(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		first, last, tgFirst, tgUsername, want string
	}{
		{"Ali", "Sohani", "Tg", "tguser", "Ali Sohani"},
		{"Ali", "", "Tg", "tguser", "Ali"},
		{"", "Sohani", "Tg", "tguser", "Sohani"},
		{"", "", "Tg", "tguser", "Tg"},
		{"", "", "", "tguser", "@tguser"},
		{"", "", "", "", "User 42"},
	} {
		if got := leaderName(ctx, tc.first, tc.last, tc.tgFirst, tc.tgUsername, 42); got != tc.want {
			t.Errorf("leaderName(%q, %q, %q, %q) = %q, want %q", tc.first, tc.last, tc.tgFirst, tc.tgUsername, got, tc.want)
		}
	}
	// One very long name must not stretch every row of the board.
	long := leaderName(ctx, strings.Repeat("a", 40), strings.Repeat("b", 40), "", "", 42)
	if n := len([]rune(long)); n != 32 {
		t.Errorf("a long name rendered %d runes: %q", n, long)
	}
}

// The period tokens the prompt offers have to be the ones leaderBounds accepts,
// the same contract TestExportBounds holds /download to.
func TestLeaderPeriodsHaveBounds(t *testing.T) {
	now := time.Now()
	for _, o := range slotByName(t, "leader", "period").options {
		if _, _, ok := leaderBounds(o.token, now); !ok {
			t.Errorf("leader period %q has no window", o.token)
		}
	}
}
