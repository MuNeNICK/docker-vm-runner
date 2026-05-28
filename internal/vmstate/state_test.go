package vmstate

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestMarkInstalledWritesStructuredState(t *testing.T) {
	vmDir := t.TempDir()
	now := time.Date(2026, 5, 28, 13, 0, 0, 0, time.UTC)

	if err := MarkInstalled(vmDir, now); err != nil {
		t.Fatalf("MarkInstalled returned error: %v", err)
	}
	state, err := Read(vmDir)
	if err != nil {
		t.Fatalf("Read returned error: %v", err)
	}
	if state.Version != 1 || state.Lifecycle != LifecycleInstalled || !state.UpdatedAt.Equal(now) {
		t.Fatalf("state = %#v", state)
	}
	if _, err := os.Stat(filepath.Join(vmDir, ".installed")); !os.IsNotExist(err) {
		t.Fatalf(".installed stat err = %v", err)
	}
}

func TestReadMissingStateReturnsZeroState(t *testing.T) {
	state, err := Read(t.TempDir())
	if err != nil {
		t.Fatalf("Read returned error: %v", err)
	}
	if IsInstalled(state) {
		t.Fatalf("missing state is installed: %#v", state)
	}
}

func TestReadMalformedStateErrors(t *testing.T) {
	vmDir := t.TempDir()
	if err := os.MkdirAll(filepath.Dir(Path(vmDir)), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(Path(vmDir), []byte("{"), 0o644); err != nil {
		t.Fatalf("write state: %v", err)
	}
	if _, err := Read(vmDir); err == nil {
		t.Fatal("expected parse error")
	}
}
