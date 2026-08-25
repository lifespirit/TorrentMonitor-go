package sitetpl

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

type yamlLine struct {
	indent int
	text   string
}

// unmarshalYAMLTemplate parses the small YAML subset used by tracker templates.
// It intentionally supports only maps, string/bool/int scalars, string lists,
// and lists of simple maps. That keeps external templates declarative while
// avoiding a runtime dependency for the scaffold.
func unmarshalYAMLTemplate(data []byte, out any) error {
	root, err := parseMiniYAML(string(data))
	if err != nil {
		return err
	}
	b, err := json.Marshal(root)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, out)
}

func parseMiniYAML(src string) (any, error) {
	lines := tokenizeYAML(src)
	if len(lines) == 0 {
		return map[string]any{}, nil
	}
	v, idx, err := parseYAMLBlock(lines, 0, lines[0].indent)
	if err != nil {
		return nil, err
	}
	if idx < len(lines) {
		return nil, fmt.Errorf("unexpected YAML content at line %d", idx+1)
	}
	return v, nil
}

func tokenizeYAML(src string) []yamlLine {
	raw := strings.Split(src, "\n")
	out := make([]yamlLine, 0, len(raw))
	for _, line := range raw {
		line = strings.TrimRight(line, " \t\r")
		trim := strings.TrimSpace(line)
		if trim == "" || strings.HasPrefix(trim, "#") {
			continue
		}
		indent := 0
		for indent < len(line) && line[indent] == ' ' {
			indent++
		}
		out = append(out, yamlLine{indent: indent, text: strings.TrimSpace(stripYAMLComment(line[indent:]))})
	}
	return out
}

func stripYAMLComment(s string) string {
	inSingle := false
	inDouble := false
	esc := false
	for i, r := range s {
		if esc {
			esc = false
			continue
		}
		if inDouble && r == '\\' {
			esc = true
			continue
		}
		switch r {
		case '\'':
			if !inDouble {
				inSingle = !inSingle
			}
		case '"':
			if !inSingle {
				inDouble = !inDouble
			}
		case '#':
			if !inSingle && !inDouble && (i == 0 || s[i-1] == ' ') {
				return strings.TrimSpace(s[:i])
			}
		}
	}
	return strings.TrimSpace(s)
}

func parseYAMLBlock(lines []yamlLine, idx int, indent int) (any, int, error) {
	if idx >= len(lines) {
		return map[string]any{}, idx, nil
	}
	if lines[idx].indent != indent {
		return nil, idx, fmt.Errorf("unexpected indent at line %d", idx+1)
	}
	if strings.HasPrefix(lines[idx].text, "- ") {
		return parseYAMLSeq(lines, idx, indent)
	}
	return parseYAMLMap(lines, idx, indent)
}

func parseYAMLMap(lines []yamlLine, idx int, indent int) (map[string]any, int, error) {
	m := map[string]any{}
	for idx < len(lines) {
		line := lines[idx]
		if line.indent < indent {
			break
		}
		if line.indent > indent {
			return nil, idx, fmt.Errorf("unexpected nested content at line %d", idx+1)
		}
		if strings.HasPrefix(line.text, "- ") {
			break
		}
		key, val, ok := splitYAMLKeyVal(line.text)
		if !ok || key == "" {
			return nil, idx, fmt.Errorf("invalid YAML map entry at line %d", idx+1)
		}
		idx++
		if strings.TrimSpace(val) != "" {
			m[key] = parseYAMLScalar(val)
			continue
		}
		if idx < len(lines) && lines[idx].indent > indent {
			child, next, err := parseYAMLBlock(lines, idx, lines[idx].indent)
			if err != nil {
				return nil, idx, err
			}
			m[key] = child
			idx = next
		} else {
			m[key] = ""
		}
	}
	return m, idx, nil
}

func parseYAMLSeq(lines []yamlLine, idx int, indent int) ([]any, int, error) {
	seq := []any{}
	for idx < len(lines) {
		line := lines[idx]
		if line.indent < indent {
			break
		}
		if line.indent != indent || !strings.HasPrefix(line.text, "- ") {
			break
		}
		rest := strings.TrimSpace(strings.TrimPrefix(line.text, "- "))
		idx++
		if rest == "" {
			if idx < len(lines) && lines[idx].indent > indent {
				child, next, err := parseYAMLBlock(lines, idx, lines[idx].indent)
				if err != nil {
					return nil, idx, err
				}
				seq = append(seq, child)
				idx = next
			} else {
				seq = append(seq, "")
			}
			continue
		}
		if k, v, ok := splitYAMLKeyVal(rest); ok && k != "" {
			item := map[string]any{k: parseYAMLScalar(v)}
			if idx < len(lines) && lines[idx].indent > indent {
				child, next, err := parseYAMLMap(lines, idx, lines[idx].indent)
				if err != nil {
					return nil, idx, err
				}
				for ck, cv := range child {
					item[ck] = cv
				}
				idx = next
			}
			seq = append(seq, item)
			continue
		}
		seq = append(seq, parseYAMLScalar(rest))
		if idx < len(lines) && lines[idx].indent > indent {
			return nil, idx, errors.New("scalar YAML sequence items cannot have nested children")
		}
	}
	return seq, idx, nil
}

func splitYAMLKeyVal(s string) (string, string, bool) {
	inSingle := false
	inDouble := false
	esc := false
	for i, r := range s {
		if esc {
			esc = false
			continue
		}
		if inDouble && r == '\\' {
			esc = true
			continue
		}
		switch r {
		case '\'':
			if !inDouble {
				inSingle = !inSingle
			}
		case '"':
			if !inSingle {
				inDouble = !inDouble
			}
		case ':':
			if !inSingle && !inDouble {
				return strings.TrimSpace(s[:i]), strings.TrimSpace(s[i+1:]), true
			}
		}
	}
	return "", "", false
}

func parseYAMLScalar(s string) any {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if strings.HasPrefix(s, "\"") && strings.HasSuffix(s, "\"") && len(s) >= 2 {
		if unq, err := strconv.Unquote(s); err == nil {
			return unq
		}
		return strings.Trim(s, "\"")
	}
	if strings.HasPrefix(s, "'") && strings.HasSuffix(s, "'") && len(s) >= 2 {
		return strings.ReplaceAll(s[1:len(s)-1], "''", "'")
	}
	switch strings.ToLower(s) {
	case "true":
		return true
	case "false":
		return false
	case "null", "~":
		return nil
	}
	if n, err := strconv.Atoi(s); err == nil {
		return n
	}
	return s
}
