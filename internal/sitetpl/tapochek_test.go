package sitetpl

import (
	"testing"
	"time"
)

func TestTapochekTemplateParsesCurrentTopicMarkup(t *testing.T) {
	tmpl := DefaultTapochekTemplate()
	vars := map[string]string{"item.torrent_id": "287865"}
	page := `<html><head><title>&#1044;&#1072;&#1088;&#1072; &#1080;&#1079; &#1056;&#1101;&#1081;&#1074;&#1099;</title></head><body>
<h1 class="maintitle"><a href="viewtopic.php?t=287865">Дара из Рэйвы | Reiwa no Dara-san | Dara-san of Reiwa [TV] [1-8 из 13] [2026] [комедия] [WEBRip] [1080p] [Многоголосый закадровый, (JAP+SUB)]</a></h1>
<table><tr><td width="15%">Трекер:</td><td width="70%">Зарегистрирован &nbsp; [ <span title="6 дней">22-08-2026 18:48</span> ]</td><td><a href="download.php?id=188173" class="genmed">Скачать</a></td></tr></table>
</body></html>`

	if !matchSuccess(page, renderMatchRules(tmpl.Item.Page.Success, vars)) {
		t.Fatal("Tapochek topic readiness marker did not match current markup")
	}

	title := extractString(page, tmpl.Item.Extract["title"])
	wantTitle := "Дара из Рэйвы | Reiwa no Dara-san | Dara-san of Reiwa [TV] [1-8 из 13] [2026] [комедия] [WEBRip] [1080p] [Многоголосый закадровый, (JAP+SUB)]"
	if title != wantTitle {
		t.Fatalf("title = %q, want %q", title, wantTitle)
	}

	updatedText := extractString(page, tmpl.Item.Extract["updated_at"])
	if updatedText != "22-08-2026 18:48" {
		t.Fatalf("updated_at = %q", updatedText)
	}
	updated, err := parseTemplateTime(updatedText, tmpl.Item.Extract["updated_at"])
	if err != nil {
		t.Fatalf("parse updated_at: %v", err)
	}
	if updated.Year() != 2026 || updated.Month() != time.August || updated.Day() != 22 || updated.Hour() != 18 || updated.Minute() != 48 {
		t.Fatalf("unexpected parsed updated_at: %v", updated)
	}

	downloadRef := extractString(page, tmpl.Download.URLFromPage)
	if downloadRef != "download.php?id=188173" {
		t.Fatalf("download URL ref = %q", downloadRef)
	}
	downloadURL, err := resolveTemplateURL(tmpl, "https://tapochek.net/viewtopic.php?t=287865", downloadRef)
	if err != nil {
		t.Fatalf("resolve download URL: %v", err)
	}
	if downloadURL != "https://tapochek.net/download.php?id=188173" {
		t.Fatalf("download URL = %q", downloadURL)
	}
}
