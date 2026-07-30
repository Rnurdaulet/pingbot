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

type Client struct {
	token  string
	chatID string
	http   *http.Client
	long   *http.Client // for long-polling getUpdates
}

func New(token, chatID string) *Client {
	return &Client{
		token:  token,
		chatID: chatID,
		http:   &http.Client{Timeout: 10 * time.Second},
		long:   &http.Client{Timeout: 45 * time.Second},
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

func (c *Client) postJSON(method string, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	apiURL := fmt.Sprintf("https://api.telegram.org/bot%s/%s", c.token, method)
	resp, err := c.http.Post(apiURL, "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return fmt.Errorf("telegram %s status %d: %s", method, resp.StatusCode, b)
	}
	return nil
}

type Update struct {
	UpdateID      int             `json:"update_id"`
	Message       *MessageUpdate  `json:"message"`
	CallbackQuery *CallbackQuery  `json:"callback_query"`
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

	apiURL := fmt.Sprintf("https://api.telegram.org/bot%s/getUpdates?%s", c.token, q.Encode())
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
