package notify

import (
	"context"
	"time"
)

type Message struct {
	Type      string
	Date      time.Time
	Tracker   string
	Title     string
	Body      string
	TopicID   string
	TorrentID int64
}

type Notifier interface {
	Send(ctx context.Context, msg Message) error
}
