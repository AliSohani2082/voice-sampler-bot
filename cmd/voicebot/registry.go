package main

import (
	"context"
	"strconv"
	"strings"
)

const allowUsage = "Use /allow ID or /unallow ID with a positive numeric Telegram user ID."

func validateUserID(raw string) (string, string) {
	v := normalizeDigits(strings.TrimSpace(raw))
	id, err := strconv.ParseInt(v, 10, 64)
	if err != nil || id <= 0 {
		return "", allowUsage
	}
	return strconv.FormatInt(id, 10), ""
}

// idArg is the Telegram user id /allow and /unallow both take.
var idArg = argSpec{
	name: "id", kind: argText, mandatory: true, maxRunes: 24,
	ask:      allowUsage,
	validate: validateUserID,
}

// commandSpecs is the single source of truth for dispatch and for the Telegram
// menu. Slice order is menu order, and a command with an empty description stays
// out of the menu.
//
// Rule: a command whose handler mutates data must not have a mandatory choice arg.
// A cmd: button is a replayable command line carrying no nonce, so a stale press
// repeats whatever it asked for. Idempotent writes are fine — /start's language
// and the profile upsert are — but anything else keeps its own confirmation
// button, the way deleteyes: does.
//
// Built in init rather than as a var initializer: the handlers here call back into
// lookupCommand, which the compiler would otherwise reject as an initialization
// cycle through this very slice.
var commandSpecs []commandSpec

func init() {
	commandSpecs = []commandSpec{
		{
			name:        "start",
			description: "Choose language / انتخاب زبان",
			args: []argSpec{{
				name: "language", kind: argChoice, mandatory: true,
				ask:     "Choose your language / زبان خود را انتخاب کنید:",
				options: []argOption{{"en", "English"}, {"fa", "فارسی"}},
			}},
			run: func(a *app, ctx context.Context, userID int64, p cmdArgs) error {
				language := p.get("language")
				if err := a.setLanguage(ctx, userID, language); err != nil {
					return err
				}
				ctx = withLanguage(ctx, language)
				if err := a.send(ctx, userID, welcome, actionButtons()); err != nil {
					return err
				}
				return a.onboardIfNew(ctx, userID)
			},
		},
		{
			name:        "new",
			description: "Record a pair / ضبط جفت صدا",
			precheck:    requireProfile,
			run: func(a *app, ctx context.Context, userID int64, _ cmdArgs) error {
				return a.startPair(ctx, userID, false)
			},
		},
		{
			name:        "my",
			description: "View my pairs / دیدن جفت‌ها",
			run: func(a *app, ctx context.Context, userID int64, _ cmdArgs) error {
				return a.listPairs(ctx, userID, 0)
			},
		},
		{
			name:        "onboard",
			description: "Set up my details / ثبت اطلاعات من",
			args:        profileFields(),
			precheck:    onboardPrecheck,
			run:         saveProfileWith("onboard"),
		},
		{
			name:        "profile",
			description: "My profile / پروفایل من",
			// The edit slot is never asked for: only the profile card's buttons set it.
			args:     append([]argSpec{{name: "edit", kind: argChoice, options: profileEditOptions}}, profileFields()...),
			precheck: profilePrecheck,
			run:      saveProfileWith("profile"),
		},
		{
			name:        "leader",
			description: "Leaderboard / جدول برترین‌ها",
			args: []argSpec{
				{
					name: "period", kind: argChoice, mandatory: true, ask: askPeriod,
					options: []argOption{{"d", "Today"}, {"w", "This week"}},
				},
				{name: "detail", kind: argFlag, token: "l"},
			},
			run: func(a *app, ctx context.Context, userID int64, p cmdArgs) error {
				return a.leaderboard(ctx, userID, p.get("period"), p.has("detail"))
			},
		},
		{
			name:        "help",
			description: "How it works / راهنما",
			run: func(a *app, ctx context.Context, userID int64, _ cmdArgs) error {
				return a.send(ctx, userID, helpText(ctx), actionButtons())
			},
		},
		{
			name:        "cancel",
			description: "Cancel current pair / لغو جفت فعلی",
			run: func(a *app, ctx context.Context, userID int64, _ cmdArgs) error {
				return a.cancel(ctx, userID)
			},
		},
		{
			name:        "download",
			description: "Download my voices (WAV zip) / دریافت صداهای من",
			args: []argSpec{{
				name: "range", kind: argChoice, mandatory: true, perRow: 1,
				ask:     "What would you like to export?",
				options: []argOption{{"all", "All my records"}, {"week", "This week"}, {"today", "Today"}},
			}},
			run: func(a *app, ctx context.Context, userID int64, p cmdArgs) error {
				a.startExport(ctx, userID, p.get("range"))
				return nil
			},
		},
		{
			name:        "stats",
			description: "App and user statistics",
			adminOnly:   true,
			run: func(a *app, ctx context.Context, userID int64, _ cmdArgs) error {
				return a.adminStats(ctx, userID)
			},
		},
		{
			name:        "users",
			description: "Browse users and their records",
			adminOnly:   true,
			run: func(a *app, ctx context.Context, userID int64, _ cmdArgs) error {
				return a.adminUsers(ctx, userID, 0)
			},
		},
		{
			name:        "broadcast",
			description: "Message all users: /broadcast text",
			adminOnly:   true,
			args: []argSpec{{
				name: "text", kind: argText, mandatory: true, maxRunes: 4096,
				ask: "Send the text to broadcast in your next message. Use /cancel to cancel.",
			}},
			run: func(a *app, ctx context.Context, userID int64, p cmdArgs) error {
				return a.broadcast(ctx, userID, p.get("text"))
			},
		},
		{
			name:        "allow",
			description: "Allow a user: /allow ID",
			adminOnly:   true,
			args:        []argSpec{idArg},
			run: func(a *app, ctx context.Context, userID int64, p cmdArgs) error {
				return a.setAllowed(ctx, userID, p.get("id"), true)
			},
		},
		{
			name:        "unallow",
			description: "Remove a user: /unallow ID",
			adminOnly:   true,
			args:        []argSpec{idArg},
			run: func(a *app, ctx context.Context, userID int64, p cmdArgs) error {
				return a.setAllowed(ctx, userID, p.get("id"), false)
			},
		},
	}
}
