package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestRegisterMenu(t *testing.T) {
	var methods []string
	a := &app{apiURL: "https://telegram.test/", http: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		methods = append(methods, r.URL.Path)
		var body map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if r.URL.Path == "/setMyCommands" {
			var commands []map[string]string
			if err := json.Unmarshal(body["commands"], &commands); err != nil {
				t.Errorf("decode commands: %v", err)
			}
			// The registry owns the list; this checks it reaches the wire intact.
			// TestMenuCommands is where the expected names themselves live.
			want := menuCommands(false)
			if len(commands) != len(want) {
				t.Errorf("sent %d commands, registry has %d: %v", len(commands), len(want), commands)
			}
			for i := range want {
				if i < len(commands) && commands[i]["command"] != want[i]["command"] {
					t.Errorf("command %d = %q, want %q", i, commands[i]["command"], want[i]["command"])
				}
			}
		}
		if r.URL.Path == "/setChatMenuButton" {
			var menu map[string]string
			if err := json.Unmarshal(body["menu_button"], &menu); err != nil {
				t.Errorf("decode menu: %v", err)
			}
			if menu["type"] != "commands" {
				t.Errorf("unexpected menu: %v", menu)
			}
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"ok":true,"result":true}`)), Header: make(http.Header)}, nil
	})}}
	if err := a.registerMenu(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(methods) != 2 || methods[0] != "/setMyCommands" || methods[1] != "/setChatMenuButton" {
		t.Fatalf("unexpected calls: %v", methods)
	}
}
