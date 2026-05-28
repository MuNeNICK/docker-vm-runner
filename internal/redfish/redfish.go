package redfish

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/munenick/docker-vm-runner/internal/certs"
	"github.com/munenick/docker-vm-runner/internal/process"
	"golang.org/x/crypto/bcrypt"
)

const defaultLibvirtURI = "qemu:///system"

type Options struct {
	StateDir  string
	ConfigDir string
	CertDir   string
	SushyPath string
}

type Request struct {
	Enabled    bool
	User       string
	Password   string
	Port       int
	LibvirtURI string
}

type Result struct {
	Started    bool
	ConfigPath string
	AuthPath   string
	CertPath   string
	KeyPath    string
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
	Sleep        func(context.Context, time.Duration) error
}

func NewManager(opts Options) *Manager {
	applyDefaults(&opts)
	return &Manager{
		Options:      opts,
		StartProcess: startProcess,
		Sleep:        sleepContext,
	}
}

func applyDefaults(opts *Options) {
	if opts.StateDir == "" {
		opts.StateDir = "/var/lib/docker-vm-runner"
	}
	if opts.ConfigDir == "" {
		opts.ConfigDir = filepath.Join(opts.StateDir, "sushy")
	}
	if opts.CertDir == "" {
		opts.CertDir = filepath.Join(opts.StateDir, "certs")
	}
	if opts.SushyPath == "" {
		opts.SushyPath = "sushy-emulator"
	}
}

func (m *Manager) Start(ctx context.Context, req Request) (Result, error) {
	if !req.Enabled {
		return Result{}, nil
	}
	applyRequestDefaults(&req)
	if err := os.MkdirAll(m.Options.ConfigDir, 0o755); err != nil {
		return Result{}, fmt.Errorf("create sushy config directory: %w", err)
	}
	certPath := filepath.Join(m.Options.CertDir, "sushy.crt")
	keyPath := filepath.Join(m.Options.CertDir, "sushy.key")
	if err := certs.EnsureSelfSigned(certs.Request{CertPath: certPath, KeyPath: keyPath}); err != nil {
		return Result{}, err
	}
	authPath, err := writeAuthFile(m.Options.ConfigDir, req.User, req.Password)
	if err != nil {
		return Result{}, err
	}
	configPath, err := writeConfig(m.Options.ConfigDir, req, certPath, keyPath, authPath)
	if err != nil {
		return Result{}, err
	}
	command := process.Command{
		Name: m.Options.SushyPath,
		Args: []string{"--config", configPath, "--libvirt-uri", req.LibvirtURI},
	}
	proc, err := m.StartProcess(ctx, command)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Result{}, fmt.Errorf("sushy-emulator not found. Ensure sushy-tools is installed")
		}
		return Result{}, fmt.Errorf("start sushy-emulator: %w", err)
	}
	if err := m.Sleep(ctx, 500*time.Millisecond); err != nil {
		return Result{}, err
	}
	if !proc.Running() {
		return Result{}, fmt.Errorf("sushy-emulator failed to start: %s", proc.Stderr())
	}
	return Result{
		Started:    true,
		ConfigPath: configPath,
		AuthPath:   authPath,
		CertPath:   certPath,
		KeyPath:    keyPath,
		Process:    proc,
	}, nil
}

func applyRequestDefaults(req *Request) {
	if req.User == "" {
		req.User = "admin"
	}
	if req.Password == "" {
		req.Password = "password"
	}
	if req.Port == 0 {
		req.Port = 8443
	}
	if req.LibvirtURI == "" {
		req.LibvirtURI = defaultLibvirtURI
	}
}

func writeAuthFile(configDir string, user string, password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", fmt.Errorf("hash Redfish password: %w", err)
	}
	authPath := filepath.Join(configDir, "htpasswd")
	if err := os.WriteFile(authPath, []byte(user+":"+string(hash)+"\n"), 0o600); err != nil {
		return "", fmt.Errorf("write Redfish auth file: %w", err)
	}
	return authPath, nil
}

func writeConfig(configDir string, req Request, certPath string, keyPath string, authPath string) (string, error) {
	configPath := filepath.Join(configDir, "sushy.conf")
	content := fmt.Sprintf(
		"SUSHY_EMULATOR_LIBVIRT_URI = %s\n"+
			"SUSHY_EMULATOR_LISTEN_IP = \"0.0.0.0\"\n"+
			"SUSHY_EMULATOR_LISTEN_PORT = %d\n"+
			"SUSHY_EMULATOR_SSL_CERT = %s\n"+
			"SUSHY_EMULATOR_SSL_KEY = %s\n"+
			"SUSHY_EMULATOR_AUTH_FILE = %s\n",
		strconv.Quote(req.LibvirtURI),
		req.Port,
		strconv.Quote(certPath),
		strconv.Quote(keyPath),
		strconv.Quote(authPath),
	)
	if err := os.WriteFile(configPath, []byte(content), 0o644); err != nil {
		return "", fmt.Errorf("write sushy config: %w", err)
	}
	return configPath, nil
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
