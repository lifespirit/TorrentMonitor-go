package core

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"torrentmonitor-go/internal/sitetpl"
)

type retryUploadRepo struct {
	*credentialRepo
	item     TorrentItem
	settings Settings
	warnings []Warning
}

func (r *retryUploadRepo) GetTorrent(_ context.Context, id int64) (TorrentItem, error) {
	if id != r.item.ID {
		return TorrentItem{}, ErrNotFound
	}
	return r.item, nil
}

func (r *retryUploadRepo) UpdateTorrent(_ context.Context, id int64, patch UpdateTorrentRequest) (TorrentItem, error) {
	if id != r.item.ID {
		return TorrentItem{}, ErrNotFound
	}
	if patch.Name != nil {
		r.item.Name = *patch.Name
	}
	if patch.UpdatedAt != nil {
		v := *patch.UpdatedAt
		r.item.UpdatedAt = &v
	}
	if patch.HasError != nil {
		r.item.HasError = *patch.HasError
	}
	if patch.Closed != nil {
		r.item.Closed = *patch.Closed
	}
	return r.item, nil
}

func (r *retryUploadRepo) SetTorrentClientHash(_ context.Context, id int64, hash string) (TorrentItem, error) {
	if id != r.item.ID {
		return TorrentItem{}, ErrNotFound
	}
	r.item.Hash = hash
	return r.item, nil
}

func (r *retryUploadRepo) GetSettings(context.Context) (Settings, error) {
	return r.settings, nil
}

func (r *retryUploadRepo) CreateWarning(_ context.Context, warning Warning) (Warning, error) {
	r.warnings = append(r.warnings, warning)
	return warning, nil
}

func (r *retryUploadRepo) ClearWarnings(_ context.Context, tracker string) (int, error) {
	count := len(r.warnings)
	r.warnings = nil
	return count, nil
}

func TestFailedClientUploadDoesNotAcknowledgeTorrentUpdate(t *testing.T) {
	torrent := []byte("d8:announce14:http://tracker4:infod4:name4:testee")
	old := time.Date(2026, 6, 24, 14, 30, 0, 0, time.UTC)
	failUpload := true

	qbMux := http.NewServeMux()
	qbMux.HandleFunc("/api/v2/torrents/add", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("qBittorrent add method = %s", r.Method)
		}
		if failUpload {
			http.Error(w, "temporary failure", http.StatusServiceUnavailable)
		} else {
			w.WriteHeader(http.StatusOK)
		}
	})
	qb := httptest.NewServer(qbMux)
	defer qb.Close()

	topic := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/topic":
			_, _ = fmt.Fprint(w, "<html><title>New Name</title><span>25-Июн-26 14:30</span></html>")
		case "/download":
			_, _ = w.Write(torrent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer topic.Close()

	registry := &sitetpl.Registry{}
	registry.Register(sitetpl.Template{
		Version: 1,
		Site:    "example.test",
		Mode:    sitetpl.ModeHTTP,
		Item: sitetpl.ItemFlow{
			Page: sitetpl.HTTPRequest{URL: topic.URL + "/topic"},
			Extract: map[string]sitetpl.Extract{
				"title": {Selector: "title"},
				"updated_at": {
					Regex: `([0-9]{2}-[А-Яа-я]{3}-[0-9]{2} [0-9]{2}:[0-9]{2})`,
					Layout: "02-Jan-06 15:04",
					Locale: "ru",
					Timezone: "Europe/Moscow",
				},
			},
		},
		Download: sitetpl.DownloadFlow{
			Request: sitetpl.HTTPRequest{URL: topic.URL + "/download"},
			Validate: sitetpl.Validate{BencodeTorrent: true},
		},
	})

	settings := DefaultSettings()
	settings.TorrentClient = "qbittorrent"
	settings.TorrentAddress = qb.URL
	repo := &retryUploadRepo{
		credentialRepo: &credentialRepo{},
		item: TorrentItem{
			ID:          1,
			Tracker:     "example.test",
			Type:        TrackerTypeForum,
			Name:        "Old Name",
			TorrentID:   "42",
			UpdatedAt:   &old,
			UpdateTitle: true,
		},
		settings: settings,
	}
	svc := NewServiceWithRunner(repo, nil, sitetpl.NewRunner(sitetpl.WithRegistry(registry)))

	if _, err := svc.CheckTorrent(context.Background(), 1); err == nil {
		t.Fatal("expected the qBittorrent upload failure")
	}
	if repo.item.Name != "Old Name" || !repo.item.UpdatedAt.Equal(old) {
		t.Fatalf("failed upload incorrectly acknowledged torrent update: %+v", repo.item)
	}
	if !repo.item.HasError || len(repo.warnings) == 0 {
		t.Fatalf("failed upload must be surfaced as warning: %+v", repo.item)
	}

	failUpload = false
	result, err := svc.CheckTorrent(context.Background(), 1)
	if err != nil {
		t.Fatalf("retry CheckTorrent: %v", err)
	}
	if !result.Updated || repo.item.Name != "New Name" || repo.item.UpdatedAt.Equal(old) || repo.item.Hash == "" {
		t.Fatalf("retry did not apply the update: result=%+v item=%+v", result, repo.item)
	}
	if repo.item.HasError || len(repo.warnings) != 0 {
		t.Fatalf("retry did not clear previous failure: %+v", repo.item)
	}
}
