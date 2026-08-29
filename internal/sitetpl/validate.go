package sitetpl

import (
	"fmt"
	"strings"
)

func validateTemplatePage(tmpl Template, data []byte) error {
	page := decodeBody(data, tmpl.Encoding.Response)

	for _, field := range []string{"title", "updated_at"} {
		ex, ok := tmpl.Item.Extract[field]
		if !ok || !extractDefined(ex) {
			continue
		}
		value := strings.TrimSpace(extractString(page, ex))
		if value == "" {
			return fmt.Errorf("template parse error: %s: required field %q was not found on item page", tmpl.Site, field)
		}
		if field == "updated_at" {
			if _, err := parseTemplateTime(value, ex); err != nil {
				return fmt.Errorf("template parse error: %s: field %q value %q cannot be parsed: %w", tmpl.Site, field, value, err)
			}
		}
	}

	if extractDefined(tmpl.Download.URLFromPage) {
		value := strings.TrimSpace(extractString(page, tmpl.Download.URLFromPage))
		if value == "" {
			return fmt.Errorf("template parse error: %s: required field %q was not found on item page", tmpl.Site, "download.url_from_page")
		}
	}

	return nil
}
