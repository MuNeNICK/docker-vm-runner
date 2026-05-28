package tpm

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/munenick/docker-vm-runner/internal/process"
)

type Supervisor struct {
	StateDir     string
	StartProcess func(context.Context, process.Command) (Process, error)
	Sleep        func(context.Context, time.Duration) error
}

type Request struct {
	Enabled bool
	VMName  string
}

type Result struct {
	Started    bool
	SocketPath string
	Process    Process
}

type Process interface {
	Running() bool
	Stderr() string
	Stop() error
}

func NewSupervisor(stateDir string) *Supervisor {
	return &Supervisor{
		StateDir:     stateDir,
		StartProcess: startProcess,
		Sleep:        sleepContext,
	}
}

func (s *Supervisor) Start(ctx context.Context, req Request) (Result, error) {
	if !req.Enabled {
		return Result{}, nil
	}
	tpmDir := filepath.Join(s.StateDir, "tpm", req.VMName)
	if err := os.MkdirAll(tpmDir, 0o755); err != nil {
		return Result{}, fmt.Errorf("create TPM state directory: %w", err)
	}
	socketPath := filepath.Join(tpmDir, "swtpm-sock")
	command := process.Command{
		Name: "swtpm",
		Args: []string{
			"socket",
			"--tpmstate", "dir=" + tpmDir,
			"--ctrl", "type=unixio,path=" + socketPath,
			"--tpm2",
		},
	}
	proc, err := s.StartProcess(ctx, command)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Result{}, fmt.Errorf("swtpm not found. Ensure swtpm and swtpm-tools are installed")
		}
		return Result{}, fmt.Errorf("start swtpm: %w", err)
	}
	if err := s.Sleep(ctx, 500*time.Millisecond); err != nil {
		return Result{}, err
	}
	if !proc.Running() {
		return Result{}, fmt.Errorf("swtpm failed to start: %s", proc.Stderr())
	}
	return Result{Started: true, SocketPath: socketPath, Process: proc}, nil
}

type osProcess struct {
	cmd    *exec.Cmd
	stderr *bytes.Buffer
}

func startProcess(ctx context.Context, command process.Command) (Process, error) {
	cmd := exec.CommandContext(ctx, command.Name, command.Args...)
	if command.Dir != "" {
		cmd.Dir = command.Dir
	}
	if len(command.Env) > 0 {
		cmd.Env = append(os.Environ(), command.Env...)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &osProcess{cmd: cmd, stderr: &stderr}, nil
}

func (p *osProcess) Running() bool {
	if p.cmd.ProcessState != nil {
		return false
	}
	if p.cmd.Process == nil {
		return false
	}
	if err := p.cmd.Process.Signal(syscall.Signal(0)); err != nil {
		_ = p.cmd.Wait()
		return false
	}
	return true
}

func (p *osProcess) Stderr() string {
	return p.stderr.String()
}

func (p *osProcess) Stop() error {
	if p.cmd.Process == nil {
		return nil
	}
	if err := p.cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}
	_ = p.cmd.Wait()
	return nil
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
