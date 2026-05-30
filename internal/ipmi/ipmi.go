package ipmi

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/munenick/docker-vm-runner/internal/process"
)

const defaultLibvirtURI = "qemu:///system"

type Options struct {
	StateDir  string
	ConfigDir string
	HomeDir   string
	VBMCDPath string
	VBMCPath  string
}

type Request struct {
	Enabled    bool
	User       string
	Password   string
	Port       int
	SystemID   string
	Address    string
	LibvirtURI string
}

type Result struct {
	Started    bool
	ConfigPath string
	SystemID   string
	Process    Process
}

type Process interface {
	Running() bool
	Stderr() string
	Stop() error
}

type Manager struct {
	Options      Options
	StartProcess func(context.Context, process.Command) (Process, error)
	RunCommand   func(context.Context, process.Command) (process.Result, error)
	Sleep        func(context.Context, time.Duration) error
}

func NewManager(opts Options) *Manager {
	applyDefaults(&opts)
	runner := process.NewCommandRunner()
	return &Manager{
		Options:      opts,
		StartProcess: startProcess,
		RunCommand:   runner.Run,
		Sleep:        sleepContext,
	}
}

func applyDefaults(opts *Options) {
	if opts.StateDir == "" {
		opts.StateDir = "/var/lib/docker-vm-runner"
	}
	if opts.ConfigDir == "" {
		opts.ConfigDir = filepath.Join(opts.StateDir, "ipmi")
	}
	if opts.HomeDir == "" {
		opts.HomeDir = filepath.Join(opts.ConfigDir, "home")
	}
	if opts.VBMCDPath == "" {
		opts.VBMCDPath = "vbmcd"
	}
	if opts.VBMCPath == "" {
		opts.VBMCPath = "vbmc"
	}
}

func (m *Manager) Start(ctx context.Context, req Request) (Result, error) {
	if !req.Enabled {
		return Result{}, nil
	}
	applyRequestDefaults(&req)
	if req.Password == "password" {
		return Result{}, fmt.Errorf("IPMI password must be changed from the default")
	}
	if req.SystemID == "" {
		return Result{}, fmt.Errorf("IPMI system id must not be empty")
	}
	if err := os.MkdirAll(m.Options.ConfigDir, 0o755); err != nil {
		return Result{}, fmt.Errorf("create IPMI config directory: %w", err)
	}
	if err := os.MkdirAll(m.Options.HomeDir, 0o755); err != nil {
		return Result{}, fmt.Errorf("create IPMI home directory: %w", err)
	}
	configPath, err := writeConfig(m.Options.ConfigDir)
	if err != nil {
		return Result{}, err
	}
	env := m.env(configPath)
	proc, err := m.StartProcess(ctx, process.Command{Name: m.Options.VBMCDPath, Env: env})
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Result{}, fmt.Errorf("vbmcd not found. Ensure virtualbmc is installed")
		}
		return Result{}, fmt.Errorf("start vbmcd: %w", err)
	}
	if err := m.Sleep(ctx, 500*time.Millisecond); err != nil {
		_ = proc.Stop()
		return Result{}, err
	}
	if !proc.Running() {
		return Result{}, fmt.Errorf("vbmcd failed to start: %s", proc.Stderr())
	}
	result := Result{Started: true, ConfigPath: configPath, SystemID: req.SystemID, Process: proc}
	_ = m.runVBMC(ctx, configPath, "delete", req.SystemID)
	if err := m.runVBMC(ctx, configPath, addArgs(req)...); err != nil {
		_ = proc.Stop()
		return Result{}, err
	}
	if err := m.runVBMC(ctx, configPath, "start", req.SystemID); err != nil {
		_ = proc.Stop()
		return Result{}, err
	}
	return result, nil
}

func (m *Manager) Stop(ctx context.Context, result Result) error {
	if !result.Started {
		return nil
	}
	var firstErr error
	if result.SystemID != "" {
		if err := m.runVBMC(ctx, result.ConfigPath, "stop", result.SystemID); err != nil && firstErr == nil {
			firstErr = err
		}
		if err := m.runVBMC(ctx, result.ConfigPath, "delete", result.SystemID); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if result.Process != nil {
		if err := result.Process.Stop(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func applyRequestDefaults(req *Request) {
	if req.User == "" {
		req.User = "admin"
	}
	if req.Password == "" {
		req.Password = "password"
	}
	if req.Port == 0 {
		req.Port = 623
	}
	req.SystemID = strings.TrimSpace(req.SystemID)
	if req.Address == "" {
		req.Address = "0.0.0.0"
	}
	if req.LibvirtURI == "" {
		req.LibvirtURI = defaultLibvirtURI
	}
}

func writeConfig(configDir string) (string, error) {
	instancesDir := filepath.Join(configDir, "instances")
	logDir := filepath.Join(configDir, "log")
	for _, dir := range []string{instancesDir, logDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", fmt.Errorf("create IPMI state directory: %w", err)
		}
	}
	configPath := filepath.Join(configDir, "virtualbmc.conf")
	content := fmt.Sprintf(
		"[default]\n"+
			"config_dir = %s\n"+
			"pid_file = %s\n"+
			"\n"+
			"[log]\n"+
			"logfile = %s\n"+
			"debug = false\n"+
			"\n"+
			"[ipmi]\n"+
			"session_timeout = 10\n",
		instancesDir,
		filepath.Join(configDir, "master.pid"),
		filepath.Join(logDir, "vbmcd.log"),
	)
	if err := os.WriteFile(configPath, []byte(content), 0o644); err != nil {
		return "", fmt.Errorf("write VirtualBMC config: %w", err)
	}
	return configPath, nil
}

func addArgs(req Request) []string {
	return []string{
		"add", req.SystemID,
		"--address", req.Address,
		"--port", strconv.Itoa(req.Port),
		"--username", req.User,
		"--password", req.Password,
		"--libvirt-uri", req.LibvirtURI,
	}
}

func (m *Manager) env(configPath string) []string {
	return []string{
		"VIRTUALBMC_CONFIG=" + configPath,
		"HOME=" + m.Options.HomeDir,
	}
}

func (m *Manager) runVBMC(ctx context.Context, configPath string, args ...string) error {
	_, err := m.RunCommand(ctx, process.Command{Name: m.Options.VBMCPath, Args: args, Env: m.env(configPath)})
	if err == nil {
		return nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("vbmc not found. Ensure virtualbmc is installed")
	}
	return fmt.Errorf("run vbmc %s: %w", strings.Join(args, " "), err)
}

type osProcess struct {
	cmd      *exec.Cmd
	stderr   *bytes.Buffer
	waitDone chan struct{}
	waitErr  error
	mu       sync.Mutex
}

func startProcess(_ context.Context, command process.Command) (Process, error) {
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
	<-p.waitDone
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
