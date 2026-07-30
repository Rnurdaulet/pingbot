package telegram

import (
	"strings"
	"testing"
)

func TestCommand(t *testing.T) {
	t.Parallel()
	if got := Command("/status@MyBot"); got != "status" {
		t.Fatalf("got %q", got)
	}
	if got := Command("  /STATS  "); got != "stats" {
		t.Fatalf("got %q", got)
	}
	args := Args("/check site-a")
	if len(args) != 1 || args[0] != "site-a" {
		t.Fatalf("args=%v", args)
	}
}

func TestAllowedChat(t *testing.T) {
	t.Parallel()
	c := New("token", "-100123")
	if !c.AllowedChat(-100123) {
		t.Fatal("expected allowed")
	}
	if c.AllowedChat(1) {
		t.Fatal("expected denied")
	}
}

func TestCheckCallbackData(t *testing.T) {
	t.Parallel()
	data, ok := CheckCallbackData("site-a")
	if !ok || data != "c:site-a" {
		t.Fatalf("got %q %v", data, ok)
	}
	target, ok := ParseCheckCallback(data)
	if !ok || target != "site-a" {
		t.Fatalf("parse got %q %v", target, ok)
	}
	long := strings.Repeat("x", 70)
	if _, ok := CheckCallbackData(long); ok {
		t.Fatal("expected reject oversized callback")
	}
}
