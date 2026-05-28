package download

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

const userAgent = "docker-vm-runner/1.0"

type Downloader struct {
	Client *http.Client
	Sleep  func(context.Context, time.Duration) error
}

func NewDownloader(client *http.Client) *Downloader {
	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second}
	}
	return &Downloader{
		Client: client,
		Sleep:  sleepContext,
	}
}

func (d *Downloader) Download(ctx context.Context, url string, destination string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("create download request: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)

	resp, err := d.Client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to download %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP error downloading %s: %s", url, resp.Status)
	}

	dir := filepath.Dir(destination)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create destination directory: %w", err)
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(destination)+".*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary download file: %w", err)
	}
	tmpPath := tmp.Name()
	keepTemp := false
	defer func() {
		if !keepTemp {
			_ = os.Remove(tmpPath)
		}
	}()

	if _, err := io.Copy(tmp, resp.Body); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write download %s: %w", url, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temporary download file: %w", err)
	}
	if err := os.Rename(tmpPath, destination); err != nil {
		return fmt.Errorf("move download into place: %w", err)
	}
	keepTemp = true
	return nil
}

func (d *Downloader) DownloadWithRetry(ctx context.Context, url string, destination string, retries int) error {
	if retries < 1 {
		retries = 1
	}
	delays := []time.Duration{5 * time.Second, 10 * time.Second, 20 * time.Second}
	var lastErr error
	for attempt := 1; attempt <= retries; attempt++ {
		if err := d.Download(ctx, url, destination); err != nil {
			lastErr = err
			if attempt == retries {
				break
			}
			delay := delays[min(attempt-1, len(delays)-1)]
			if sleepErr := d.Sleep(ctx, delay); sleepErr != nil {
				return sleepErr
			}
			continue
		}
		return nil
	}
	return lastErr
}

func sleepContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
