package sitetpl

import "strings"

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func extractDefined(ex Extract) bool {
	return strings.TrimSpace(ex.Selector) != "" || strings.TrimSpace(ex.Regex) != "" || strings.TrimSpace(ex.Regexp) != "" || len(ex.Cleanup) > 0 || strings.TrimSpace(ex.Layout) != "" || len(ex.Layouts) > 0
}

func matchRulesDefined(r MatchRules) bool {
	return strings.TrimSpace(r.Contains) != "" || len(r.ContainsAll) > 0 || len(r.ContainsAny) > 0 || strings.TrimSpace(r.Regex) != "" || strings.TrimSpace(r.Regexp) != ""
}

func topicReadyDefined(r ReadyRules) bool {
	return strings.TrimSpace(r.URL.Exact) != "" || strings.TrimSpace(r.URL.Contains) != "" || len(r.URL.ContainsAny) > 0 || len(r.DOMContains) > 0 || len(r.DOMContainsAll) > 0 || len(r.DOMContainsAny) > 0 || strings.TrimSpace(r.DOMRegex) != "" || strings.TrimSpace(r.Regex) != ""
}

func normalizeMatchRules(r MatchRules) MatchRules {
	if strings.TrimSpace(r.Regex) == "" && strings.TrimSpace(r.Regexp) != "" {
		r.Regex = r.Regexp
	}
	out := MatchRules{Contains: strings.TrimSpace(r.Contains), Regex: strings.TrimSpace(r.Regex)}
	out.ContainsAll = cleanStrings(r.ContainsAll)
	out.ContainsAny = cleanStrings(r.ContainsAny)
	return out
}

func mergeReadyIntoMatchRules(existing MatchRules, ready ReadyRules) MatchRules {
	out := normalizeMatchRules(existing)
	all := append([]string{}, out.ContainsAll...)
	all = append(all, cleanStrings(ready.DOMContains)...)
	all = append(all, cleanStrings(ready.DOMContainsAll)...)
	if out.Contains != "" {
		all = append(all, out.Contains)
		out.Contains = ""
	}
	out.ContainsAll = uniqueStrings(all)
	out.ContainsAny = uniqueStrings(append(out.ContainsAny, cleanStrings(ready.DOMContainsAny)...))
	if strings.TrimSpace(out.Regex) == "" {
		out.Regex = firstNonEmpty(ready.DOMRegex, ready.Regex)
	}
	return out
}

func normalizeExtract(ex Extract, defaults Defaults) Extract {
	if strings.TrimSpace(ex.Regex) == "" && strings.TrimSpace(ex.Regexp) != "" {
		ex.Regex = ex.Regexp
	}
	if strings.TrimSpace(ex.Timezone) == "" {
		ex.Timezone = defaults.Timezone
	}
	return ex
}

func cleanStrings(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		if strings.TrimSpace(s) != "" {
			out = append(out, strings.TrimSpace(s))
		}
	}
	return out
}

func uniqueStrings(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, s := range cleanStrings(in) {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
