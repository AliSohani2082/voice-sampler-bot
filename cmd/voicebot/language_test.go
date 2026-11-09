package main

import (
	"context"
	"testing"
)

func TestTranslate(t *testing.T) {
	const prompt = "Ready for another pair?"
	if got := translate(context.Background(), prompt); got != prompt {
		t.Fatalf("default language = %q", got)
	}
	if got := translate(withLanguage(context.Background(), "fa"), prompt); got != "برای ضبط جفت بعدی آماده‌اید؟" {
		t.Fatalf("Persian prompt = %q", got)
	}
	if got := translate(withLanguage(context.Background(), "fa"), "unknown"); got != "unknown" {
		t.Fatalf("unknown prompt = %q", got)
	}
	if got := translate(withLanguage(context.Background(), "fa"), welcome); got == welcome {
		t.Fatal("welcome is not translated")
	}
}
