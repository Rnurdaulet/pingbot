package config

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
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

func TestFollowRedirectsDefaults(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		status []int
		set    bool
		value  bool
		want   bool
	}{
		{name: "unset with 3xx", status: []int{301}, want: false},
		{name: "unset with 200", status: []int{200}, want: true},
		{name: "unset with 200 and 301", status: []int{200, 301}, want: false},
		{name: "unset empty expect_status", want: true},
		{name: "explicit false wins", status: []int{200}, set: true, value: false, want: false},
		{name: "explicit true wins", status: []int{301}, set: true, value: true, want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			target := Target{Name: "t", URL: "https://example.com", ExpectStatus: tt.status}
			if tt.set {
				v := tt.value
				target.FollowRedirects = &v
			}
			cfg := Config{Targets: []Target{target}}
			cfg.applyDefaults()

			got := cfg.Targets[0]
			if got.FollowRedirects == nil {
				t.Fatal("follow_redirects not resolved")
			}
			if *got.FollowRedirects != tt.want || got.FollowRedirectsEnabled() != tt.want {
				t.Fatalf("got %v want %v", *got.FollowRedirects, tt.want)
			}
		})
	}

	// Two targets in one pass: pointers must not alias the loop variable.
	cfg := Config{Targets: []Target{
		{ExpectStatus: []int{301}},
		{ExpectStatus: []int{200}},
	}}
	cfg.applyDefaults()
	if cfg.Targets[0].FollowRedirectsEnabled() || !cfg.Targets[1].FollowRedirectsEnabled() {
		t.Fatalf("got %v %v", cfg.Targets[0].FollowRedirectsEnabled(), cfg.Targets[1].FollowRedirectsEnabled())
	}
}

func TestFollowRedirectsYAML(t *testing.T) {
	t.Parallel()

	raw := []byte(`
targets:
  - name: redir
    url: https://example.com/old
    expect_status: [301]
  - name: ok
    url: https://example.com/health
  - name: forced
    url: https://example.com/old
    expect_status: [301]
    follow_redirects: true
`)
	var cfg Config
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	cfg.applyDefaults()

	if cfg.Targets[0].FollowRedirectsEnabled() {
		t.Fatal("unset 3xx should not follow")
	}
	if !cfg.Targets[1].FollowRedirectsEnabled() {
		t.Fatal("unset 200 should follow")
	}
	if !cfg.Targets[2].FollowRedirectsEnabled() {
		t.Fatal("explicit true should follow")
	}
}
