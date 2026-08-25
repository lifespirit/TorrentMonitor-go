package scheduler

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

type Core interface {
	RunMonitorOnce(ctx context.Context) error
	RunTorrentOnce(ctx context.Context, id int64) error
	RunTemplateUpdate(ctx context.Context) error
}

type Config struct {
	MonitorInterval  time.Duration
	TemplateInterval time.Duration
}

type Scheduler struct {
	cfg           Config
	core          Core
	logger        *slog.Logger
	jobs          map[string]*Job
	mu            sync.RWMutex
	monitorMu     sync.Mutex
	changed       chan struct{}
	torrentChecks chan int64
}

type Job struct {
	Name            string     `json:"name"`
	Running         bool       `json:"running"`
	LastStart       *time.Time `json:"last_start"`
	LastFinish      *time.Time `json:"last_finish"`
	NextRun         *time.Time `json:"next_run"`
	LastError       string     `json:"last_error"`
	IntervalSeconds int64      `json:"interval_seconds"`
	running         atomic.Bool
}

type Status struct {
	Jobs []Job `json:"jobs"`
}

func New(cfg Config, core Core, logger *slog.Logger) *Scheduler {
	if cfg.MonitorInterval <= 0 {
		cfg.MonitorInterval = 15 * time.Minute
	}
	return &Scheduler{
		cfg:           cfg,
		core:          core,
		logger:        logger,
		changed:       make(chan struct{}, 1),
		torrentChecks: make(chan int64, 256),
		jobs: map[string]*Job{
			"monitor":   {Name: "monitor"},
			"templates": {Name: "templates"},
		},
	}
}

func (s *Scheduler) Start(ctx context.Context) {
	go s.loop(ctx, "monitor", s.runMonitorOnce, s.monitorInterval)
	go s.loop(ctx, "templates", s.core.RunTemplateUpdate, s.templateInterval)
	go s.torrentCheckLoop(ctx)
}

func (s *Scheduler) RunNow(ctx context.Context, name string) error {
	switch name {
	case "monitor":
		return s.run(ctx, name, s.runMonitorOnce, false)
	case "templates", "template_update":
		return s.run(ctx, "templates", s.core.RunTemplateUpdate, false)
	default:
		return nil
	}
}

func (s *Scheduler) QueueTorrentCheck(id int64) {
	if id <= 0 {
		return
	}
	s.torrentChecks <- id
}

func (s *Scheduler) runMonitorOnce(ctx context.Context) error {
	s.monitorMu.Lock()
	defer s.monitorMu.Unlock()
	return s.core.RunMonitorOnce(ctx)
}

func (s *Scheduler) torrentCheckLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case id := <-s.torrentChecks:
			s.monitorMu.Lock()
			err := s.core.RunTorrentOnce(ctx, id)
			s.monitorMu.Unlock()
			if err != nil {
				s.logger.Error("queued torrent check failed", "id", id, "error", err)
			}
		}
	}
}

func (s *Scheduler) SetMonitorInterval(interval time.Duration) {
	if interval <= 0 {
		interval = 15 * time.Minute
	}
	s.mu.Lock()
	s.cfg.MonitorInterval = interval
	next := time.Now().Add(interval)
	if job := s.jobs["monitor"]; job != nil {
		job.NextRun = &next
	}
	s.mu.Unlock()
	s.notifyChanged()
}

func (s *Scheduler) SetTemplateInterval(interval time.Duration) {
	s.mu.Lock()
	s.cfg.TemplateInterval = interval
	if job := s.jobs["templates"]; job != nil {
		if interval > 0 {
			next := time.Now().Add(interval)
			job.NextRun = &next
		} else {
			job.NextRun = nil
		}
	}
	s.mu.Unlock()
	s.notifyChanged()
}

func (s *Scheduler) Status() Status {
	s.mu.RLock()
	defer s.mu.RUnlock()
	jobs := make([]Job, 0, len(s.jobs))
	for _, job := range s.jobs {
		snap := *job
		snap.Running = job.running.Load()
		switch snap.Name {
		case "monitor":
			snap.IntervalSeconds = int64(s.cfg.MonitorInterval.Seconds())
		case "templates":
			snap.IntervalSeconds = int64(s.cfg.TemplateInterval.Seconds())
		}
		jobs = append(jobs, snap)
	}
	return Status{Jobs: jobs}
}

func (s *Scheduler) loop(ctx context.Context, name string, fn func(context.Context) error, intervalFn func() time.Duration) {
	for {
		interval := intervalFn()
		if interval <= 0 {
			s.setNextRun(name, nil)
			select {
			case <-ctx.Done():
				return
			case <-s.changed:
				continue
			}
		}
		next := time.Now().Add(interval)
		s.setNextRun(name, &next)
		timer := time.NewTimer(interval)

		select {
		case <-ctx.Done():
			stopTimer(timer)
			return
		case <-s.changed:
			stopTimer(timer)
			continue
		case <-timer.C:
			_ = s.run(ctx, name, fn, true)
		}
	}
}

func (s *Scheduler) monitorInterval() time.Duration {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.cfg.MonitorInterval <= 0 {
		return 15 * time.Minute
	}
	return s.cfg.MonitorInterval
}

func (s *Scheduler) templateInterval() time.Duration {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg.TemplateInterval
}

func stopTimer(timer *time.Timer) {
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
}

func (s *Scheduler) run(ctx context.Context, name string, fn func(context.Context) error, scheduleNext bool) error {
	job := s.jobs[name]
	if job == nil {
		return nil
	}
	if !job.running.CompareAndSwap(false, true) {
		s.logger.Info("job already running", "job", name)
		return nil
	}
	defer job.running.Store(false)

	start := time.Now()
	s.mu.Lock()
	job.LastStart = &start
	job.LastError = ""
	s.mu.Unlock()

	err := fn(ctx)

	finish := time.Now()
	s.mu.Lock()
	job.LastFinish = &finish
	if scheduleNext {
		if interval := s.intervalForLocked(name); interval > 0 {
			next := finish.Add(interval)
			job.NextRun = &next
		} else {
			job.NextRun = nil
		}
	}
	if err != nil {
		job.LastError = err.Error()
	}
	s.mu.Unlock()

	if err != nil {
		s.logger.Error("job failed", "job", name, "error", err)
	}
	return err
}

func (s *Scheduler) intervalForLocked(name string) time.Duration {
	switch name {
	case "monitor":
		return s.cfg.MonitorInterval
	case "templates":
		return s.cfg.TemplateInterval
	default:
		return 0
	}
}

func (s *Scheduler) setNextRun(name string, t *time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if job := s.jobs[name]; job != nil {
		job.NextRun = t
	}
}

func (s *Scheduler) notifyChanged() {
	select {
	case s.changed <- struct{}{}:
	default:
	}
}
