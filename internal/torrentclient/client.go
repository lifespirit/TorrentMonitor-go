package torrentclient

import (
	"context"
	"time"
)

type AddRequest struct {
	ID          int64
	FileURL     string
	FileData    []byte
	FileName    string
	SavePath    string
	OldHash     string
	Tracker     string
	DeleteFiles bool
}

type AddResult struct {
	Hash string
}

// CheckResult describes a successful control-plane check against a torrent client.
// It must not add, remove, or otherwise mutate torrents.
type CheckResult struct {
	Version string
}

type Session struct {
	Cookie  string
	Expires *time.Time
}

type SessionProvider interface {
	Session() Session
}

type Client interface {
	Add(ctx context.Context, req AddRequest) (AddResult, error)
	Remove(ctx context.Context, hash string, deleteFiles bool) error
	CheckConnection(ctx context.Context) (CheckResult, error)
}
