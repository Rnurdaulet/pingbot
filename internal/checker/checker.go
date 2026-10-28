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
	client     *http.Client
	noRedirect *http.Client
	now        func() time.Time
}

func New() *Checker {
	client := &http.Client{Timeout: 30 * time.Second}
	return &Checker{
		client:     client,
		noRedirect: clientWithoutRedirects(client),
		now:        time.Now,
	}
}

// clientWithoutRedirects stops at the first response so a 3xx can match.
// The TLS cert is then that of the configured URL.
func clientWithoutRedirects(base *http.Client) *http.Client {
	c := *base
	c.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return &c
}

func (c *Checker) clientFor(t config.Target) *http.Client {
	if t.FollowRedirectsEnabled() {
		return c.client
	}
	if c.noRedirect == nil {
		return clientWithoutRedirects(c.client)
	}
	return c.noRedirect
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
	resp, err := c.clientFor(t).Do(req)
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

	if certErr := c.checkSSL(t, resp); certErr != nil {
		return Result{
			Status:     certErr.status,
			Latency:    latency,
			StatusCode: resp.StatusCode,
			Err:        certErr.err,
		}
	}

	if t.MaxLatencyMS > 0 && latency > time.Duration(t.MaxLatencyMS)*time.Millisecond {
		return Result{Status: Slow, Latency: latency, StatusCode: resp.StatusCode}
	}

	return Result{Status: OK, Latency: latency, StatusCode: resp.StatusCode}
}

type sslResult struct {
	status Status
	err    string
}

func (c *Checker) checkSSL(t config.Target, resp *http.Response) *sslResult {
	if t.SSLWarnDays <= 0 {
		return nil
	}
	if resp.TLS == nil || len(resp.TLS.PeerCertificates) == 0 {
		return &sslResult{status: Fail, err: "ssl cert missing"}
	}

	now := c.now()
	notAfter := resp.TLS.PeerCertificates[0].NotAfter
	if !notAfter.After(now) {
		return &sslResult{
			status: Fail,
			err:    fmt.Sprintf("ssl cert expired (%s)", notAfter.UTC().Format("2006-01-02")),
		}
	}

	warnUntil := now.Add(time.Duration(t.SSLWarnDays) * 24 * time.Hour)
	if !notAfter.After(warnUntil) {
		remaining := notAfter.Sub(now)
		days := int(remaining.Hours() / 24)
		if days < 1 {
			return &sslResult{
				status: Slow,
				err:    fmt.Sprintf("ssl cert expires in <1d (%s)", notAfter.UTC().Format("2006-01-02")),
			}
		}
		return &sslResult{
			status: Slow,
			err:    fmt.Sprintf("ssl cert expires in %dd (%s)", days, notAfter.UTC().Format("2006-01-02")),
		}
	}
	return nil
}
