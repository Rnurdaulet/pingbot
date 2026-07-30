package config

import "testing"

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
