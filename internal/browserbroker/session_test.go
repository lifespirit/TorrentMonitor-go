package browserbroker

import (
	"encoding/json"
	"testing"
)

func TestDecodeCDPFetchResultStringValue(t *testing.T) {
	payload := `{"ok":true,"status":200,"statusText":"OK","url":"https://example.test/file.torrent","contentType":"application/x-bittorrent","body":"YWJj"}`
	raw, _ := json.Marshal(payload)
	var res struct {
		OK     bool   `json:"ok"`
		Status int    `json:"status"`
		Body   string `json:"body"`
	}
	if err := decodeCDPFetchResult(raw, &res); err != nil {
		t.Fatalf("decode string value: %v", err)
	}
	if !res.OK || res.Status != 200 || res.Body != "YWJj" {
		t.Fatalf("unexpected decoded result: %+v", res)
	}
}

func TestDecodeCDPFetchResultObjectValue(t *testing.T) {
	raw := json.RawMessage(`{"ok":true,"status":200,"statusText":"OK","url":"https://example.test/file.torrent","contentType":"application/x-bittorrent","body":"YWJj"}`)
	var res struct {
		OK     bool   `json:"ok"`
		Status int    `json:"status"`
		Body   string `json:"body"`
	}
	if err := decodeCDPFetchResult(raw, &res); err != nil {
		t.Fatalf("decode object value: %v", err)
	}
	if !res.OK || res.Status != 200 || res.Body != "YWJj" {
		t.Fatalf("unexpected decoded result: %+v", res)
	}
}
