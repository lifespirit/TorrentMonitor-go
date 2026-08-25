package notify

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTelegramUsesConfiguredHTTPProxy(t *testing.T) {
	var requestURI string
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestURI = r.RequestURI
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	defer proxy.Close()

	notifier, err := NewTelegram(TelegramConfig{
		BotToken:     "test-token",
		ChatID:       "123",
		UseProxy:     true,
		ProxyType:    "HTTP",
		ProxyAddress: proxy.URL,
		APIBaseURL:   "http://telegram.invalid",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := notifier.Send(context.Background(), TelegramMessage{Text: "test"}); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(requestURI, "http://telegram.invalid/bottest-token/sendMessage") {
		t.Fatalf("request did not pass through HTTP proxy: %q", requestURI)
	}
}

func TestTelegramRejectsUnsupportedProxy(t *testing.T) {
	_, err := NewTelegram(TelegramConfig{
		BotToken:     "test-token",
		ChatID:       "123",
		UseProxy:     true,
		ProxyType:    "unknown",
		ProxyAddress: "127.0.0.1:1",
	})
	if err == nil || !strings.Contains(err.Error(), "unsupported proxy type") {
		t.Fatalf("expected unsupported proxy error, got %v", err)
	}
}
