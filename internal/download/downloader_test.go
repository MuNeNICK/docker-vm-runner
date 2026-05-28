package download

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDownloadSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.UserAgent(); got != "docker-vm-runner/1.0" {
			t.Fatalf("User-Agent = %q", got)
		}
		w.Header().Set("Content-Length", "11")
		_, _ = w.Write([]byte("hello-world"))
	}))
	defer server.Close()

	dest := filepath.Join(t.TempDir(), "image.qcow2")
	if err := NewDownloader(nil).Download(context.Background(), server.URL, dest); err != nil {
		t.Fatalf("Download returned error: %v", err)
	}
	content, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read destination: %v", err)
	}
	if string(content) != "hello-world" {
		t.Fatalf("destination content = %q", content)
	}
}

func TestNewDownloaderDoesNotSetWholeDownloadTimeout(t *testing.T) {
	downloader := NewDownloader(nil)
	if downloader.Client.Timeout != 0 {
		t.Fatalf("client timeout = %s", downloader.Client.Timeout)
	}
}

func TestDownloadChecksumSuccess(t *testing.T) {
	body := []byte("hello-world")
	sum := sha256.Sum256(body)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(body)
	}))
	defer server.Close()

	dest := filepath.Join(t.TempDir(), "image.iso")
	err := NewDownloader(nil).DownloadChecked(context.Background(), server.URL, dest, Checksum{
		Algorithm: "sha256",
		Value:     hex.EncodeToString(sum[:]),
	})
	if err != nil {
		t.Fatalf("DownloadChecked returned error: %v", err)
	}
}

func TestDownloadChecksumMismatchCleansTempFile(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("hello-world"))
	}))
	defer server.Close()

	dir := t.TempDir()
	dest := filepath.Join(dir, "image.iso")
	err := NewDownloader(nil).DownloadChecked(context.Background(), server.URL, dest, Checksum{
		Algorithm: "sha256",
		Value:     "0000",
	})
	if err == nil {
		t.Fatal("expected checksum error")
	}
	if !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, statErr := os.Stat(dest); !os.IsNotExist(statErr) {
		t.Fatalf("destination stat error = %v", statErr)
	}
	assertNoTempFiles(t, dir)
}

func TestDownloadHTTPErrorDoesNotLeaveDestinationOrTempFile(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer server.Close()

	dir := t.TempDir()
	dest := filepath.Join(dir, "image.qcow2")
	err := NewDownloader(nil).Download(context.Background(), server.URL, dest)
	if err == nil {
		t.Fatal("expected error")
	}
	if _, statErr := os.Stat(dest); !os.IsNotExist(statErr) {
		t.Fatalf("destination stat error = %v", statErr)
	}
	assertNoTempFiles(t, dir)
}

func TestDownloadReadErrorCleansTempFile(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "image.qcow2")
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Body:       errReader{},
			Header:     make(http.Header),
		}, nil
	})}

	err := NewDownloader(client).Download(context.Background(), "https://example.com/image.qcow2", dest)
	if err == nil {
		t.Fatal("expected error")
	}
	if _, statErr := os.Stat(dest); !os.IsNotExist(statErr) {
		t.Fatalf("destination stat error = %v", statErr)
	}
	assertNoTempFiles(t, dir)
}

func TestDownloadWithRetryRetriesOnce(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts == 1 {
			http.Error(w, "temporary", http.StatusBadGateway)
			return
		}
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()

	var slept []time.Duration
	downloader := NewDownloader(nil)
	downloader.Sleep = func(ctx context.Context, delay time.Duration) error {
		slept = append(slept, delay)
		return nil
	}

	dest := filepath.Join(t.TempDir(), "image.qcow2")
	if err := downloader.DownloadWithRetry(context.Background(), server.URL, dest, 2); err != nil {
		t.Fatalf("DownloadWithRetry returned error: %v", err)
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d", attempts)
	}
	if len(slept) != 1 || slept[0] != 5*time.Second {
		t.Fatalf("slept = %#v", slept)
	}
}

func TestDownloadWithRetryReturnsLastError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad", http.StatusBadGateway)
	}))
	defer server.Close()

	downloader := NewDownloader(nil)
	downloader.Sleep = func(ctx context.Context, delay time.Duration) error { return nil }

	err := downloader.DownloadWithRetry(context.Background(), server.URL, filepath.Join(t.TempDir(), "image.qcow2"), 2)
	if err == nil {
		t.Fatal("expected error")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) {
	return 0, errors.New("read failed")
}

func (errReader) Close() error {
	return nil
}

func assertNoTempFiles(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	for _, entry := range entries {
		if entry.Name() != "." && entry.Name() != ".." {
			t.Fatalf("unexpected leftover file: %s", entry.Name())
		}
	}
}

var _ io.ReadCloser = errReader{}
