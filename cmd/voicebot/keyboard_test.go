package main

import "testing"

func TestKeyboardCommand(t *testing.T) {
	for text, want := range map[string]string{"Record a pair": "new", "وضعیت من": "status", "Leaderboard": "leader", "دریافت صداهای من": "download", "My profile": "profile", "جفت‌های من": "my"} {
		if got, ok := keyboardCommand(text); !ok || got != want {
			t.Errorf("keyboardCommand(%q) = %q, %v; want %q", text, got, ok, want)
		}
	}
	if _, ok := keyboardCommand("I am sleepy today"); ok {
		t.Error("ordinary text matched a keyboard button")
	}
	for _, row := range keyboardButtons {
		for _, b := range row {
			if lookupCommand(b.command) == nil {
				t.Errorf("button %q points at unknown command %q", b.label, b.command)
			}
		}
	}
}
