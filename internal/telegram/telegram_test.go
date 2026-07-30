package telegram

import "testing"

func TestCommand(t *testing.T) {
	t.Parallel()
	if got := Command("/status@MyBot"); got != "status" {
		t.Fatalf("got %q", got)
	}
	if got := Command("  /STATS  "); got != "stats" {
		t.Fatalf("got %q", got)
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
