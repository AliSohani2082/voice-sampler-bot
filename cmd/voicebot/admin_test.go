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
	if err != nil || cfg.adminID != 34 || !cfg.allowed[34] {
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

func TestAdminMenuScope(t *testing.T) {
	var adminMenuFound bool
	a := &app{cfg: config{adminID: 34}, apiURL: "https://telegram.test/", http: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
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
			if body.Scope.ChatID == 34 {
				adminMenuFound = len(body.Commands) == 11 && body.Commands[6].Command == "stats" && body.Commands[8].Command == "broadcast" && body.Commands[9].Command == "allow" && body.Commands[10].Command == "unallow"
			} else if len(body.Commands) != 6 {
				t.Errorf("admin commands leaked into default menu: %+v", body.Commands)
			}
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"ok":true,"result":true}`)), Header: make(http.Header)}, nil
	})}}
	if err := a.registerMenu(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !adminMenuFound {
		t.Fatal("admin menu was not registered for the admin chat")
	}
}

func TestAdminCommandRejectedForOtherUser(t *testing.T) {
	var sentTo int64
	a := &app{cfg: config{adminID: 34}, apiURL: "https://telegram.test/", http: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		var body struct {
			ChatID int64 `json:"chat_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		sentTo = body.ChatID
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"ok":true,"result":true}`)), Header: make(http.Header)}, nil
	})}}
	for _, command := range []string{"/stats", "/allow 55", "/unallow 55"} {
		if err := a.handleMessage(context.Background(), &message{From: tgUser{ID: 12}, Text: command}); err != nil {
			t.Fatal(err)
		}
	}
	if sentTo != 12 {
		t.Fatalf("unexpected recipient %d", sentTo)
	}
}
