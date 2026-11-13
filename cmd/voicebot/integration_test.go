package main

// Integration tests for the invariants that only a real database can show:
// which commands leave a collection row behind, whether a reported rank matches
// the board's own order, and that the schema upgrades a database created before
// profiles existed.
//
// They are skipped unless VOICEBOT_TEST_DSN points at a scratch PostgreSQL, so
// the default suite stays dependency-free:
//
//	VOICEBOT_TEST_DSN='postgres:///voicebot?host=/tmp/pg&port=5433&user=postgres' go test ./cmd/voicebot
//
// The database is written to. Never point this at real data.

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// testBot connects to the scratch database, applies the schema the way main.go
// does, and returns an app whose Telegram calls are captured instead of sent.
func testBot(t *testing.T) (*app, *[]string, context.Context) {
	t.Helper()
	dsn := os.Getenv("VOICEBOT_TEST_DSN")
	if dsn == "" {
		t.Skip("set VOICEBOT_TEST_DSN to a scratch PostgreSQL to run the integration tests")
	}
	ctx := context.Background()
	db, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	for _, statement := range strings.Split(schema, ";") {
		if strings.TrimSpace(statement) == "" {
			continue
		}
		if _, err := db.Exec(ctx, statement); err != nil {
			t.Fatalf("schema: %v\nstatement: %s", err, strings.TrimSpace(statement))
		}
	}
	sent := &[]string{}
	a := &app{
		db:     db,
		cfg:    config{fullAdmins: map[int64]bool{}, allowed: map[int64]bool{}},
		apiURL: "https://telegram.test/",
		http: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			var body struct {
				Text string `json:"text"`
			}
			raw, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(raw, &body)
			*sent = append(*sent, body.Text)
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"ok":true,"result":true}`)), Header: make(http.Header)}, nil
		})},
	}
	return a, sent, ctx
}

// reset clears everything the given users own, so each test starts clean.
func reset(t *testing.T, a *app, ctx context.Context, users ...int64) {
	t.Helper()
	for _, id := range users {
		for _, q := range []string{
			`DELETE FROM samples WHERE pair_id IN (SELECT id FROM pairs WHERE user_id=$1)`,
			`DELETE FROM pairs WHERE user_id=$1`,
			`DELETE FROM sessions WHERE user_id=$1`,
			`DELETE FROM profiles WHERE user_id=$1`,
		} {
			if _, err := a.db.Exec(ctx, q, id); err != nil {
				t.Fatal(err)
			}
		}
		if err := a.upsertUser(ctx, tgUser{ID: id}); err != nil {
			t.Fatal(err)
		}
	}
}

// lastSent is the most recent message the bot tried to send.
func lastSent(t *testing.T, sent *[]string) string {
	t.Helper()
	if len(*sent) == 0 {
		t.Fatal("nothing was sent")
	}
	return (*sent)[len(*sent)-1]
}

// A bare /broadcast asks for the text and stores a collection. Answering on the
// command line instead must take that row with it: otherwise the admin's next
// ordinary message is read as the broadcast body and sent to everyone.
func TestCommandLineAnswerClearsThePrompt(t *testing.T) {
	a, sent, ctx := testBot(t)
	const admin = int64(900001)
	a.cfg.fullAdmins[admin] = true
	reset(t, a, ctx, admin)

	msg := func(text string) {
		t.Helper()
		*sent = nil
		if err := a.handleMessage(ctx, &message{From: tgUser{ID: admin}, Chat: chat{ID: admin, Type: "private"}, Text: text}); err != nil {
			t.Fatalf("%q: %v", text, err)
		}
	}
	msg("/broadcast")
	if s, err := a.getSession(ctx, admin); err != nil || s.Stage != awaitStage("broadcast") {
		t.Fatalf("the prompt stored %q, %v", s.Stage, err)
	}
	msg("/broadcast announced on the command line")
	if !strings.Contains(lastSent(t, sent), "Broadcast finished") {
		t.Fatalf("the broadcast did not run: %q", lastSent(t, sent))
	}
	if s, err := a.getSession(ctx, admin); err != nil || s.Stage != "" {
		t.Errorf("the prompt outlived its answer: stage %q, pending %s", s.Stage, s.Pending)
	}
	msg("just thinking out loud")
	if got := lastSent(t, sent); strings.Contains(got, "Broadcast finished") {
		t.Errorf("an ordinary message was broadcast: %q", got)
	}

	// Same shape for /allow, where a stale prompt would read the next message as
	// a user id.
	msg("/allow")
	msg("/allow 123456")
	if s, err := a.getSession(ctx, admin); err != nil || s.Stage != "" {
		t.Errorf("/allow left a prompt behind: stage %q", s.Stage)
	}
	if _, err := a.db.Exec(ctx, `DELETE FROM allowlist WHERE user_id=123456`); err != nil {
		t.Fatal(err)
	}
	delete(a.cfg.allowed, 123456)
}

// Showing the profile card ends an edit that was waiting on an answer. Without
// that, the next ordinary message silently rewrites the field being edited.
func TestProfileCardEndsAPendingEdit(t *testing.T) {
	a, sent, ctx := testBot(t)
	const u = int64(900002)
	reset(t, a, ctx, u)
	stored := profile{Gender: "male", Age: 22, FirstName: "Ali", LastName: "Sohani", StudentNumber: "90000201"}
	if err := a.putProfile(ctx, u, stored); err != nil {
		t.Fatal(err)
	}

	*sent = nil
	if err := a.handleCallback(ctx, u, "cmd:profile:ag"); err != nil {
		t.Fatal(err)
	}
	if lastSent(t, sent) != askAge {
		t.Fatalf("the edit asked %q", lastSent(t, sent))
	}
	*sent = nil
	if err := a.handleMessage(ctx, &message{From: tgUser{ID: u}, Chat: chat{ID: u, Type: "private"}, Text: "/profile"}); err != nil {
		t.Fatal(err)
	}
	if s, err := a.getSession(ctx, u); err != nil || s.Stage != "" {
		t.Errorf("the card left the edit pending: stage %q, pending %s", s.Stage, s.Pending)
	}
	*sent = nil
	if err := a.handleMessage(ctx, &message{From: tgUser{ID: u}, Chat: chat{ID: u, Type: "private"}, Text: "41"}); err != nil {
		t.Fatal(err)
	}
	after, _, err := a.getProfile(ctx, u)
	if err != nil {
		t.Fatal(err)
	}
	if after != stored {
		t.Errorf("an ordinary message rewrote the profile: %+v, want %+v", after, stored)
	}
}

// The rank a participant is told must be the position the board shows them in.
// Two contributors tied on pairs are separated by recorded seconds, so the one
// listed second must not be told they are first.
func TestLeaderRankMatchesBoardOrder(t *testing.T) {
	a, _, ctx := testBot(t)
	const louder, quieter = int64(900003), int64(900004)
	reset(t, a, ctx, louder, quieter)

	now := time.Now().In(exportLoc)
	at := time.Date(now.Year(), now.Month(), now.Day(), 12, 0, 0, 0, exportLoc)
	// One pair each, so they tie on pairs; different durations break the tie.
	for _, c := range []struct {
		pairID  string
		user    int64
		seconds int
	}{{"rank-louder", louder, 10}, {"rank-quieter", quieter, 1}} {
		if _, err := a.db.Exec(ctx, `INSERT INTO pairs(id,user_id,status,created_at,completed_at) VALUES($1,$2,'complete',$3,$3)`, c.pairID, c.user, at); err != nil {
			t.Fatal(err)
		}
		for _, kind := range []string{"sleepy", "awake"} {
			if _, err := a.db.Exec(ctx, `INSERT INTO samples(pair_id,kind,object_key,telegram_file_id,telegram_unique_id,duration_seconds,file_size_bytes,mime_type) VALUES($1,$2,$3,'f','u',$4,100,'audio/ogg')`,
				c.pairID, kind, c.pairID+kind, c.seconds); err != nil {
				t.Fatal(err)
			}
		}
	}
	// Name them so their rows are identifiable on the board.
	if err := a.putProfile(ctx, louder, profile{Gender: "male", Age: 22, FirstName: "L", LastName: "Ouder", StudentNumber: "90000301"}); err != nil {
		t.Fatal(err)
	}
	if err := a.putProfile(ctx, quieter, profile{Gender: "male", Age: 22, FirstName: "Q", LastName: "Uieter", StudentNumber: "90000401"}); err != nil {
		t.Fatal(err)
	}
	from, to, ok := leaderBounds("d", time.Now())
	if !ok {
		t.Fatal("no day bounds")
	}
	rows, err := a.leaderRows(ctx, from, to, 10)
	if err != nil {
		t.Fatal(err)
	}
	position := map[int64]int{}
	for i, r := range rows {
		if r.Name == "L Ouder" {
			position[louder] = i + 1
		}
		if r.Name == "Q Uieter" {
			position[quieter] = i + 1
		}
	}
	if position[louder] == 0 || position[quieter] == 0 {
		t.Fatalf("both contributors should be listed, got %+v", rows)
	}
	if position[louder] > position[quieter] {
		t.Fatalf("the longer recordings should rank first: %+v", position)
	}
	for user, want := range position {
		rank, pairs, found, err := a.leaderRank(ctx, user, from, to)
		if err != nil || !found {
			t.Fatalf("user %d has no rank: %v, %v", user, found, err)
		}
		if pairs != 1 {
			t.Errorf("user %d: pairs = %d, want 1", user, pairs)
		}
		if rank != want {
			t.Errorf("user %d is listed at %d but told they are #%d", user, want, rank)
		}
	}
}

// The schema has to upgrade a database created before profiles existed, and be
// safe to re-apply on every restart, which is how main.go runs it.
func TestSchemaUpgradesAnExistingDeployment(t *testing.T) {
	a, _, ctx := testBot(t)
	// Pre-change shapes: CREATE TABLE IF NOT EXISTS would not alter these.
	for _, statement := range []string{
		`CREATE TABLE IF NOT EXISTS users (user_id bigint PRIMARY KEY)`,
		`CREATE TABLE IF NOT EXISTS sessions (user_id bigint PRIMARY KEY, stage text NOT NULL, pair_id text NOT NULL, candidate jsonb, nonce text NOT NULL DEFAULT '')`,
	} {
		if _, err := a.db.Exec(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	for range 2 { // idempotent across restarts
		for _, statement := range strings.Split(schema, ";") {
			if strings.TrimSpace(statement) == "" {
				continue
			}
			if _, err := a.db.Exec(ctx, statement); err != nil {
				t.Fatalf("re-applying the schema: %v", err)
			}
		}
	}
	for _, c := range []struct{ table, column string }{
		{"users", "tg_first_name"}, {"users", "tg_username"}, {"sessions", "pending"},
	} {
		var ok bool
		if err := a.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM information_schema.columns WHERE table_name=$1 AND column_name=$2)`, c.table, c.column).Scan(&ok); err != nil {
			t.Fatal(err)
		}
		if !ok {
			t.Errorf("%s.%s was not added to the existing table", c.table, c.column)
		}
	}
	var indexed bool
	if err := a.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_indexes WHERE indexname='pairs_completed_idx')`).Scan(&indexed); err != nil {
		t.Fatal(err)
	}
	if !indexed {
		t.Error("pairs_completed_idx is missing, so the leaderboard aggregation has no index")
	}

	const u = int64(900005)
	reset(t, a, ctx, u)
	// A collection survives a restart, which is the point of storing it.
	if err := a.putSession(ctx, u, session{Stage: awaitStage("onboard"), Pending: encodePending(lookupCommand("onboard"), func() cmdArgs {
		p := newArgs()
		p.setValue("gender", "m")
		p.setValue("age", "22")
		return p
	}())}); err != nil {
		t.Fatal(err)
	}
	s, err := a.getSession(ctx, u)
	if err != nil {
		t.Fatal(err)
	}
	restored, ok := decodePending(lookupCommand("onboard"), s.Pending)
	if !ok || restored.get("gender") != "m" || restored.get("age") != "22" {
		t.Errorf("a stored collection came back as %+v, ok=%v", restored, ok)
	}
	if err := a.clearSession(ctx, u); err != nil {
		t.Fatal(err)
	}

	// The profile CHECKs, and one student number per account.
	const other = int64(900006)
	reset(t, a, ctx, other)
	if err := a.putProfile(ctx, u, profile{Gender: "male", Age: 22, FirstName: "A", LastName: "B", StudentNumber: "90000501"}); err != nil {
		t.Fatal(err)
	}
	err = a.putProfile(ctx, other, profile{Gender: "female", Age: 23, FirstName: "C", LastName: "D", StudentNumber: "90000501"})
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23505" {
		t.Errorf("a duplicate student number gave %v, want SQLSTATE 23505", err)
	}
	if err := a.putProfile(ctx, u, profile{Gender: "male", Age: 23, FirstName: "A", LastName: "B", StudentNumber: "90000501"}); err != nil {
		t.Errorf("a profile could not update itself: %v", err)
	}
	for _, bad := range []profile{
		{Gender: "other", Age: 22, FirstName: "A", LastName: "B", StudentNumber: "9"},
		{Gender: "male", Age: 200, FirstName: "A", LastName: "B", StudentNumber: "9"},
		{Gender: "male", Age: 22, FirstName: "", LastName: "B", StudentNumber: "9"},
	} {
		if err := a.putProfile(ctx, other, bad); err == nil {
			t.Errorf("the database accepted %+v", bad)
		}
	}
}
