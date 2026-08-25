package scheduler

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"
)

type orderingCore struct {
	monitorStarted chan struct{}
	releaseMonitor chan struct{}
	torrentDone    chan int64
	once           sync.Once
}

func (c *orderingCore) RunMonitorOnce(ctx context.Context) error {
	c.once.Do(func() { close(c.monitorStarted) })
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-c.releaseMonitor:
		return nil
	}
}

func (c *orderingCore) RunTorrentOnce(ctx context.Context, id int64) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case c.torrentDone <- id:
		return nil
	}
}

func (c *orderingCore) RunTemplateUpdate(context.Context) error { return nil }

func TestQueuedTorrentCheckWaitsForRunningMonitor(t *testing.T) {
	core := &orderingCore{
		monitorStarted: make(chan struct{}),
		releaseMonitor: make(chan struct{}),
		torrentDone:    make(chan int64, 1),
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := New(Config{MonitorInterval: time.Hour}, core, logger)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.Start(ctx)

	monitorDone := make(chan error, 1)
	go func() { monitorDone <- s.RunNow(ctx, "monitor") }()
	<-core.monitorStarted
	s.QueueTorrentCheck(42)

	select {
	case id := <-core.torrentDone:
		t.Fatalf("torrent %d was checked before monitor finished", id)
	case <-time.After(50 * time.Millisecond):
	}

	close(core.releaseMonitor)
	if err := <-monitorDone; err != nil {
		t.Fatalf("monitor failed: %v", err)
	}
	select {
	case id := <-core.torrentDone:
		if id != 42 {
			t.Fatalf("checked torrent %d, want 42", id)
		}
	case <-time.After(time.Second):
		t.Fatal("queued torrent check did not run after monitor")
	}
}
