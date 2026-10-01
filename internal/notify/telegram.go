// Package notify sends operator notifications (failed data refreshes) to a
// Telegram chat through the Bot API.
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
	"unicode/utf8"
)

// Notifier delivers an operator message. A nil Notifier value (not a typed
// nil pointer) means notifications are off.
type Notifier interface {
	Notify(ctx context.Context, text string) error
}

// DefaultBaseURL is the Telegram Bot API endpoint.
const DefaultBaseURL = "https://api.telegram.org"

// MaxMessageLen is Telegram's limit on a message's text, in characters.
const MaxMessageLen = 4096

// Telegram posts messages to one chat as a bot. Construct it with NewTelegram.
type Telegram struct {
	token  string
	chatID string
	// BaseURL is overridable for tests (httptest).
	BaseURL string
	Client  *http.Client
}

// NewTelegram returns a notifier for the bot token and chat id (a numeric id
// or "@channelname").
func NewTelegram(token, chatID string) *Telegram {
	return &Telegram{
		token:   token,
		chatID:  chatID,
		BaseURL: DefaultBaseURL,
		Client:  &http.Client{Timeout: 10 * time.Second},
	}
}

// Notify sends text as a plain-text message, cut to MaxMessageLen. Errors
// never contain the bot token (it is part of the request URL).
func (t *Telegram) Notify(ctx context.Context, text string) error {
	body, err := json.Marshal(map[string]any{
		"chat_id":                  t.chatID,
		"text":                     Truncate(text, MaxMessageLen),
		"disable_web_page_preview": true,
	})
	if err != nil {
		return fmt.Errorf("telegram: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.BaseURL+"/bot"+t.token+"/sendMessage", bytes.NewReader(body))
	if err != nil {
		return errors.New("telegram: bad request URL")
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := t.Client.Do(req)
	if err != nil {
		// *url.Error quotes the URL, token included; keep only the cause.
		var uerr *url.Error
		if errors.As(err, &uerr) {
			err = uerr.Err
		}
		return fmt.Errorf("telegram: send: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	var apiErr struct {
		Description string `json:"description"`
	}
	_ = json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&apiErr)
	return fmt.Errorf("telegram: status %d: %s", resp.StatusCode, apiErr.Description)
}

// Truncate cuts s to at most n characters, marking a cut with "…".
func Truncate(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	if n < 1 {
		return ""
	}
	r := []rune(s)
	return string(r[:n-1]) + "…"
}
