package oci

import (
	"archive/tar"
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestIsReference(t *testing.T) {
	valid := []string{
		"docker.io/kubevirt/fedora-cloud-container-disk-demo:latest",
		"ghcr.io/munenick/my-vm-disk:v1",
		"registry.example.com/images/vm:1.0",
		"quay.io/libvirt/alpine:edge",
		"localhost:5000/myimage:latest",
	}
	for _, ref := range valid {
		if !IsReference(ref) {
			t.Fatalf("IsReference(%q) = false", ref)
		}
	}

	invalid := []string{
		"https://example.com/image.qcow2",
		"http://example.com/image.qcow2",
		"/local/path/to/image.qcow2",
		"/dev/sda",
		"ubuntu",
		"my-image",
		"library/ubuntu",
		"",
	}
	for _, ref := range invalid {
		if IsReference(ref) {
			t.Fatalf("IsReference(%q) = true", ref)
		}
	}
}

func TestPullCacheHitReturnsExistingDisk(t *testing.T) {
	cacheDir := t.TempDir()
	digest := "sha256:abcdef1234567890"
	digestKey := "sha256-abcdef123456"
	diskDir := filepath.Join(cacheDir, digestKey+"-image_latest")
	if err := os.MkdirAll(diskDir, 0o755); err != nil {
		t.Fatalf("mkdir disk dir: %v", err)
	}
	diskPath := filepath.Join(diskDir, "disk.qcow2")
	if err := os.WriteFile(diskPath, []byte("cached"), 0o644); err != nil {
		t.Fatalf("write disk: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cacheDir, digestKey+"-image_latest.done"), []byte(digest), 0o644); err != nil {
		t.Fatalf("write sentinel: %v", err)
	}

	fetcher := &fakeFetcher{image: fakeImage{digest: digest}}
	result, err := (&Puller{Fetcher: fetcher}).Pull(context.Background(), "docker.io/test/image:latest", cacheDir)
	if err != nil {
		t.Fatalf("Pull returned error: %v", err)
	}
	if result.Path != diskPath {
		t.Fatalf("Path = %s want %s", result.Path, diskPath)
	}
	if fetcher.calls != 1 {
		t.Fatalf("fetch calls = %d", fetcher.calls)
	}
}

func TestPullExtractsKubeVirtDiskDirectory(t *testing.T) {
	cacheDir := t.TempDir()
	fetcher := &fakeFetcher{image: fakeImage{
		digest: "sha256:deadbeef12345678",
		layers: []Layer{
			fakeLayer(tarBytes(t, map[string][]byte{
				"README":           bytes.Repeat([]byte("x"), 20),
				"disk/disk.qcow2":  []byte("disk"),
				"disk/other-notes": bytes.Repeat([]byte("n"), 10),
			})),
		},
	}}

	result, err := (&Puller{Fetcher: fetcher}).Pull(context.Background(), "docker.io/test/image:latest", cacheDir)
	if err != nil {
		t.Fatalf("Pull returned error: %v", err)
	}
	if filepath.Base(result.Path) != "disk.qcow2" {
		t.Fatalf("Path = %s", result.Path)
	}
	if string(readFile(t, result.Path)) != "disk" {
		t.Fatalf("extracted payload = %q", readFile(t, result.Path))
	}
	if result.Fallback {
		t.Fatalf("Fallback = true")
	}
}

func TestPullPrefersKnownDiskCandidateOutsideDiskDirectory(t *testing.T) {
	cacheDir := t.TempDir()
	fetcher := &fakeFetcher{image: fakeImage{
		digest: "sha256:feedface12345678",
		layers: []Layer{
			fakeLayer(tarBytes(t, map[string][]byte{
				"larger.bin":    bytes.Repeat([]byte("x"), 20),
				"images/vm.raw": []byte("rawdisk"),
			})),
		},
	}}

	result, err := (&Puller{Fetcher: fetcher}).Pull(context.Background(), "registry.example.com/images/vm:1.0", cacheDir)
	if err != nil {
		t.Fatalf("Pull returned error: %v", err)
	}
	if filepath.Base(result.Path) != "vm.raw" {
		t.Fatalf("Path = %s", result.Path)
	}
	if result.Fallback {
		t.Fatalf("Fallback = true")
	}
}

func TestPullFallsBackToLargestRegularFile(t *testing.T) {
	cacheDir := t.TempDir()
	fetcher := &fakeFetcher{image: fakeImage{
		digest: "sha256:cafebabe12345678",
		layers: []Layer{
			fakeLayer(tarBytes(t, map[string][]byte{
				"small": []byte("a"),
				"blob":  bytes.Repeat([]byte("b"), 10),
			})),
		},
	}}

	result, err := (&Puller{Fetcher: fetcher}).Pull(context.Background(), "ghcr.io/test/blob:v1", cacheDir)
	if err != nil {
		t.Fatalf("Pull returned error: %v", err)
	}
	if filepath.Base(result.Path) != "blob" {
		t.Fatalf("Path = %s", result.Path)
	}
	if !result.Fallback {
		t.Fatalf("Fallback = false")
	}
}

func TestPullNoDiskFound(t *testing.T) {
	cacheDir := t.TempDir()
	fetcher := &fakeFetcher{image: fakeImage{
		digest: "sha256:badbadbadbadbad1",
		layers: []Layer{fakeLayer([]byte("not a tar"))},
	}}

	_, err := (&Puller{Fetcher: fetcher}).Pull(context.Background(), "docker.io/test/empty:latest", cacheDir)
	if err == nil {
		t.Fatal("expected error")
	}
}

type fakeFetcher struct {
	image Image
	err   error
	calls int
}

func (f *fakeFetcher) Fetch(ctx context.Context, reference string) (Image, error) {
	f.calls++
	return f.image, f.err
}

type fakeImage struct {
	digest string
	layers []Layer
}

func (i fakeImage) Digest() (string, error) {
	return i.digest, nil
}

func (i fakeImage) Layers() ([]Layer, error) {
	return i.layers, nil
}

type fakeLayer []byte

func (l fakeLayer) Open() (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(l)), nil
}

func tarBytes(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	writer := tar.NewWriter(&buf)
	for name, payload := range files {
		if err := writer.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(payload))}); err != nil {
			t.Fatalf("write tar header: %v", err)
		}
		if _, err := writer.Write(payload); err != nil {
			t.Fatalf("write tar payload: %v", err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	return buf.Bytes()
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return content
}
