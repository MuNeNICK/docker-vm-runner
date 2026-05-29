package services

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/munenick/docker-vm-runner/internal/process"
)

var ErrWaitTimeout = errors.New("process wait timeout")

type RuntimeInfo struct {
	Rootless   bool
	Privileged bool
}

type Options struct {
	RunDir        string
	VarRunDir     string
	LibvirtdPath  string
	VirtlogdPath  string
	LibvirtdConf  string
	VirtlogdConf  string
	Runtime       RuntimeInfo
	WarningWriter io.Writer
	SocketCleaner func(string) error
}

type Supervisor struct {
	Options      Options
	Processes    []Process
	StartProcess func(context.Context, process.Command) (Process, error)
	Sleep        func(context.Context, time.Duration) error
	WaitPath     func(context.Context, string, time.Duration) bool
	SocketProbe  func(string) SocketState
	shutdown     bool
}

type Process interface {
	Running() bool
	ExitCode() int
	Stderr() string
	Terminate() error
	Kill() error
	Wait(context.Context, time.Duration) error
}

type SocketState int

const (
	SocketMissing SocketState = iota
	SocketActive
	SocketStale
	SocketUnknown
)

func NewSupervisor(opts Options) *Supervisor {
	applyDefaults(&opts)
	supervisor := &Supervisor{
		Options:      opts,
		StartProcess: startProcess,
		Sleep:        sleepContext,
		WaitPath:     waitPath,
		SocketProbe:  probeSocket,
	}
	if supervisor.Options.SocketCleaner == nil {
		supervisor.Options.SocketCleaner = supervisor.CleanupSocket
	}
	return supervisor
}

func applyDefaults(opts *Options) {
	if opts.RunDir == "" {
		opts.RunDir = "/run/libvirt"
	}
	if opts.VarRunDir == "" {
		opts.VarRunDir = "/var/run/libvirt"
	}
	if opts.LibvirtdPath == "" {
		opts.LibvirtdPath = "/usr/sbin/libvirtd"
	}
	if opts.VirtlogdPath == "" {
		opts.VirtlogdPath = "/usr/sbin/virtlogd"
	}
	if opts.LibvirtdConf == "" {
		opts.LibvirtdConf = "/etc/libvirt/libvirtd.conf"
	}
	if opts.VirtlogdConf == "" {
		opts.VirtlogdConf = "/etc/libvirt/virtlogd.conf"
	}
}

func (s *Supervisor) Start(ctx context.Context) (err error) {
	started := false
	defer func() {
		if err != nil && started {
			stopCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_ = s.Stop(stopCtx)
		}
	}()
	if err := os.MkdirAll(s.Options.RunDir, 0o755); err != nil {
		return fmt.Errorf("create libvirt run directory: %w", err)
	}
	if err := os.MkdirAll(s.Options.VarRunDir, 0o755); err != nil {
		return fmt.Errorf("create libvirt var-run directory: %w", err)
	}
	for _, socketPath := range s.socketPaths() {
		if err := s.Options.SocketCleaner(socketPath); err != nil {
			return err
		}
	}
	virtlogd, err := s.startAndAssert(ctx, "virtlogd", serviceCommand(s.Options.VirtlogdPath, s.Options.VirtlogdConf))
	if err != nil {
		return err
	}
	s.Processes = append(s.Processes, virtlogd)
	started = true
	libvirtd, err := s.startAndAssert(ctx, "libvirtd", serviceCommand(s.Options.LibvirtdPath, s.Options.LibvirtdConf))
	if err != nil {
		return err
	}
	s.Processes = append(s.Processes, libvirtd)
	return s.WaitForLibvirt(ctx)
}

func (s *Supervisor) startAndAssert(ctx context.Context, name string, cmd process.Command) (Process, error) {
	proc, err := s.StartProcess(ctx, cmd)
	if err != nil {
		return nil, fmt.Errorf("start %s: %w", name, err)
	}
	if err := s.Sleep(ctx, 500*time.Millisecond); err != nil {
		_ = proc.Terminate()
		stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = proc.Wait(stopCtx, 5*time.Second)
		return nil, err
	}
	if !proc.Running() {
		return nil, fmt.Errorf("%s exited prematurely (code %d): %s", name, proc.ExitCode(), proc.Stderr())
	}
	return proc, nil
}

func serviceCommand(binary string, conf string) process.Command {
	cmd := process.Command{Name: binary}
	if _, err := os.Stat(conf); err == nil {
		cmd.Args = []string{"-f", conf}
	}
	return cmd
}

func (s *Supervisor) WaitForLibvirt(ctx context.Context) error {
	if !s.anyPath(ctx, []string{
		filepath.Join(s.Options.RunDir, "libvirt-sock"),
		filepath.Join(s.Options.VarRunDir, "libvirt-sock"),
	}, 15*time.Second) {
		msg := "libvirt socket did not appear; run the container with --privileged, --cgroupns=host, and /dev/kvm when hardware acceleration is required"
		if s.Options.Runtime.Rootless {
			s.warnf("%s", msg)
			return nil
		}
		return fmt.Errorf("%s", msg)
	}
	if !s.anyPath(ctx, []string{
		filepath.Join(s.Options.RunDir, "virtlogd-sock"),
		filepath.Join(s.Options.VarRunDir, "virtlogd-sock"),
	}, 15*time.Second) {
		msg := "virtlogd socket did not appear; check container privileges and libvirt startup logs"
		if s.Options.Runtime.Rootless {
			s.warnf("%s", msg)
			return nil
		}
		return fmt.Errorf("%s", msg)
	}
	return nil
}

func (s *Supervisor) warnf(format string, args ...any) {
	if s.Options.WarningWriter == nil {
		return
	}
	fmt.Fprintf(s.Options.WarningWriter, "[WARN] "+format+"\n", args...)
}

func (s *Supervisor) anyPath(ctx context.Context, paths []string, timeout time.Duration) bool {
	for _, path := range paths {
		if s.WaitPath(ctx, path, timeout) {
			return true
		}
	}
	return false
}

func (s *Supervisor) CleanupSocket(path string) error {
	if _, err := os.Stat(path); err != nil {
		return nil
	}
	switch s.SocketProbe(path) {
	case SocketActive:
		return nil
	case SocketStale:
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove stale socket %s: %w", path, err)
		}
		return nil
	default:
		return nil
	}
}

func (s *Supervisor) Stop(ctx context.Context) error {
	if s.shutdown {
		return nil
	}
	s.shutdown = true
	for _, proc := range s.Processes {
		if proc.Running() {
			if err := proc.Terminate(); err != nil {
				return fmt.Errorf("terminate process: %w", err)
			}
		}
	}
	for _, proc := range s.Processes {
		if !proc.Running() {
			continue
		}
		err := proc.Wait(ctx, 5*time.Second)
		if errors.Is(err, ErrWaitTimeout) {
			if killErr := proc.Kill(); killErr != nil {
				return fmt.Errorf("kill process after timeout: %w", killErr)
			}
			continue
		}
		if err != nil {
			return fmt.Errorf("wait process: %w", err)
		}
	}
	return nil
}

func (s *Supervisor) socketPaths() []string {
	return []string{
		filepath.Join(s.Options.RunDir, "libvirt-sock"),
		filepath.Join(s.Options.VarRunDir, "libvirt-sock"),
		filepath.Join(s.Options.RunDir, "virtlogd-sock"),
		filepath.Join(s.Options.VarRunDir, "virtlogd-sock"),
	}
}

type osProcess struct {
	cmd      *exec.Cmd
	stderr   *bytes.Buffer
	waitDone chan struct{}
	waitErr  error
	mu       sync.Mutex
}

func startProcess(ctx context.Context, command process.Command) (Process, error) {
	cmd := exec.Command(command.Name, command.Args...)
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
	proc := &osProcess{cmd: cmd, stderr: &stderr, waitDone: make(chan struct{})}
	go func() {
		err := cmd.Wait()
		proc.mu.Lock()
		proc.waitErr = err
		proc.mu.Unlock()
		close(proc.waitDone)
	}()
	return proc, nil
}

func (p *osProcess) Running() bool {
	if p.cmd.Process == nil {
		return false
	}
	select {
	case <-p.waitDone:
		return false
	default:
	}
	if err := p.cmd.Process.Signal(syscall.Signal(0)); err != nil {
		return false
	}
	return true
}

func (p *osProcess) ExitCode() int {
	if p.cmd.ProcessState == nil {
		return 0
	}
	return p.cmd.ProcessState.ExitCode()
}

func (p *osProcess) Stderr() string {
	return p.stderr.String()
}

func (p *osProcess) Terminate() error {
	if p.cmd.Process == nil {
		return nil
	}
	return p.cmd.Process.Signal(syscall.SIGTERM)
}

func (p *osProcess) Kill() error {
	if p.cmd.Process == nil {
		return nil
	}
	if err := p.cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}
	return nil
}

func (p *osProcess) Wait(ctx context.Context, timeout time.Duration) error {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-p.waitDone:
		p.mu.Lock()
		defer p.mu.Unlock()
		return p.waitErr
	case <-timer.C:
		return ErrWaitTimeout
	case <-ctx.Done():
		return ctx.Err()
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

func waitPath(ctx context.Context, path string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(100 * time.Millisecond):
		}
	}
	return false
}

func probeSocket(path string) SocketState {
	conn, err := net.DialTimeout("unix", path, 200*time.Millisecond)
	if err == nil {
		_ = conn.Close()
		return SocketActive
	}
	if errors.Is(err, os.ErrNotExist) {
		return SocketMissing
	}
	return SocketStale
}
