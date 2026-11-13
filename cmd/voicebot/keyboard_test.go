package main

import "testing"

func TestKeyboardCommand(t *testing.T) {
	for text, want := range map[string]string{"🎙 New pair": "new", "📊 وضعیت": "status", "🏆 Leader": "leader", "📥 دریافت": "download", "👤 Profile": "profile", "📂 جفت‌های من": "my"} {
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
