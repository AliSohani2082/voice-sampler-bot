package main

import "testing"

func TestIsStatusLong(t *testing.T) {
	for text, want := range map[string]bool{"/status": false, "/status l": true, "/status L": true, "/status long": true, "/status x": false, "/status@bot l": true} {
		if got := isStatusLong(text); got != want {
			t.Errorf("isStatusLong(%q) = %v, want %v", text, got, want)
		}
	}
}
