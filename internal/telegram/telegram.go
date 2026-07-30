package telegram

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

type Client struct {
	token  string
	chatID string
	http   *http.Client
}

func New(token, chatID string) *Client {
	return &Client{
		token:  token,
		chatID: chatID,
		http:   &http.Client{Timeout: 10 * time.Second},
	}
}

func (c *Client) Send(text string) error {
	payload, err := json.Marshal(map[string]string{
		"chat_id": c.chatID,
		"text":    text,
	})
	if err != nil {
		return err
	}

	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", c.token)
	resp, err := c.http.Post(url, "application/json", bytes.NewReader(payload))
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
