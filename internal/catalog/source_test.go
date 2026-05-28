package catalog

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadSourceFetchesAndCachesCatalog(t *testing.T) {
	cachePath := filepath.Join(t.TempDir(), "all.json")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, sourceCatalogJSON("ubuntu-24.04-cloud-amd64", "https://example.com/ubuntu.qcow2"))
	}))
	defer server.Close()

	response, err := LoadSource(SourceOptions{URL: server.URL, CachePath: cachePath})
	if err != nil {
		t.Fatalf("LoadSource returned error: %v", err)
	}
	if len(response.Images) != 1 || response.Images[0].ID != "ubuntu-24.04-cloud-amd64" {
		t.Fatalf("response = %#v", response)
	}
	cached, err := os.ReadFile(cachePath)
	if err != nil {
		t.Fatalf("read cache: %v", err)
	}
	if !strings.Contains(string(cached), "ubuntu-24.04-cloud-amd64") {
		t.Fatalf("cache = %s", cached)
	}
}

func TestLoadSourceFallsBackToCache(t *testing.T) {
	cachePath := filepath.Join(t.TempDir(), "all.json")
	if err := os.MkdirAll(filepath.Dir(cachePath), 0o755); err != nil {
		t.Fatalf("create cache dir: %v", err)
	}
	if err := os.WriteFile(cachePath, []byte(sourceCatalogJSON("cached", "https://example.com/cached.qcow2")), 0o644); err != nil {
		t.Fatalf("write cache: %v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	response, err := LoadSource(SourceOptions{URL: server.URL, CachePath: cachePath})
	if err != nil {
		t.Fatalf("LoadSource returned error: %v", err)
	}
	if response.Images[0].ID != "cached" {
		t.Fatalf("image ID = %q", response.Images[0].ID)
	}
}

func TestLoadSourceOfflineUsesCacheOnly(t *testing.T) {
	cachePath := filepath.Join(t.TempDir(), "all.json")
	if err := os.WriteFile(cachePath, []byte(sourceCatalogJSON("offline", "https://example.com/offline.qcow2")), 0o644); err != nil {
		t.Fatalf("write cache: %v", err)
	}

	response, err := LoadSource(SourceOptions{URL: "https://invalid.invalid/catalog.json", CachePath: cachePath, Offline: true})
	if err != nil {
		t.Fatalf("LoadSource returned error: %v", err)
	}
	if response.Images[0].ID != "offline" {
		t.Fatalf("image ID = %q", response.Images[0].ID)
	}
}

func TestLoadSourceRequiresCacheWhenOffline(t *testing.T) {
	_, err := LoadSource(SourceOptions{Offline: true})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "catalog cache path is empty") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func sourceCatalogJSON(id string, url string) string {
	return fmt.Sprintf(`{
  "meta": {"api_version": "v1", "count": 1},
  "images": [
    {
      "id": %q,
      "name": "Ubuntu 24.04 Cloud",
      "image_type": "cloud-image",
      "category": "linux",
      "distro": "ubuntu",
      "version": "24.04",
      "edition": "Cloud",
      "arch": "amd64",
      "release_type": "stable",
      "format": "qcow2",
      "compression": "none",
      "url": %q,
      "status": "supported"
    }
  ]
}`, id, url)
}
