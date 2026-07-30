package checker

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/Rnurdaulet/pingbot/internal/config"
)

type Status int

const (
	OK Status = iota
	Slow
	Fail
)

type Result struct {
	Status     Status
	Latency    time.Duration
	StatusCode int
	Err        string
}

type Checker struct {
	client *http.Client
}

func New() *Checker {
	return &Checker{
		client: &http.Client{Timeout: 30 * time.Second},
	}
}

func (c *Checker) Check(ctx context.Context, t config.Target) Result {
	ctx, cancel := context.WithTimeout(ctx, t.Timeout.Std())
	defer cancel()

	var body io.Reader
	if t.Body != "" {
		body = strings.NewReader(t.Body)
	}

	req, err := http.NewRequestWithContext(ctx, t.Method, t.URL, body)
	if err != nil {
		return Result{Status: Fail, Err: fmt.Sprintf("build request: %v", err)}
	}
	for k, v := range t.Headers {
		req.Header.Set(k, v)
	}

	start := time.Now()
	resp, err := c.client.Do(req)
	latency := time.Since(start)
	if err != nil {
		return Result{Status: Fail, Latency: latency, Err: err.Error()}
	}
	defer resp.Body.Close()
	// Drain so the connection can be reused.
	io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))

	if !slices.Contains(t.ExpectStatus, resp.StatusCode) {
		return Result{
			Status:     Fail,
			Latency:    latency,
			StatusCode: resp.StatusCode,
			Err:        fmt.Sprintf("unexpected status %d", resp.StatusCode),
		}
	}

	if t.MaxLatencyMS > 0 && latency > time.Duration(t.MaxLatencyMS)*time.Millisecond {
		return Result{Status: Slow, Latency: latency, StatusCode: resp.StatusCode}
	}

	return Result{Status: OK, Latency: latency, StatusCode: resp.StatusCode}
}
