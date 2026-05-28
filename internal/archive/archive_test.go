package archive

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ulikunitz/xz"
)

func TestExtractCompressedGzipXZAndBzip2(t *testing.T) {
	payload := []byte("payload")
	extractor := NewExtractor()

	gzPath := filepath.Join(t.TempDir(), "disk.raw.gz")
	writeGzip(t, gzPath, payload)
	got, err := extractor.Extract(context.Background(), gzPath, filepath.Dir(gzPath))
	if err != nil {
		t.Fatalf("Extract gzip returned error: %v", err)
	}
	if content := readFile(t, got); !bytes.Equal(content, payload) {
		t.Fatalf("gzip payload = %q", content)
	}

	xzPath := filepath.Join(t.TempDir(), "disk.raw.xz")
	writeXZ(t, xzPath, payload)
	got, err = extractor.Extract(context.Background(), xzPath, filepath.Dir(xzPath))
	if err != nil {
		t.Fatalf("Extract xz returned error: %v", err)
	}
	if content := readFile(t, got); !bytes.Equal(content, payload) {
		t.Fatalf("xz payload = %q", content)
	}

	bz2Path := filepath.Join(t.TempDir(), "disk.raw.bz2")
	writeFile(t, bz2Path, mustBase64(t, "QlpoOTFBWSZTWR0big0AAAKBgCQEwCAgACIYaDAJWBMLuSKcKEgOjcUGgA=="))
	got, err = extractor.Extract(context.Background(), bz2Path, filepath.Dir(bz2Path))
	if err != nil {
		t.Fatalf("Extract bzip2 returned error: %v", err)
	}
	if content := readFile(t, got); !bytes.Equal(content, payload) {
		t.Fatalf("bzip2 payload = %q", content)
	}
}

func TestExtractZipAndTarLargestFile(t *testing.T) {
	extractor := NewExtractor()

	zipPath := filepath.Join(t.TempDir(), "disk.zip")
	writeZip(t, zipPath, map[string][]byte{
		"small.txt":       []byte("a"),
		"disk/big.qcow2":  bytes.Repeat([]byte("x"), 10),
		"disk/empty-file": nil,
	})
	got, err := extractor.Extract(context.Background(), zipPath, filepath.Dir(zipPath))
	if err != nil {
		t.Fatalf("Extract zip returned error: %v", err)
	}
	if filepath.Base(got) != "big.qcow2" {
		t.Fatalf("zip extracted path = %s", got)
	}
	if len(readFile(t, got)) != 10 {
		t.Fatalf("zip extracted size mismatch")
	}

	tarPath := filepath.Join(t.TempDir(), "disk.tar")
	writeTar(t, tarPath, map[string][]byte{
		"small.txt":     []byte("a"),
		"disk/big.raw":  bytes.Repeat([]byte("y"), 20),
		"disk/zero.raw": nil,
	})
	got, err = extractor.Extract(context.Background(), tarPath, filepath.Dir(tarPath))
	if err != nil {
		t.Fatalf("Extract tar returned error: %v", err)
	}
	if filepath.Base(got) != "big.raw" {
		t.Fatalf("tar extracted path = %s", got)
	}
	if len(readFile(t, got)) != 20 {
		t.Fatalf("tar extracted size mismatch")
	}
}

func TestExtractSevenZipLargestFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "disk.7z")
	writeFile(t, path, mustBase64(t, "N3q8ryccAASgR6WICAAAAAAAAABmAAAAAAAAAN2R8/FiYXIKZm9vCgEEBgACCQQEAAcLAgABAQABAQAMBAQACAoB6bOiBKhlMn4AAAUCGQUAAAAAABERAGIAYQByAAAAZgBvAG8AAAAZAgAAFBIBAACFM3PyY9YBAFgCcvJj1gEVCgEAIICkgSCApIEAAA=="))

	got, err := NewExtractor().Extract(context.Background(), path, filepath.Dir(path))
	if err != nil {
		t.Fatalf("Extract 7z returned error: %v", err)
	}
	if filepath.Base(got) != "bar" {
		t.Fatalf("7z extracted path = %s", got)
	}
	if len(readFile(t, got)) != 4 {
		t.Fatalf("7z extracted size mismatch")
	}
}

func TestExtractRARUsesRARDecoder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.rar")
	writeFile(t, path, []byte("not-rar"))

	_, err := NewExtractor().Extract(context.Background(), path, filepath.Dir(path))
	if err == nil {
		t.Fatal("expected rar decode error")
	}
	if strings.Contains(err.Error(), "unsupported compressed format") {
		t.Fatalf("rar should be handled by decoder, got: %v", err)
	}
}

func TestExtractRejectsUnsupportedFormat(t *testing.T) {
	path := filepath.Join(t.TempDir(), "disk.unknown")
	writeFile(t, path, []byte("x"))

	_, err := NewExtractor().Extract(context.Background(), path, filepath.Dir(path))
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestExtractRejectsArchiveTraversal(t *testing.T) {
	zipPath := filepath.Join(t.TempDir(), "bad.zip")
	writeZip(t, zipPath, map[string][]byte{
		"../evil.qcow2": bytes.Repeat([]byte("x"), 10),
	})

	_, err := NewExtractor().Extract(context.Background(), zipPath, filepath.Dir(zipPath))
	if err == nil {
		t.Fatal("expected traversal error")
	}
}

func writeGzip(t *testing.T, path string, payload []byte) {
	t.Helper()
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	if _, err := w.Write(payload); err != nil {
		t.Fatalf("write gzip payload: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}
	writeFile(t, path, buf.Bytes())
}

func writeXZ(t *testing.T, path string, payload []byte) {
	t.Helper()
	var buf bytes.Buffer
	w, err := xz.NewWriter(&buf)
	if err != nil {
		t.Fatalf("new xz writer: %v", err)
	}
	if _, err := w.Write(payload); err != nil {
		t.Fatalf("write xz payload: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close xz: %v", err)
	}
	writeFile(t, path, buf.Bytes())
}

func writeZip(t *testing.T, path string, files map[string][]byte) {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for name, payload := range files {
		f, err := w.Create(name)
		if err != nil {
			t.Fatalf("create zip member: %v", err)
		}
		if _, err := f.Write(payload); err != nil {
			t.Fatalf("write zip member: %v", err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	writeFile(t, path, buf.Bytes())
}

func writeTar(t *testing.T, path string, files map[string][]byte) {
	t.Helper()
	var buf bytes.Buffer
	w := tar.NewWriter(&buf)
	for name, payload := range files {
		if err := w.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(payload))}); err != nil {
			t.Fatalf("write tar header: %v", err)
		}
		if _, err := w.Write(payload); err != nil {
			t.Fatalf("write tar member: %v", err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	writeFile(t, path, buf.Bytes())
}

func writeFile(t *testing.T, path string, payload []byte) {
	t.Helper()
	if err := os.WriteFile(path, payload, 0o644); err != nil {
		t.Fatalf("write file %s: %v", path, err)
	}
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file %s: %v", path, err)
	}
	return content
}

func mustBase64(t *testing.T, value string) []byte {
	t.Helper()
	decoded, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		t.Fatalf("decode base64: %v", err)
	}
	return decoded
}
