package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"torrentmonitor-go/internal/app"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	cfg := app.Config{
		ListenAddr:      env("TM_LISTEN", ":8080"),
		MonitorInterval: parseDurationEnv("TM_MONITOR_INTERVAL", 15*time.Minute),
		StoreMode:       env("TM_STORE", "sqlite"),
		DataFile:        env("TM_DATA_FILE", defaultDataFile()),
	}

	application, err := app.New(cfg, logger)
	if err != nil {
		logger.Error("init failed", "error", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := application.Start(ctx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("server failed", "error", err)
		os.Exit(1)
	}
}

func parseDurationEnv(key string, fallback time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		return fallback
	}
	return d
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func defaultDataFile() string {
	if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
		return filepath.Join(xdg, "torrentmonitor-go", "torrentmonitor.sqlite3")
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".local", "share", "torrentmonitor-go", "torrentmonitor.sqlite3")
	}
	return "torrentmonitor.sqlite3"
}
