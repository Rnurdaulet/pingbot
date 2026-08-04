package config

import (
	"strings"
	"testing"
)

func TestStartupNotifyEnabled(t *testing.T) {
	t.Parallel()

	if !(Telegram{}).StartupNotifyEnabled() {
		t.Fatal("omitted startup_notify should default to true")
	}

	off := false
	if (Telegram{StartupNotify: &off}).StartupNotifyEnabled() {
		t.Fatal("startup_notify: false should disable")
	}

	on := true
	if !(Telegram{StartupNotify: &on}).StartupNotifyEnabled() {
		t.Fatal("startup_notify: true should enable")
	}
}

func TestValidateSSLWarnDays(t *testing.T) {
	t.Parallel()

	base := func(url string, days int) Config {
		return Config{
			Telegram: Telegram{BotToken: "t", ChatID: "1"},
			Storage:  Storage{Driver: "sqlite", Path: "data/pingbot.db"},
			Targets: []Target{{
				Name:        "t1",
				URL:         url,
				SSLWarnDays: days,
			}},
		}
	}

	cfg := base("https://example.com/health", 14)
	cfg.applyDefaults()
	if err := cfg.validate(); err != nil {
		t.Fatalf("https + ssl_warn_days: %v", err)
	}

	cfg = base("http://example.com/health", 14)
	cfg.applyDefaults()
	if err := cfg.validate(); err == nil || !strings.Contains(err.Error(), "https") {
		t.Fatalf("expected https error, got %v", err)
	}

	cfg = base("https://example.com/health", -1)
	cfg.applyDefaults()
	if err := cfg.validate(); err == nil || !strings.Contains(err.Error(), "ssl_warn_days") {
		t.Fatalf("expected ssl_warn_days error, got %v", err)
	}
}
