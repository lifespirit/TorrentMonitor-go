package app

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"torrentmonitor-go/internal/browserbroker"
	"torrentmonitor-go/internal/core"
	"torrentmonitor-go/internal/scheduler"
	"torrentmonitor-go/internal/store"
	"torrentmonitor-go/internal/web"
)

type Config struct {
	ListenAddr      string
	MonitorInterval time.Duration
	StoreMode       string
	DataFile        string
}

type App struct {
	cfg       Config
	logger    *slog.Logger
	httpSrv   *http.Server
	scheduler *scheduler.Scheduler
}

func New(cfg Config, logger *slog.Logger) (*App, error) {
	var repo core.Repository
	switch cfg.StoreMode {
	case "memory":
		repo = store.NewMemoryStore()
		logger.Info("using in-memory store")
	case "json":
		disk, err := store.NewDiskStore(cfg.DataFile)
		if err != nil {
			return nil, err
		}
		repo = disk
		logger.Info("using json store", "path", disk.Path())
	case "sqlite", "":
		sqlite, err := store.NewSQLiteStore(cfg.DataFile)
		if err != nil {
			return nil, err
		}
		repo = sqlite
		logger.Info("using sqlite store", "path", sqlite.Path())
	default:
		return nil, fmt.Errorf("unsupported TM_STORE mode %q", cfg.StoreMode)
	}

	settings, settingsErr := repo.GetSettings(context.Background())
	if settingsErr != nil {
		settings = core.DefaultSettings()
	}

	browserCfg := core.BrowserConfigFromSettings(settings)
	if strings.TrimSpace(browserCfg.Binary) == "" {
		browserCfg.Binary = os.Getenv("TM_BROWSER_BINARY")
	}
	if strings.TrimSpace(browserCfg.ProfileBase) == "" {
		browserCfg.ProfileBase = os.Getenv("TM_BROWSER_PROFILE_BASE")
	}
	if core.NormalizeBrowserMode(settings.BrowserMode) == core.BrowserModeExternal && strings.TrimSpace(browserCfg.ConnectURL) == "" {
		browserCfg.ConnectURL = os.Getenv("TM_BROWSER_CONNECT_URL")
	}
	if !browserCfg.Debug {
		browserCfg.Debug = envBoolDefault("TM_BROWSER_DEBUG", false)
	}
	browser := browserbroker.New(browserCfg, logger)

	svc := core.NewServiceWithBrowser(repo, logger, nil, browser)
	if res, err := svc.ReloadTemplatesFromSettings(context.Background()); err != nil {
		logger.Warn("failed to load external templates", "error", err)
	} else if res.Loaded > 0 {
		logger.Info("external templates loaded", "directory", res.Directory, "loaded", res.Loaded)
	}

	monitorInterval := cfg.MonitorInterval
	if settings.MonitorIntervalMinutes > 0 {
		monitorInterval = time.Duration(settings.MonitorIntervalMinutes) * time.Minute
	}

	templateInterval := time.Duration(settings.TemplateUpdateIntervalMinutes) * time.Minute
	sch := scheduler.New(scheduler.Config{
		MonitorInterval:  monitorInterval,
		TemplateInterval: templateInterval,
	}, svc, logger)

	srv := web.NewServer(web.Config{}, svc, sch, logger, browser)

	return &App{
		cfg:       cfg,
		logger:    logger,
		scheduler: sch,
		httpSrv: &http.Server{
			Addr:              cfg.ListenAddr,
			Handler:           srv.Handler(),
			ReadHeaderTimeout: 10 * time.Second,
		},
	}, nil
}

func (a *App) Start(ctx context.Context) error {
	a.scheduler.Start(ctx)

	errCh := make(chan error, 1)
	go func() {
		a.logger.Info("listening", "addr", a.cfg.ListenAddr)
		errCh <- a.httpSrv.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return a.httpSrv.Shutdown(shutdownCtx)
	case err := <-errCh:
		return err
	}
}

func envBoolDefault(key string, fallback bool) bool {
	v := strings.TrimSpace(strings.ToLower(os.Getenv(key)))
	if v == "" {
		return fallback
	}
	switch v {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}
