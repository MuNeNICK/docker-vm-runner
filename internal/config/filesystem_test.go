package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseFilesystemsEmpty(t *testing.T) {
	shares, err := ParseFilesystems(MapEnv{}, FilesystemParseOptions{})
	if err != nil {
		t.Fatalf("ParseFilesystems returned error: %v", err)
	}
	if len(shares) != 0 {
		t.Fatalf("share count = %d", len(shares))
	}
}

func TestParseFilesystemAutoDerivesTargetAndCreatesSource(t *testing.T) {
	source := filepath.Join(t.TempDir(), "shared-data")
	shares, err := ParseFilesystems(MapEnv{"FILESYSTEM_SOURCE": source}, FilesystemParseOptions{})
	if err != nil {
		t.Fatalf("ParseFilesystems returned error: %v", err)
	}
	if len(shares) != 1 {
		t.Fatalf("share count = %d", len(shares))
	}
	share := shares[0]
	if share.Source != source {
		t.Fatalf("Source = %q", share.Source)
	}
	if share.Target != "shared-data" {
		t.Fatalf("Target = %q", share.Target)
	}
	if share.Driver != "virtiofs" {
		t.Fatalf("Driver = %q", share.Driver)
	}
	if share.AccessMode != "passthrough" {
		t.Fatalf("AccessMode = %q", share.AccessMode)
	}
	if share.Readonly {
		t.Fatal("Readonly = true")
	}
	if stat, err := os.Stat(source); err != nil || !stat.IsDir() {
		t.Fatalf("source was not created as directory: stat=%v err=%v", stat, err)
	}
}

func TestParseFilesystemExpandsHomeSource(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	source := filepath.Join(home, "shared-data")

	shares, err := ParseFilesystems(MapEnv{"FILESYSTEM_SOURCE": "~/shared-data"}, FilesystemParseOptions{})
	if err != nil {
		t.Fatalf("ParseFilesystems returned error: %v", err)
	}
	if len(shares) != 1 {
		t.Fatalf("share count = %d", len(shares))
	}
	if shares[0].Source != source {
		t.Fatalf("Source = %q want %q", shares[0].Source, source)
	}
	if _, err := os.Stat(source); err != nil {
		t.Fatalf("expanded source was not created: %v", err)
	}
}

func TestParseFilesystemReadonlyMissingSourceRaises(t *testing.T) {
	source := filepath.Join(t.TempDir(), "missing-share")
	_, err := ParseFilesystems(MapEnv{
		"FILESYSTEM_SOURCE":   source,
		"FILESYSTEM_READONLY": "1",
	}, FilesystemParseOptions{})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "cannot be created while readonly") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseFilesystemSourceMustBeDirectory(t *testing.T) {
	source := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(source, []byte("data"), 0o644); err != nil {
		t.Fatalf("write source file: %v", err)
	}
	_, err := ParseFilesystems(MapEnv{"FILESYSTEM_SOURCE": source}, FilesystemParseOptions{})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "must point to a directory") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseFilesystemTargetRequiredWhenCannotDerive(t *testing.T) {
	_, err := ParseFilesystems(MapEnv{"FILESYSTEM_SOURCE": "/"}, FilesystemParseOptions{})
	if err == nil {
		t.Fatal("expected target error")
	}
	if !strings.Contains(err.Error(), "FILESYSTEM_TARGET is required") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseFilesystemRejectsTargetWithSlash(t *testing.T) {
	source := t.TempDir()
	_, err := ParseFilesystems(MapEnv{
		"FILESYSTEM_SOURCE": source,
		"FILESYSTEM_TARGET": "bad/target",
	}, FilesystemParseOptions{})
	if err == nil {
		t.Fatal("expected target error")
	}
	if !strings.Contains(err.Error(), "must be a simple tag") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseFilesystemDriverAndAccessMode(t *testing.T) {
	source := t.TempDir()
	shares, err := ParseFilesystems(MapEnv{
		"FILESYSTEM_SOURCE":     source,
		"FILESYSTEM_TARGET":     "share",
		"FILESYSTEM_DRIVER":     "9p",
		"FILESYSTEM_ACCESSMODE": "mapped",
		"FILESYSTEM_READONLY":   "1",
	}, FilesystemParseOptions{})
	if err != nil {
		t.Fatalf("ParseFilesystems returned error: %v", err)
	}
	share := shares[0]
	if share.Driver != "9p" {
		t.Fatalf("Driver = %q", share.Driver)
	}
	if share.AccessMode != "mapped" {
		t.Fatalf("AccessMode = %q", share.AccessMode)
	}
	if !share.Readonly {
		t.Fatal("Readonly = false")
	}
}

func TestParseFilesystemRejectsInvalidDriver(t *testing.T) {
	source := t.TempDir()
	_, err := ParseFilesystems(MapEnv{
		"FILESYSTEM_SOURCE": source,
		"FILESYSTEM_DRIVER": "bad",
	}, FilesystemParseOptions{})
	if err == nil {
		t.Fatal("expected driver error")
	}
	if !strings.Contains(err.Error(), "Unsupported FILESYSTEM_DRIVER") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseFilesystemRejectsInvalidAccessMode(t *testing.T) {
	source := t.TempDir()
	_, err := ParseFilesystems(MapEnv{
		"FILESYSTEM_SOURCE":     source,
		"FILESYSTEM_ACCESSMODE": "bad",
	}, FilesystemParseOptions{})
	if err == nil {
		t.Fatal("expected access mode error")
	}
	if !strings.Contains(err.Error(), "Unsupported FILESYSTEM_ACCESSMODE") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseFilesystemRejectsVirtioFSNonPassthrough(t *testing.T) {
	source := t.TempDir()
	_, err := ParseFilesystems(MapEnv{
		"FILESYSTEM_SOURCE":     source,
		"FILESYSTEM_DRIVER":     "virtiofs",
		"FILESYSTEM_ACCESSMODE": "mapped",
	}, FilesystemParseOptions{})
	if err == nil {
		t.Fatal("expected access mode error")
	}
	if !strings.Contains(err.Error(), "not supported with virtiofs") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseMultipleFilesystems(t *testing.T) {
	root := t.TempDir()
	first := filepath.Join(root, "first")
	second := filepath.Join(root, "second")
	shares, err := ParseFilesystems(MapEnv{
		"FILESYSTEM_SOURCE":  first,
		"FILESYSTEM2_SOURCE": second,
		"FILESYSTEM2_TARGET": "second-target",
	}, FilesystemParseOptions{})
	if err != nil {
		t.Fatalf("ParseFilesystems returned error: %v", err)
	}
	if len(shares) != 2 {
		t.Fatalf("share count = %d", len(shares))
	}
	if shares[1].Target != "second-target" {
		t.Fatalf("second target = %q", shares[1].Target)
	}
}
