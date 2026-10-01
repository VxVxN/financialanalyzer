package notify

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestTelegramNotify(t *testing.T) {
	var got struct {
		path string
		body map[string]any
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.path = r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&got.body); err != nil {
			t.Errorf("decode: %v", err)
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	tg := NewTelegram("123:secret", "-100500")
	tg.BaseURL = srv.URL
	if err := tg.Notify(context.Background(), "Обновление: ошибка"); err != nil {
		t.Fatalf("Notify: %v", err)
	}
	if got.path != "/bot123:secret/sendMessage" {
		t.Errorf("path = %q", got.path)
	}
	if got.body["chat_id"] != "-100500" || got.body["text"] != "Обновление: ошибка" {
		t.Errorf("body = %v", got.body)
	}
	if _, ok := got.body["parse_mode"]; ok {
		t.Error("parse_mode set: run errors would need escaping")
	}
}

func TestTelegramErrorsHideToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"ok":false,"description":"Bad Request: chat not found"}`))
	}))
	tg := NewTelegram("123:secret", "1")
	tg.BaseURL = srv.URL
	err := tg.Notify(context.Background(), "x")
	if err == nil || !strings.Contains(err.Error(), "chat not found") || strings.Contains(err.Error(), "secret") {
		t.Errorf("API error = %v", err)
	}

	srv.Close() // transport error: *url.Error would quote the URL
	err = tg.Notify(context.Background(), "x")
	if err == nil || strings.Contains(err.Error(), "secret") {
		t.Errorf("transport error = %v", err)
	}
}

func TestTruncate(t *testing.T) {
	if got := Truncate("абв", 3); got != "абв" {
		t.Errorf("short = %q", got)
	}
	got := Truncate(strings.Repeat("я", 10), 5)
	if got != "яяяя…" || utf8.RuneCountInString(got) != 5 {
		t.Errorf("cut = %q", got)
	}
}
