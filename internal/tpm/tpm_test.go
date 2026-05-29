package tpm

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestStartDisabledNoop(t *testing.T) {
	supervisor := NewSupervisor(t.TempDir())

	result, err := supervisor.Start(context.Background(), Request{Enabled: false, VMName: "test-vm"})
	if err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	if result.Started {
		t.Fatalf("Started = true")
	}
}

func TestStartCreatesStateDirForLibvirtManagedTPM(t *testing.T) {
	stateDir := t.TempDir()
	supervisor := NewSupervisor(stateDir)

	result, err := supervisor.Start(context.Background(), Request{Enabled: true, VMName: "test-vm"})
	if err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	tpmDir := filepath.Join(stateDir, "tpm", "test-vm")
	if result.StateDir != tpmDir {
		t.Fatalf("StateDir = %q want %q", result.StateDir, tpmDir)
	}
	if !result.Started {
		t.Fatalf("Started = false")
	}
	if _, err := os.Stat(tpmDir); err != nil {
		t.Fatalf("tpm dir stat: %v", err)
	}
}
