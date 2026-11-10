package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestAdminConfig(t *testing.T) {
	t.Setenv("TELEGRAM_BOT_TOKEN", "test")
	t.Setenv("S3_BUCKET", "test")
	t.Setenv("ALLOWED_USER_IDS", "12")
	t.Setenv("ADMIN_TELEGRAM_ID", "34")
	cfg, err := loadConfig()
	if err != nil || len(cfg.fullAdmins) != 1 || !cfg.fullAdmins[34] || !cfg.allowed[34] || !cfg.admins[34] {
		t.Fatalf("admin config = %+v, %v", cfg, err)
	}
	t.Setenv("ALLOWED_USER_IDS", "")
	if cfg, err := loadConfig(); err != nil || !cfg.allowed[34] {
		t.Fatalf("admin-only config = %+v, %v", cfg, err)
	}
	t.Setenv("ADMIN_TELEGRAM_ID", "invalid")
	if _, err := loadConfig(); err == nil {
		t.Fatal("invalid admin ID accepted")
	}
}

func TestAdminConfigMultiple(t *testing.T) {
	t.Setenv("TELEGRAM_BOT_TOKEN", "test")
	t.Setenv("S3_BUCKET", "test")
	t.Setenv("ALLOWED_USER_IDS", "7") // an empty ADMIN_TELEGRAM_ID is valid only with another allowed user
	for _, tc := range []struct {
		raw  string
		want []int64 // nil: must fail
	}{
		{"111", []int64{111}},
		{"111,222", []int64{111, 222}},
		{" 111 , 222 ", []int64{111, 222}},
		{"", []int64{}},
		{"0", nil}, {"-1", nil}, {"abc", nil}, {"1,,x", nil},
	} {
		t.Setenv("ADMIN_TELEGRAM_ID", tc.raw)
		cfg, err := loadConfig()
		if tc.want == nil {
			if err == nil {
				t.Errorf("ADMIN_TELEGRAM_ID=%q accepted", tc.raw)
			}
			continue
		}
		if err != nil || len(cfg.fullAdmins) != len(tc.want) {
			t.Errorf("ADMIN_TELEGRAM_ID=%q: %v, %v", tc.raw, cfg.fullAdmins, err)
			continue
		}
		for _, id := range tc.want {
			if !cfg.fullAdmins[id] || !cfg.allowed[id] || !cfg.admins[id] {
				t.Errorf("ADMIN_TELEGRAM_ID=%q: %d not a full admin/allowed/request admin", tc.raw, id)
			}
		}
	}
	// Full admins are request admins even when ADMIN_USER_IDS is set; half admins stay half.
	t.Setenv("ADMIN_TELEGRAM_ID", "111,222")
	t.Setenv("ADMIN_USER_IDS", "333")
	cfg, err := loadConfig()
	if err != nil || !cfg.admins[111] || !cfg.admins[222] || !cfg.admins[333] || cfg.fullAdmins[333] {
		t.Fatalf("admins = %v, fullAdmins = %v, %v", cfg.admins, cfg.fullAdmins, err)
	}
}

func TestAdminIDsExcludesAllFullAdmins(t *testing.T) {
	a := &app{cfg: config{fullAdmins: map[int64]bool{111: true, 222: true}, admins: map[int64]bool{333: true}}}
	ids := a.adminIDs()
	if len(ids) != 2 || !(ids[0] == 111 && ids[1] == 222 || ids[0] == 222 && ids[1] == 111) {
		t.Fatalf("adminIDs = %v", ids)
	}
	if ids := (&app{}).adminIDs(); ids == nil || len(ids) != 0 {
		t.Fatalf("no admins must give an empty non-nil slice, got %#v", ids)
	}
}

func TestAdminMenuScope(t *testing.T) {
	adminMenus := map[int64]bool{}
	a := &app{cfg: config{fullAdmins: map[int64]bool{34: true, 35: true}, admins: map[int64]bool{36: true}}, apiURL: "https://telegram.test/", http: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/setMyCommands" {
			var body struct {
				Scope struct {
					ChatID int64 `json:"chat_id"`
				} `json:"scope"`
				Commands []struct {
					Command string `json:"command"`
				} `json:"commands"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body.Scope.ChatID != 0 {
				adminMenus[body.Scope.ChatID] = len(body.Commands) == 11 && body.Commands[6].Command == "stats" && body.Commands[8].Command == "broadcast" && body.Commands[9].Command == "allow" && body.Commands[10].Command == "unallow"
			} else if len(body.Commands) != 6 {
				t.Errorf("admin commands leaked into default menu: %+v", body.Commands)
			}
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"ok":true,"result":true}`)), Header: make(http.Header)}, nil
	})}}
	if err := a.registerMenu(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(adminMenus) != 2 || !adminMenus[34] || !adminMenus[35] {
		t.Fatalf("admin menu must be registered for exactly the full admins, got %v", adminMenus)
	}
}

type sentMessage struct {
	ChatID int64  `json:"chat_id"`
	Text   string `json:"text"`
}

// newAdminTestApp records every Telegram call. Full admins 34 and 35, half admin 36.
func newAdminTestApp(t *testing.T, sent *[]sentMessage) *app {
	return &app{cfg: config{fullAdmins: map[int64]bool{34: true, 35: true}, admins: map[int64]bool{34: true, 35: true, 36: true}}, apiURL: "https://telegram.test/", http: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		var m sentMessage
		if err := json.NewDecoder(r.Body).Decode(&m); err != nil {
			t.Fatal(err)
		}
		*sent = append(*sent, m)
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"ok":true,"result":true}`)), Header: make(http.Header)}, nil
	})}}
}

func TestAdminCommandAuthorizationAndReplyTarget(t *testing.T) {
	var sent []sentMessage
	a := newAdminTestApp(t, &sent)
	// Non-admin 12 and half admin 36 are refused; each full admin gets the admin handler's reply, to themselves.
	for _, tc := range []struct {
		user  int64
		admin bool
	}{{12, false}, {36, false}, {34, true}, {35, true}} {
		sent = nil
		if err := a.handleMessage(context.Background(), &message{From: tgUser{ID: tc.user}, Text: "/allow x"}); err != nil {
			t.Fatal(err)
		}
		if len(sent) != 1 || sent[0].ChatID != tc.user || strings.Contains(sent[0].Text, "Use /allow ID") != tc.admin {
			t.Errorf("user %d (admin=%v): sent %+v", tc.user, tc.admin, sent)
		}
	}
	// Admin callbacks answer the pressing admin; "admin:user:34:0" is rejected for targeting an admin.
	for _, admin := range []int64{34, 35} {
		sent = nil
		if err := a.handleAdminCallback(context.Background(), admin, "admin:user:34:0"); err != nil {
			t.Fatal(err)
		}
		if len(sent) != 1 || sent[0].ChatID != admin || sent[0].Text != "Invalid user." {
			t.Errorf("admin %d callback: sent %+v", admin, sent)
		}
	}
}

func TestBroadcastPendingIsPerAdmin(t *testing.T) {
	var sent []sentMessage
	a := newAdminTestApp(t, &sent)
	for _, admin := range []int64{34, 35} {
		if err := a.handleMessage(context.Background(), &message{From: tgUser{ID: admin}, Text: "/broadcast"}); err != nil {
			t.Fatal(err)
		}
	}
	if !a.broadcastPending[34] || !a.broadcastPending[35] {
		t.Fatalf("pending = %v", a.broadcastPending)
	}
	if err := a.handleMessage(context.Background(), &message{From: tgUser{ID: 34}, Text: "/cancel"}); err != nil {
		t.Fatal(err)
	}
	if a.broadcastPending[34] || !a.broadcastPending[35] {
		t.Fatalf("canceling one admin's broadcast changed the other's: %v", a.broadcastPending)
	}
}
