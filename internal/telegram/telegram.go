package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Rnurdaulet/pingbot/internal/state"
)

const (
	defaultBaseURL  = "https://api.telegram.org"
	maxSendAttempts = 3
	maxRetryAfter   = 30 * time.Second
)

type Client struct {
	token   string
	chatID  string
	http    *http.Client
	long    *http.Client // for long-polling getUpdates
	baseURL string       // API root; default https://api.telegram.org
	sleep   func(time.Duration)
}

func New(token, chatID string) *Client {
	return &Client{
		token:   token,
		chatID:  chatID,
		http:    &http.Client{Timeout: 10 * time.Second},
		long:    &http.Client{Timeout: 45 * time.Second},
		baseURL: defaultBaseURL,
		sleep:   time.Sleep,
	}
}

func (c *Client) ChatID() string { return c.chatID }

// Send sends a plain-text message (no parse_mode).
func (c *Client) Send(text string) error {
	return c.SendMessage(state.Message{Text: text})
}

// SendMessage implements state.Notifier.
func (c *Client) SendMessage(msg state.Message) error {
	payload := map[string]any{
		"chat_id": c.chatID,
		"text":    msg.Text,
	}
	if msg.HTML {
		payload["parse_mode"] = "HTML"
	}
	if len(msg.Buttons) > 0 {
		payload["reply_markup"] = buildInlineKeyboard(msg.Buttons)
	}
	return c.postJSON("sendMessage", payload)
}

func buildInlineKeyboard(rows [][]state.Button) map[string]any {
	out := make([][]map[string]string, 0, len(rows))
	for _, row := range rows {
		r := make([]map[string]string, 0, len(row))
		for _, b := range row {
			r = append(r, map[string]string{
				"text":          b.Label,
				"callback_data": b.Data,
			})
		}
		out = append(out, r)
	}
	return map[string]any{"inline_keyboard": out}
}

type BotCommand struct {
	Command     string `json:"command"`
	Description string `json:"description"`
}

// SetMyCommands registers the bot command menu in Telegram clients.
func (c *Client) SetMyCommands(commands []BotCommand) error {
	return c.postJSON("setMyCommands", map[string]any{"commands": commands})
}

// DefaultCommands is the slash-command menu for pingbot.
func DefaultCommands() []BotCommand {
	return []BotCommand{
		{Command: "status", Description: "Current status"},
		{Command: "stats", Description: "Uptime 24h / 7d / 30d"},
		{Command: "incidents", Description: "Last downs"},
		{Command: "check", Description: "Live check: /check <target>"},
		{Command: "info", Description: "Config summary (redacted)"},
		{Command: "mute", Description: "Silence alerts: /mute 1h"},
		{Command: "unmute", Description: "Resume alerts"},
		{Command: "help", Description: "Command list"},
	}
}

func (c *Client) AnswerCallback(callbackID, text string) error {
	payload := map[string]any{"callback_query_id": callbackID}
	if text != "" {
		payload["text"] = text
		payload["show_alert"] = false
	}
	return c.postJSON("answerCallbackQuery", payload)
}

func (c *Client) apiBase() string {
	base := c.baseURL
	if base == "" {
		base = defaultBaseURL
	}
	return strings.TrimRight(base, "/")
}

func (c *Client) sleepFor(d time.Duration) {
	if c.sleep != nil {
		c.sleep(d)
		return
	}
	time.Sleep(d)
}

// postJSON posts payload. Network errors, HTTP 429, and HTTP 5xx are retried
// (3 attempts). Other 4xx responses are returned immediately.
func (c *Client) postJSON(method string, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	apiURL := fmt.Sprintf("%s/bot%s/%s", c.apiBase(), c.token, method)

	var lastErr error
	for attempt := 1; attempt <= maxSendAttempts; attempt++ {
		status, respBody, reqErr := c.doPost(apiURL, body)
		switch {
		case reqErr != nil:
			lastErr = fmt.Errorf("telegram %s: %w", method, reqErr)
		case status != http.StatusOK:
			lastErr = fmt.Errorf("telegram %s status %d: %s", method, status, respBody)
		default:
			return nil
		}
		if attempt == maxSendAttempts || !retryable(status, reqErr) {
			return lastErr
		}
		c.sleepFor(retryDelay(status, respBody, attempt))
	}
	return lastErr
}

func (c *Client) doPost(apiURL string, body []byte) (int, []byte, error) {
	resp, err := c.http.Post(apiURL, "application/json", bytes.NewReader(body))
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
	return resp.StatusCode, b, nil
}

func retryable(status int, reqErr error) bool {
	if reqErr != nil {
		return true
	}
	return status == http.StatusTooManyRequests || status >= 500
}

// retryDelay is retry_after (capped at 30s) for 429, otherwise 1s then 2s.
func retryDelay(status int, body []byte, attempt int) time.Duration {
	if status == http.StatusTooManyRequests {
		if d, ok := retryAfter(body); ok {
			return d
		}
	}
	return time.Duration(attempt) * time.Second
}

func retryAfter(body []byte) (time.Duration, bool) {
	var parsed struct {
		Parameters struct {
			RetryAfter int `json:"retry_after"`
		} `json:"parameters"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil || parsed.Parameters.RetryAfter <= 0 {
		return 0, false
	}
	d := time.Duration(parsed.Parameters.RetryAfter) * time.Second
	if d > maxRetryAfter {
		d = maxRetryAfter
	}
	return d, true
}

type Update struct {
	UpdateID      int            `json:"update_id"`
	Message       *MessageUpdate `json:"message"`
	CallbackQuery *CallbackQuery `json:"callback_query"`
}

type MessageUpdate struct {
	Text string `json:"text"`
	Chat struct {
		ID int64 `json:"id"`
	} `json:"chat"`
}

type CallbackQuery struct {
	ID   string `json:"id"`
	Data string `json:"data"`
	From struct {
		ID int64 `json:"id"`
	} `json:"from"`
	Message *struct {
		Chat struct {
			ID int64 `json:"id"`
		} `json:"chat"`
	} `json:"message"`
}

type getUpdatesResponse struct {
	OK     bool     `json:"ok"`
	Result []Update `json:"result"`
}

// GetUpdates long-polls Telegram. timeoutSec is the Telegram long-poll wait (max ~35).
func (c *Client) GetUpdates(ctx context.Context, offset, timeoutSec int) ([]Update, error) {
	q := url.Values{}
	q.Set("offset", strconv.Itoa(offset))
	q.Set("timeout", strconv.Itoa(timeoutSec))
	q.Set("allowed_updates", `["message","callback_query"]`)

	apiURL := fmt.Sprintf("%s/bot%s/getUpdates?%s", c.apiBase(), c.token, q.Encode())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.long.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("telegram getUpdates status %d: %s", resp.StatusCode, body)
	}

	var parsed getUpdatesResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, err
	}
	if !parsed.OK {
		return nil, fmt.Errorf("telegram getUpdates not ok: %s", body)
	}
	return parsed.Result, nil
}

// AllowedChat reports whether chatID matches the configured alerts chat.
func (c *Client) AllowedChat(chatID int64) bool {
	return strconv.FormatInt(chatID, 10) == c.chatID
}

// Command extracts the bot command from a message (e.g. "/status@MyBot" → "status").
func Command(text string) string {
	text = strings.TrimSpace(text)
	if text == "" || text[0] != '/' {
		return ""
	}
	cmd := strings.Fields(text)[0]
	cmd = strings.TrimPrefix(cmd, "/")
	if i := strings.IndexByte(cmd, '@'); i >= 0 {
		cmd = cmd[:i]
	}
	return strings.ToLower(cmd)
}

// Args returns space-separated arguments after the command.
func Args(text string) []string {
	fields := strings.Fields(strings.TrimSpace(text))
	if len(fields) <= 1 {
		return nil
	}
	return fields[1:]
}

// Callback data helpers (Telegram limit: 64 bytes).
const (
	CallbackCheckPrefix = "c:"
	CallbackMute1h      = "m:1h"
)

func CheckCallbackData(target string) (string, bool) {
	data := CallbackCheckPrefix + target
	if len(data) > 64 {
		return "", false
	}
	return data, true
}

func ParseCheckCallback(data string) (target string, ok bool) {
	if !strings.HasPrefix(data, CallbackCheckPrefix) {
		return "", false
	}
	target = strings.TrimPrefix(data, CallbackCheckPrefix)
	return target, target != ""
}
