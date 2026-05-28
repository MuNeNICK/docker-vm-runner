package download

import (
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const userAgent = "docker-vm-runner/1.0"

type Downloader struct {
	Client *http.Client
	Sleep  func(context.Context, time.Duration) error
}

type Checksum struct {
	Algorithm string
	Value     string
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
	return d.DownloadChecked(ctx, url, destination, Checksum{})
}

func (d *Downloader) DownloadChecked(ctx context.Context, url string, destination string, checksum Checksum) error {
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
	if err := verifyChecksum(tmpPath, checksum); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, destination); err != nil {
		return fmt.Errorf("move download into place: %w", err)
	}
	keepTemp = true
	return nil
}

func (d *Downloader) DownloadWithRetry(ctx context.Context, url string, destination string, retries int) error {
	return d.DownloadWithRetryChecked(ctx, url, destination, retries, Checksum{})
}

func (d *Downloader) DownloadWithRetryChecked(ctx context.Context, url string, destination string, retries int, checksum Checksum) error {
	if retries < 1 {
		retries = 1
	}
	delays := []time.Duration{5 * time.Second, 10 * time.Second, 20 * time.Second}
	var lastErr error
	for attempt := 1; attempt <= retries; attempt++ {
		if err := d.DownloadChecked(ctx, url, destination, checksum); err != nil {
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

func verifyChecksum(path string, checksum Checksum) error {
	if strings.TrimSpace(checksum.Value) == "" {
		return nil
	}
	hasher, normalized, err := checksumHasher(checksum.Algorithm)
	if err != nil {
		return err
	}
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open download for checksum: %w", err)
	}
	defer file.Close()
	if _, err := io.Copy(hasher, file); err != nil {
		return fmt.Errorf("hash download: %w", err)
	}
	got := hex.EncodeToString(hasher.Sum(nil))
	want := strings.ToLower(strings.TrimSpace(checksum.Value))
	if got != want {
		return fmt.Errorf("%s checksum mismatch: got %s want %s", normalized, got, want)
	}
	return nil
}

func checksumHasher(algorithm string) (hash.Hash, string, error) {
	switch strings.ToLower(strings.TrimSpace(algorithm)) {
	case "sha256":
		return sha256.New(), "sha256", nil
	case "sha512":
		return sha512.New(), "sha512", nil
	default:
		return nil, "", fmt.Errorf("unsupported checksum algorithm %q", algorithm)
	}
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
