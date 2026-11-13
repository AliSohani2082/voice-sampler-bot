package main

import "context"

// keyboardButtons is the persistent reply keyboard shown under the message field.
// Each label sends itself as a plain message, which keyboardCommand maps back to
// the command it stands for.
var keyboardButtons = [][]struct{ label, command string }{
	{{"Record a pair", "new"}, {"My status", "status"}},
	{{"My pairs", "my"}, {"Leaderboard", "leader"}},
	{{"Download my voices", "download"}, {"My profile", "profile"}},
}

// keyboardCommand returns the command for a reply-keyboard label, in either language.
// Labels are matched before any session stage, so a button press is never taken
// as a transcript or as an answer to a pending prompt.
func keyboardCommand(text string) (string, bool) {
	fa := withLanguage(context.Background(), "fa")
	for _, row := range keyboardButtons {
		for _, b := range row {
			if text == b.label || text == translate(fa, b.label) {
				return b.command, true
			}
		}
	}
	return "", false
}

// sendWithKeyboard sends text and (re)attaches the reply keyboard in the user's language.
func (a *app) sendWithKeyboard(ctx context.Context, chatID int64, text string) error {
	rows := make([][]map[string]string, 0, len(keyboardButtons))
	for _, row := range keyboardButtons {
		r := make([]map[string]string, 0, len(row))
		for _, b := range row {
			r = append(r, map[string]string{"text": translate(ctx, b.label)})
		}
		rows = append(rows, r)
	}
	return a.call(ctx, "sendMessage", map[string]any{
		"chat_id": chatID,
		"text":    translate(ctx, text),
		"reply_markup": map[string]any{
			"keyboard":        rows,
			"resize_keyboard": true,
			"is_persistent":   true,
		},
	}, nil)
}
