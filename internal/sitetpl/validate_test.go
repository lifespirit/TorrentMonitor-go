package sitetpl

import (
	"strings"
	"testing"
)

func TestValidateTemplatePageReportsMissingRequiredExtract(t *testing.T) {
	tmpl := Template{
		Site: "example.test",
		Item: ItemFlow{Extract: map[string]Extract{
			"title":      {Regex: `<h1>(.*?)</h1>`},
			"updated_at": {Regex: `Updated: ([0-9-]+ [0-9:]+)`, Layout: "2006-01-02 15:04:05"},
		}},
	}

	err := validateTemplatePage(tmpl, []byte(`<html><h1>Release</h1></html>`))
	if err == nil {
		t.Fatal("expected template parse error")
	}
	if !strings.Contains(err.Error(), "template parse error") || !strings.Contains(err.Error(), `"updated_at"`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateTemplatePageReportsInvalidExtractedTime(t *testing.T) {
	tmpl := Template{
		Site: "example.test",
		Item: ItemFlow{Extract: map[string]Extract{
			"updated_at": {Regex: `Updated: ([^<]+)`, Layout: "2006-01-02 15:04:05"},
		}},
	}

	err := validateTemplatePage(tmpl, []byte(`<html>Updated: definitely-not-a-date</html>`))
	if err == nil {
		t.Fatal("expected template parse error")
	}
	if !strings.Contains(err.Error(), "cannot be parsed") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateTemplatePageReportsMissingDownloadURL(t *testing.T) {
	tmpl := Template{
		Site: "example.test",
		Download: DownloadFlow{
			URLFromPage: Extract{Regex: `href="([^"]+\.torrent)"`},
		},
	}

	err := validateTemplatePage(tmpl, []byte(`<html><body>no torrent here</body></html>`))
	if err == nil {
		t.Fatal("expected template parse error")
	}
	if !strings.Contains(err.Error(), "download.url_from_page") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateTemplatePageAcceptsConfiguredExtracts(t *testing.T) {
	tmpl := Template{
		Site: "example.test",
		Item: ItemFlow{Extract: map[string]Extract{
			"title":      {Regex: `<h1>(.*?)</h1>`},
			"updated_at": {Regex: `Updated: ([0-9-]+ [0-9:]+)`, Layout: "2006-01-02 15:04:05"},
		}},
		Download: DownloadFlow{
			URLFromPage: Extract{Regex: `href="([^"]+\.torrent)"`},
		},
	}

	page := []byte(`<html><h1>Release</h1><span>Updated: 2026-08-29 10:00:00</span><a href="download.torrent">Download</a></html>`)
	if err := validateTemplatePage(tmpl, page); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateTemplatePageAllowsUnconfiguredFields(t *testing.T) {
	tmpl := Template{Site: "example.test"}
	if err := validateTemplatePage(tmpl, []byte(`<html></html>`)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
