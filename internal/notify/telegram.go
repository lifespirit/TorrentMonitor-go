package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"torrentmonitor-go/internal/sitetpl"
)

type TelegramConfig struct {
	BotToken     string
	ChatID       string
	ThreadID     string
	Silent       bool
	Timeout      time.Duration
	UseProxy     bool
	ProxyType    string
	ProxyAddress string
	APIBaseURL   string
}

type Button struct {
	Label string
	URL   string
}

type TelegramMessage struct {
	Text   string
	Button *Button
}

type TelegramNotifier struct {
	cfg  TelegramConfig
	http *http.Client
}

func NewTelegram(cfg TelegramConfig) (*TelegramNotifier, error) {
	cfg.BotToken = strings.TrimSpace(cfg.BotToken)
	cfg.ChatID = strings.TrimSpace(cfg.ChatID)
	cfg.ThreadID = strings.TrimSpace(cfg.ThreadID)
	if cfg.BotToken == "" {
		return nil, errors.New("telegram bot token is empty")
	}
	if cfg.ChatID == "" {
		return nil, errors.New("telegram chat id is empty")
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 15 * time.Second
	}
	client := &http.Client{Timeout: cfg.Timeout}
	if cfg.UseProxy {
		transport, err := sitetpl.ProxyTransport(cfg.ProxyType, cfg.ProxyAddress)
		if err != nil {
			return nil, fmt.Errorf("configure telegram proxy: %w", err)
		}
		client.Transport = transport
	}
	return &TelegramNotifier{cfg: cfg, http: client}, nil
}

func (n *TelegramNotifier) Send(ctx context.Context, msg TelegramMessage) error {
	text := strings.TrimSpace(msg.Text)
	if text == "" {
		return errors.New("telegram message text is empty")
	}
	payload := map[string]any{
		"chat_id":                  n.cfg.ChatID,
		"text":                     text,
		"disable_web_page_preview": true,
	}
	if n.cfg.Silent {
		payload["disable_notification"] = true
	}
	if thread := strings.TrimSpace(n.cfg.ThreadID); thread != "" {
		if id, err := strconv.ParseInt(thread, 10, 64); err == nil && id != 0 {
			payload["message_thread_id"] = id
		}
	}
	if msg.Button != nil && strings.TrimSpace(msg.Button.Label) != "" && strings.TrimSpace(msg.Button.URL) != "" {
		payload["reply_markup"] = map[string]any{"inline_keyboard": [][]map[string]string{{{
			"text": strings.TrimSpace(msg.Button.Label),
			"url":  strings.TrimSpace(msg.Button.URL),
		}}}}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	baseURL := strings.TrimRight(strings.TrimSpace(n.cfg.APIBaseURL), "/")
	if baseURL == "" {
		baseURL = "https://api.telegram.org"
	}
	url := baseURL + "/bot" + n.cfg.BotToken + "/sendMessage"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := n.http.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return fmt.Errorf("telegram API returned HTTP %d: %s", res.StatusCode, strings.TrimSpace(string(b)))
	}
	var parsed struct {
		OK          bool   `json:"ok"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal(b, &parsed); err == nil && !parsed.OK {
		if parsed.Description == "" {
			parsed.Description = "telegram API returned ok=false"
		}
		return errors.New(parsed.Description)
	}
	return nil
}
