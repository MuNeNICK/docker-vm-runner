package tpm

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

type Supervisor struct {
	StateDir string
}

type Request struct {
	Enabled bool
	VMName  string
}

type Result struct {
	Started  bool
	StateDir string
}

func NewSupervisor(stateDir string) *Supervisor {
	return &Supervisor{StateDir: stateDir}
}

func (s *Supervisor) Start(_ context.Context, req Request) (Result, error) {
	if !req.Enabled {
		return Result{}, nil
	}
	tpmDir := filepath.Join(s.StateDir, "tpm", req.VMName)
	if err := os.MkdirAll(tpmDir, 0o755); err != nil {
		return Result{}, fmt.Errorf("create TPM state directory: %w", err)
	}
	return Result{Started: true, StateDir: tpmDir}, nil
}
