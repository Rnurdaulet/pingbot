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

func (c *Client) Send(text string) error {
	return c.SendTo(c.chatID, text)
}

func (c *Client) SendTo(chatID, text string) error {
	payload, err := json.Marshal(map[string]string{
		"chat_id": chatID,
		"text":    text,
	})
	if err != nil {
		return err
	}

	apiURL := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", c.token)
	resp, err := c.http.Post(apiURL, "application/json", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return fmt.Errorf("telegram api status %d: %s", resp.StatusCode, body)
	}
	return nil
}

type Update struct {
	UpdateID int `json:"update_id"`
	Message  *struct {
		Text string `json:"text"`
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
	q.Set("allowed_updates", `["message"]`)

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
