package telegram

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
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

func TestPostJSONRetry(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		statuses    []int
		bodies      []string
		dropConn    bool
		wantCalls   int
		wantSleep   []time.Duration
		wantErr     bool
		errContains string
	}{
		{
			name:      "5xx then 200",
			statuses:  []int{http.StatusInternalServerError, http.StatusOK},
			wantCalls: 2,
			wantSleep: []time.Duration{time.Second},
		},
		{
			name:      "429 retry_after",
			statuses:  []int{http.StatusTooManyRequests, http.StatusOK},
			bodies:    []string{`{"ok":false,"parameters":{"retry_after":5}}`},
			wantCalls: 2,
			wantSleep: []time.Duration{5 * time.Second},
		},
		{
			name:      "429 retry_after capped",
			statuses:  []int{http.StatusTooManyRequests, http.StatusOK},
			bodies:    []string{`{"parameters":{"retry_after":45}}`},
			wantCalls: 2,
			wantSleep: []time.Duration{30 * time.Second},
		},
		{
			name:        "400 no retry",
			statuses:    []int{http.StatusBadRequest},
			bodies:      []string{`{"description":"Bad Request"}`},
			wantCalls:   1,
			wantErr:     true,
			errContains: "status 400",
		},
		{
			name:        "three failures",
			statuses:    []int{http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable},
			wantCalls:   3,
			wantSleep:   []time.Duration{time.Second, 2 * time.Second},
			wantErr:     true,
			errContains: "status 503",
		},
		{
			name:        "network errors",
			dropConn:    true,
			wantCalls:   3,
			wantSleep:   []time.Duration{time.Second, 2 * time.Second},
			wantErr:     true,
			errContains: "sendMessage",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var mu sync.Mutex
			var calls int
			var path string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				i := calls
				calls++
				if path == "" {
					path = r.URL.Path
				}
				mu.Unlock()

				if tt.dropConn {
					conn, _, err := w.(http.Hijacker).Hijack()
					if err != nil {
						return
					}
					conn.Close()
					return
				}
				status := http.StatusInternalServerError
				if i < len(tt.statuses) {
					status = tt.statuses[i]
				}
				var body string
				if i < len(tt.bodies) {
					body = tt.bodies[i]
				}
				w.WriteHeader(status)
				_, _ = w.Write([]byte(body))
			}))
			defer srv.Close()

			var sleeps []time.Duration
			c := New("token", "1")
			c.baseURL = srv.URL
			c.http = srv.Client()
			c.sleep = func(d time.Duration) { sleeps = append(sleeps, d) }

			err := c.postJSON("sendMessage", map[string]string{"chat_id": "1", "text": "hi"})
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				if tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
					t.Fatalf("err %q", err)
				}
			} else if err != nil {
				t.Fatalf("postJSON: %v", err)
			}

			mu.Lock()
			gotCalls := calls
			gotPath := path
			mu.Unlock()
			if gotCalls != tt.wantCalls {
				t.Fatalf("calls=%d want %d", gotCalls, tt.wantCalls)
			}
			if gotPath != "/bottoken/sendMessage" {
				t.Fatalf("path %q", gotPath)
			}
			if len(sleeps) != len(tt.wantSleep) {
				t.Fatalf("sleeps=%v want %v", sleeps, tt.wantSleep)
			}
			for i := range tt.wantSleep {
				if sleeps[i] != tt.wantSleep[i] {
					t.Fatalf("sleeps=%v want %v", sleeps, tt.wantSleep)
				}
			}
		})
	}
}

func TestGetUpdatesBaseURL(t *testing.T) {
	t.Parallel()

	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"result":[]}`))
	}))
	defer srv.Close()

	c := New("token", "42")
	c.baseURL = srv.URL
	c.long = srv.Client()

	got, err := c.GetUpdates(context.Background(), 3, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("updates=%v", got)
	}
	if path != "/bottoken/getUpdates" {
		t.Fatalf("path %q", path)
	}
}
