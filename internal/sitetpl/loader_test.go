package sitetpl

import (
	"archive/zip"
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadTemplateYAML(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "site-templates", "rutracker.org.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	tmpl, err := LoadTemplateBytes("rutracker.org.yaml", data)
	if err != nil {
		t.Fatal(err)
	}
	if tmpl.Site != "rutracker.org" {
		t.Fatalf("unexpected site: %q", tmpl.Site)
	}
	if tmpl.Download.Request.Cookies["bb_dl"] == "" {
		t.Fatalf("download cookie was not parsed")
	}
}

func TestLoadNNMClubTemplateYAML(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "site-templates", "nnmclub.to.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	tmpl, err := LoadTemplateBytes("nnmclub.to.yaml", data)
	if err != nil {
		t.Fatal(err)
	}
	if tmpl.Site != "nnmclub.to" {
		t.Fatalf("unexpected site: %q", tmpl.Site)
	}
	if tmpl.Download.URLFromPage.Regex == "" {
		t.Fatalf("download url_from_page regex was not parsed")
	}
	if _, ok := DefaultRegistry().Get("nnm-club.name"); !ok {
		t.Fatalf("nnm-club.name alias was not registered")
	}
}

func TestUpdateTemplatesFromZipURL(t *testing.T) {
	buf := new(bytes.Buffer)
	zw := zip.NewWriter(buf)
	w, err := zw.Create("trackers/test.example.yaml")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = w.Write([]byte(`version: 1
site: test.example
kind: forum
mode: http
item:
  page:
    method: GET
    url: "https://test.example/topic/{{ item.torrent_id }}"
`))
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/zip")
		_, _ = w.Write(buf.Bytes())
	}))
	defer srv.Close()
	dir := t.TempDir()
	res, err := UpdateTemplatesFromSource(context.Background(), srv.URL+"/templates.zip", dir, srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	if res.Loaded != 1 {
		t.Fatalf("expected 1 loaded template, got %d", res.Loaded)
	}
	reg, _, err := LoadRegistryFromDirectory(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := reg.Get("test.example"); !ok {
		t.Fatalf("updated template was not registered")
	}
}
