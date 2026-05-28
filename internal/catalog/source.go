package catalog

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const DefaultURL = "https://munenick.github.io/os-iso-catalog/v1/all.json"

type SourceOptions struct {
	URL       string
	CachePath string
	Offline   bool
	Client    *http.Client
}

func LoadSource(opts SourceOptions) (Response, error) {
	sourceURL := strings.TrimSpace(opts.URL)
	cachePath := strings.TrimSpace(opts.CachePath)

	if opts.Offline || sourceURL == "" {
		if cachePath == "" {
			return Response{}, fmt.Errorf("catalog cache path is empty")
		}
		return loadFile(cachePath)
	}

	if !isHTTPURL(sourceURL) {
		return loadFile(fileURLPath(sourceURL))
	}

	data, err := fetchHTTP(sourceURL, opts.Client)
	if err == nil {
		response, parseErr := Load(bytes.NewReader(data))
		if parseErr == nil {
			if cachePath != "" {
				_ = writeCache(cachePath, data)
			}
			return response, nil
		}
		err = fmt.Errorf("load fetched catalog: %w", parseErr)
	}

	if cachePath == "" {
		return Response{}, fmt.Errorf("load catalog from %s: %w", sourceURL, err)
	}
	cached, cacheErr := loadFile(cachePath)
	if cacheErr == nil {
		return cached, nil
	}
	return Response{}, fmt.Errorf("load catalog from %s failed (%v); cache %s failed: %w", sourceURL, err, cachePath, cacheErr)
}

func loadFile(path string) (Response, error) {
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Response{}, fmt.Errorf("catalog config missing: %s", path)
		}
		return Response{}, fmt.Errorf("read catalog config %s: %w", path, err)
	}
	defer file.Close()
	response, err := Load(file)
	if err != nil {
		return Response{}, fmt.Errorf("load catalog config %s: %w", path, err)
	}
	return response, nil
}

func fetchHTTP(sourceURL string, client *http.Client) ([]byte, error) {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	resp, err := client.Get(sourceURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP status %s", resp.Status)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	return data, nil
}

func writeCache(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create catalog cache directory: %w", err)
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return fmt.Errorf("create catalog cache temp file: %w", err)
	}
	tmpPath := tmp.Name()
	keepTemp := false
	defer func() {
		if !keepTemp {
			_ = os.Remove(tmpPath)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write catalog cache temp file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync catalog cache temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close catalog cache temp file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("move catalog cache into place: %w", err)
	}
	keepTemp = true
	return nil
}

func isHTTPURL(value string) bool {
	return strings.HasPrefix(value, "http://") || strings.HasPrefix(value, "https://")
}

func fileURLPath(value string) string {
	if strings.HasPrefix(value, "file://") {
		parsed, err := url.Parse(value)
		if err == nil {
			return parsed.Path
		}
	}
	return value
}
