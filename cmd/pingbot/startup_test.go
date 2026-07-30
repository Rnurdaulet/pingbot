package main

import (
	"strings"
	"testing"

	"github.com/Rnurdaulet/pingbot/internal/config"
)

func TestFormatStartupMessage(t *testing.T) {
	t.Parallel()

	targets := []config.Target{
		{Name: "ummet.kz"},
		{Name: "sunna.kz"},
		{Name: "new-site"},
	}
	statuses := map[string]string{
		"ummet.kz": "up",
		"sunna.kz": "down",
	}

	msg := formatStartupMessage("srv-01", targets, statuses)
	wantParts := []string{
		"pingbot started · hostname=srv-01 · targets=3",
		"🟢 ummet.kz — up",
		"🔴 sunna.kz — down",
		"⚪ new-site — unknown",
	}
	for _, part := range wantParts {
		if !strings.Contains(msg, part) {
			t.Fatalf("message missing %q\ngot:\n%s", part, msg)
		}
	}
}
