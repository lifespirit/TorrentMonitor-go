package sitetpl

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type LoadResult struct {
	Directory string   `json:"directory"`
	Loaded    int      `json:"loaded"`
	Files     []string `json:"files"`
}

type UpdateTemplatesResult struct {
	SourceURL string    `json:"source_url"`
	Directory string    `json:"directory"`
	UpdatedAt time.Time `json:"updated_at"`
	Loaded    int       `json:"loaded"`
	Files     []string  `json:"files"`
	Skipped   bool      `json:"skipped"`
	Message   string    `json:"message"`
}

func LoadRegistryFromDirectory(dir string) (*Registry, LoadResult, error) {
	reg := DefaultRegistry()
	res := LoadResult{Directory: dir}
	if strings.TrimSpace(dir) == "" {
		return reg, res, nil
	}
	info, err := os.Stat(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return reg, res, nil
		}
		return nil, res, err
	}
	if !info.IsDir() {
		return nil, res, fmt.Errorf("template directory %s is not a directory", dir)
	}
	err = filepath.WalkDir(dir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		if !isTemplateFile(path) {
			return nil
		}
		tmpl, err := LoadTemplateFile(path)
		if err != nil {
			return fmt.Errorf("load template %s: %w", path, err)
		}
		reg.Register(tmpl)
		res.Loaded++
		res.Files = append(res.Files, path)
		return nil
	})
	sort.Strings(res.Files)
	return reg, res, err
}

func LoadTemplateFile(path string) (Template, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Template{}, err
	}
	tmpl, err := LoadTemplateBytes(filepath.Base(path), data)
	if err != nil {
		return Template{}, err
	}
	tmpl.Source = path
	return tmpl, nil
}

func LoadTemplateBytes(name string, data []byte) (Template, error) {
	var tmpl Template
	ext := strings.ToLower(filepath.Ext(name))
	switch ext {
	case ".json":
		if err := json.Unmarshal(data, &tmpl); err != nil {
			return Template{}, err
		}
	case ".yaml", ".yml":
		if err := unmarshalYAMLTemplate(data, &tmpl); err != nil {
			return Template{}, err
		}
	default:
		trim := bytes.TrimSpace(data)
		if len(trim) > 0 && trim[0] == '{' {
			if err := json.Unmarshal(trim, &tmpl); err != nil {
				return Template{}, err
			}
		} else if err := unmarshalYAMLTemplate(trim, &tmpl); err != nil {
			return Template{}, err
		}
	}
	NormalizeTemplate(&tmpl)
	if strings.TrimSpace(tmpl.Site) == "" {
		return Template{}, errors.New("template site/id is empty")
	}
	if tmpl.Version == 0 {
		return Template{}, errors.New("template version/schema_version is empty")
	}
	if tmpl.Version != 1 {
		return Template{}, fmt.Errorf("template schema version %d is not supported", tmpl.Version)
	}
	return tmpl, nil
}

func UpdateTemplatesFromSource(ctx context.Context, source string, dir string, client *http.Client) (UpdateTemplatesResult, error) {
	source = strings.TrimSpace(source)
	res := UpdateTemplatesResult{SourceURL: source, Directory: dir, UpdatedAt: time.Now()}
	if source == "" {
		res.Skipped = true
		res.Message = "Источник шаблонов не задан."
		return res, nil
	}
	if strings.TrimSpace(dir) == "" {
		return res, errors.New("template directory is empty")
	}
	data, name, err := readTemplateSource(ctx, source, client)
	if err != nil {
		return res, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return res, err
	}
	if isZipBytes(data) || strings.HasSuffix(strings.ToLower(name), ".zip") {
		files, err := extractTemplateZip(data, dir)
		if err != nil {
			return res, err
		}
		res.Files = files
	} else if isTemplateFile(name) || len(data) > 0 {
		fileName := filepath.Base(name)
		if fileName == "." || fileName == "/" || strings.TrimSpace(fileName) == "" {
			fileName = "template.yaml"
		}
		if !isTemplateFile(fileName) {
			fileName += ".yaml"
		}
		path := filepath.Join(dir, fileName)
		if _, err := LoadTemplateBytes(fileName, data); err != nil {
			return res, fmt.Errorf("downloaded template is invalid: %w", err)
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			return res, err
		}
		res.Files = []string{path}
	}
	reg, load, err := LoadRegistryFromDirectory(dir)
	_ = reg
	if err != nil {
		return res, err
	}
	res.Loaded = load.Loaded
	if len(res.Files) == 0 {
		res.Files = load.Files
	}
	res.Message = fmt.Sprintf("Шаблоны обновлены: %d.", res.Loaded)
	return res, nil
}

func readTemplateSource(ctx context.Context, source string, client *http.Client) ([]byte, string, error) {
	if u, err := url.Parse(source); err == nil && (u.Scheme == "http" || u.Scheme == "https") {
		if client == nil {
			client = &http.Client{Timeout: 60 * time.Second}
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, source, nil)
		if err != nil {
			return nil, "", err
		}
		res, err := client.Do(req)
		if err != nil {
			return nil, "", err
		}
		defer res.Body.Close()
		if res.StatusCode < 200 || res.StatusCode >= 300 {
			return nil, "", fmt.Errorf("download templates: HTTP %d", res.StatusCode)
		}
		data, err := io.ReadAll(io.LimitReader(res.Body, 64<<20))
		if err != nil {
			return nil, "", err
		}
		name := filepath.Base(u.Path)
		return data, name, nil
	}
	if strings.HasPrefix(source, "file://") {
		u, err := url.Parse(source)
		if err != nil {
			return nil, "", err
		}
		source = u.Path
	}
	info, err := os.Stat(source)
	if err != nil {
		return nil, "", err
	}
	if info.IsDir() {
		buf := new(bytes.Buffer)
		zw := zip.NewWriter(buf)
		if err := filepath.WalkDir(source, func(path string, d os.DirEntry, walkErr error) error {
			if walkErr != nil || d.IsDir() || !isTemplateFile(path) {
				return walkErr
			}
			rel, _ := filepath.Rel(source, path)
			w, err := zw.Create(rel)
			if err != nil {
				return err
			}
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			_, err = w.Write(b)
			return err
		}); err != nil {
			_ = zw.Close()
			return nil, "", err
		}
		if err := zw.Close(); err != nil {
			return nil, "", err
		}
		return buf.Bytes(), "templates.zip", nil
	}
	data, err := os.ReadFile(source)
	if err != nil {
		return nil, "", err
	}
	return data, filepath.Base(source), nil
}

func extractTemplateZip(data []byte, dir string) ([]string, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}
	written := []string{}
	for _, f := range zr.File {
		if f.FileInfo().IsDir() || !isTemplateFile(f.Name) {
			continue
		}
		clean := filepath.Clean(f.Name)
		if strings.HasPrefix(clean, "..") || filepath.IsAbs(clean) {
			return nil, fmt.Errorf("unsafe template zip path %q", f.Name)
		}
		r, err := f.Open()
		if err != nil {
			return nil, err
		}
		b, err := io.ReadAll(io.LimitReader(r, 8<<20))
		_ = r.Close()
		if err != nil {
			return nil, err
		}
		if _, err := LoadTemplateBytes(filepath.Base(clean), b); err != nil {
			return nil, fmt.Errorf("invalid template %s: %w", f.Name, err)
		}
		out := filepath.Join(dir, clean)
		if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(out, b, 0o644); err != nil {
			return nil, err
		}
		written = append(written, out)
	}
	sort.Strings(written)
	if len(written) == 0 {
		return nil, errors.New("template archive does not contain .yaml/.yml/.json files")
	}
	return written, nil
}

func isTemplateFile(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return ext == ".yaml" || ext == ".yml" || ext == ".json"
}

func isZipBytes(data []byte) bool {
	return len(data) >= 4 && data[0] == 'P' && data[1] == 'K' && data[2] == 3 && data[3] == 4
}
