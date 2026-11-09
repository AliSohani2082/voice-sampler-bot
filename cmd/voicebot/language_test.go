package main

import (
	"context"
	"testing"
)

func TestTranslate(t *testing.T) {
	const prompt = "Use /new, /my, or /cancel."
	if got := translate(context.Background(), prompt); got != prompt {
		t.Fatalf("default language = %q", got)
	}
	if got := translate(withLanguage(context.Background(), "fa"), prompt); got != "از /new، /my یا /cancel استفاده کنید." {
		t.Fatalf("Persian prompt = %q", got)
	}
	if got := translate(withLanguage(context.Background(), "fa"), "unknown"); got != "unknown" {
		t.Fatalf("unknown prompt = %q", got)
	}
}
