package main

import (
	"testing"

	"github.com/Rnurdaulet/pingbot/internal/telegram"
)

func TestTelegramCommand(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"/status":          "status",
		"/status@PingBot":  "status",
		"/stats":           "stats",
		"/history":         "history",
		"/help":            "help",
		"hello":            "",
		"":                 "",
	}
	for in, want := range cases {
		if got := telegram.Command(in); got != want {
			t.Fatalf("Command(%q)=%q want %q", in, got, want)
		}
	}
}
