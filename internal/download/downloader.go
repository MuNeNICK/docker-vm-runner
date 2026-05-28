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

const (
	userAgent       = "docker-vm-runner/1.0"
	DefaultMaxBytes = 64 * 1024 * 1024 * 1024
)

type Downloader struct {
	Client           *http.Client
	Sleep            func(context.Context, time.Duration) error
	MaxBytes         int64
	Label            string
	Progress         func(Progress)
	ProgressInterval time.Duration
}

type Checksum struct {
	Algorithm string
	Value     string
}

type Progress struct {
	Label      string
	URL        string
	Written    int64
	Total      int64
	Attempt    int
	Attempts   int
	Elapsed    time.Duration
	Done       bool
	RetryDelay time.Duration
	Err        error
}

func NewDownloader(client *http.Client) *Downloader {
	if client == nil {
		client = &http.Client{}
	}
	return &Downloader{
		Client:           client,
		Sleep:            sleepContext,
		MaxBytes:         DefaultMaxBytes,
		ProgressInterval: time.Second,
	}
}

func (d *Downloader) Download(ctx context.Context, url string, destination string) error {
	return d.DownloadChecked(ctx, url, destination, Checksum{})
}

func (d *Downloader) DownloadChecked(ctx context.Context, url string, destination string, checksum Checksum) error {
	return d.downloadChecked(ctx, url, destination, checksum, 1, 1)
}

func (d *Downloader) downloadChecked(ctx context.Context, url string, destination string, checksum Checksum, attempt int, attempts int) error {
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
	if d.MaxBytes > 0 && resp.ContentLength > d.MaxBytes {
		return fmt.Errorf("download %s exceeds maximum size: content length %d > %d bytes", url, resp.ContentLength, d.MaxBytes)
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

	started := time.Now()
	if _, err := copyWithLimit(tmp, resp.Body, d.MaxBytes, "download "+url, progressWriter{
		Label:    d.Label,
		URL:      url,
		Total:    resp.ContentLength,
		Attempt:  attempt,
		Attempts: attempts,
		Started:  started,
		Interval: d.ProgressInterval,
		Progress: d.Progress,
	}); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temporary download file: %w", err)
	}
	if err := VerifyChecksum(tmpPath, checksum); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, destination); err != nil {
		return fmt.Errorf("move download into place: %w", err)
	}
	keepTemp = true
	d.reportProgress(Progress{Label: d.Label, URL: url, Written: fileSize(destination), Total: resp.ContentLength, Attempt: attempt, Attempts: attempts, Elapsed: time.Since(started), Done: true})
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
		if err := d.downloadChecked(ctx, url, destination, checksum, attempt, retries); err != nil {
			lastErr = err
			if attempt == retries {
				break
			}
			delay := delays[min(attempt-1, len(delays)-1)]
			d.reportProgress(Progress{Label: d.Label, URL: url, Attempt: attempt, Attempts: retries, Err: err, RetryDelay: delay})
			if sleepErr := d.Sleep(ctx, delay); sleepErr != nil {
				return sleepErr
			}
			continue
		}
		return nil
	}
	return lastErr
}

func VerifyChecksum(path string, checksum Checksum) error {
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

func copyWithLimit(dst io.Writer, src io.Reader, maxBytes int64, label string, progress progressWriter) (int64, error) {
	if progress.Progress != nil {
		progress.report(0, false)
	}
	if maxBytes <= 0 {
		n, err := copyWithProgress(dst, src, progress)
		if err != nil {
			return n, fmt.Errorf("write %s: %w", label, err)
		}
		return n, nil
	}
	limited := &io.LimitedReader{R: src, N: maxBytes + 1}
	n, err := copyWithProgress(dst, limited, progress)
	if err != nil {
		return n, fmt.Errorf("write %s: %w", label, err)
	}
	if n > maxBytes {
		return n, fmt.Errorf("%s exceeds maximum size: %d > %d bytes", label, n, maxBytes)
	}
	return n, nil
}

type progressWriter struct {
	Label        string
	URL          string
	Total        int64
	Attempt      int
	Attempts     int
	Started      time.Time
	Interval     time.Duration
	Progress     func(Progress)
	written      int64
	lastReported time.Time
}

func copyWithProgress(dst io.Writer, src io.Reader, progress progressWriter) (int64, error) {
	if progress.Progress == nil {
		return io.Copy(dst, src)
	}
	buffer := make([]byte, 128*1024)
	for {
		nr, er := src.Read(buffer)
		if nr > 0 {
			nw, ew := dst.Write(buffer[:nr])
			progress.written += int64(nw)
			progress.report(progress.written, false)
			if ew != nil {
				return progress.written, ew
			}
			if nr != nw {
				return progress.written, io.ErrShortWrite
			}
		}
		if er != nil {
			if er == io.EOF {
				return progress.written, nil
			}
			return progress.written, er
		}
	}
}

func (p *progressWriter) report(written int64, done bool) {
	if p.Progress == nil {
		return
	}
	now := time.Now()
	if !done && written > 0 && p.Interval > 0 && !p.lastReported.IsZero() && now.Sub(p.lastReported) < p.Interval {
		return
	}
	p.lastReported = now
	p.Progress(Progress{
		Label:    p.Label,
		URL:      p.URL,
		Written:  written,
		Total:    p.Total,
		Attempt:  p.Attempt,
		Attempts: p.Attempts,
		Elapsed:  now.Sub(p.Started),
		Done:     done,
	})
}

func (d *Downloader) reportProgress(progress Progress) {
	if d.Progress != nil {
		d.Progress(progress)
	}
}

func fileSize(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
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
