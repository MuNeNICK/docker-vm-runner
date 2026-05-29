package guestexec

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/munenick/docker-vm-runner/internal/process"
)

const (
	defaultPollInterval      = 300 * time.Millisecond
	defaultPollTimeout       = 300 * time.Second
	defaultAgentWaitTimeout  = 60 * time.Second
	defaultAgentWaitInterval = 2 * time.Second
)

var (
	ErrAgentNotConnected = errors.New("guest agent is not connected")
	ErrAgentWaitTimeout  = errors.New("guest agent wait timeout")
	ErrCommandTimeout    = errors.New("guest command timeout")
)

type Invocation struct {
	Wait bool
	Path string
	Args []string
}

type Result struct {
	Stdout   []byte
	Stderr   []byte
	ExitCode int
}

type Command struct {
	Execute   string `json:"execute"`
	Arguments any    `json:"arguments,omitempty"`
}

type Client interface {
	ListRunningDomains(context.Context) ([]string, error)
	Execute(context.Context, string, Command) (json.RawMessage, error)
}

type Executor struct {
	Client            Client
	Sleep             func(context.Context, time.Duration) error
	PollInterval      time.Duration
	PollTimeout       time.Duration
	AgentWaitTimeout  time.Duration
	AgentWaitInterval time.Duration
	TempPath          func(int, string) string
}

func NewExecutor(client ...Client) *Executor {
	var selected Client
	if len(client) > 0 {
		selected = client[0]
	} else {
		selected = NewVirshClient(process.NewCommandRunner())
	}
	return &Executor{
		Client:            selected,
		Sleep:             sleepContext,
		PollInterval:      defaultPollInterval,
		PollTimeout:       defaultPollTimeout,
		AgentWaitTimeout:  defaultAgentWaitTimeout,
		AgentWaitInterval: defaultAgentWaitInterval,
	}
}

func ParseArgs(args []string) (Invocation, error) {
	var inv Invocation
	if len(args) > 0 && args[0] == "--wait" {
		inv.Wait = true
		args = args[1:]
	}
	if len(args) == 0 {
		return Invocation{}, fmt.Errorf("missing command")
	}
	if len(args) == 1 && strings.Contains(args[0], " ") {
		inv.Path = "/bin/sh"
		inv.Args = []string{"-c", args[0]}
		return inv, nil
	}
	inv.Path = args[0]
	inv.Args = append([]string(nil), args[1:]...)
	return inv, nil
}

func (e *Executor) Run(ctx context.Context, inv Invocation) (Result, error) {
	e.applyDefaults()
	domain, err := e.discoverDomain(ctx)
	if err != nil {
		return Result{}, err
	}
	return e.RunOnDomain(ctx, domain, inv)
}

func (e *Executor) RunStreaming(ctx context.Context, inv Invocation, stdout io.Writer, stderr io.Writer) (Result, error) {
	e.applyDefaults()
	domain, err := e.discoverDomain(ctx)
	if err != nil {
		return Result{}, err
	}
	return e.RunOnDomainStreaming(ctx, domain, inv, stdout, stderr)
}

func (e *Executor) RunOnDomain(ctx context.Context, domain string, inv Invocation) (Result, error) {
	e.applyDefaults()
	if inv.Wait {
		if err := e.waitForAgent(ctx, domain); err != nil {
			return Result{}, err
		}
	}
	pid, err := e.startCommand(ctx, domain, inv)
	if err != nil {
		return Result{}, err
	}
	return e.waitForCommand(ctx, domain, pid)
}

func (e *Executor) RunOnDomainStreaming(ctx context.Context, domain string, inv Invocation, stdout io.Writer, stderr io.Writer) (Result, error) {
	e.applyDefaults()
	if inv.Wait {
		if err := e.waitForAgent(ctx, domain); err != nil {
			return Result{}, err
		}
	}
	stdoutPath := e.TempPath(os.Getpid(), "stdout")
	stderrPath := e.TempPath(os.Getpid(), "stderr")
	if err := e.createGuestFile(ctx, domain, stdoutPath); err != nil {
		return Result{}, err
	}
	if err := e.createGuestFile(ctx, domain, stderrPath); err != nil {
		_ = e.removeGuestFiles(context.WithoutCancel(ctx), domain, stdoutPath)
		return Result{}, err
	}
	defer func() { _ = e.removeGuestFiles(context.WithoutCancel(ctx), domain, stdoutPath, stderrPath) }()
	stdoutHandle, err := e.openGuestFile(ctx, domain, stdoutPath, "r")
	if err != nil {
		_ = e.removeGuestFiles(context.WithoutCancel(ctx), domain, stdoutPath, stderrPath)
		return Result{}, err
	}
	defer func() { _ = e.closeGuestFile(context.WithoutCancel(ctx), domain, stdoutHandle) }()
	stderrHandle, err := e.openGuestFile(ctx, domain, stderrPath, "r")
	if err != nil {
		_ = e.removeGuestFiles(context.WithoutCancel(ctx), domain, stdoutPath, stderrPath)
		return Result{}, err
	}
	defer func() { _ = e.closeGuestFile(context.WithoutCancel(ctx), domain, stderrHandle) }()

	pid, err := e.startStreamingCommand(ctx, domain, inv, stdoutPath, stderrPath)
	if err != nil {
		return Result{}, err
	}
	return e.waitForStreamingCommand(ctx, domain, pid, stdoutHandle, stderrHandle, stdout, stderr)
}

func (e *Executor) applyDefaults() {
	if e.Sleep == nil {
		e.Sleep = sleepContext
	}
	if e.PollInterval == 0 {
		e.PollInterval = defaultPollInterval
	}
	if e.PollTimeout == 0 {
		e.PollTimeout = defaultPollTimeout
	}
	if e.AgentWaitTimeout == 0 {
		e.AgentWaitTimeout = defaultAgentWaitTimeout
	}
	if e.AgentWaitInterval == 0 {
		e.AgentWaitInterval = defaultAgentWaitInterval
	}
	if e.TempPath == nil {
		e.TempPath = defaultTempPath
	}
}

func (e *Executor) discoverDomain(ctx context.Context) (string, error) {
	names, err := e.Client.ListRunningDomains(ctx)
	if err != nil {
		return "", fmt.Errorf("list running domains: %w", err)
	}
	filtered := make([]string, 0, len(names))
	for _, name := range names {
		if strings.TrimSpace(name) != "" {
			filtered = append(filtered, strings.TrimSpace(name))
		}
	}
	if len(filtered) == 0 {
		return "", fmt.Errorf("no running VM found")
	}
	if len(filtered) > 1 {
		return "", fmt.Errorf("multiple running VMs found")
	}
	return filtered[0], nil
}

func (e *Executor) waitForAgent(ctx context.Context, domain string) error {
	deadline := time.Now().Add(e.AgentWaitTimeout)
	for {
		_, err := e.Client.Execute(ctx, domain, Command{Execute: "guest-ping"})
		if err == nil {
			return nil
		}
		if !errors.Is(err, ErrAgentNotConnected) {
			return err
		}
		if time.Now().After(deadline) {
			return ErrAgentWaitTimeout
		}
		if err := e.Sleep(ctx, e.AgentWaitInterval); err != nil {
			return err
		}
	}
}

func (e *Executor) startCommand(ctx context.Context, domain string, inv Invocation) (int, error) {
	raw, err := e.Client.Execute(ctx, domain, Command{
		Execute: "guest-exec",
		Arguments: map[string]any{
			"path":           inv.Path,
			"arg":            inv.Args,
			"capture-output": true,
		},
	})
	if err != nil {
		return 0, err
	}
	var resp struct {
		PID int `json:"pid"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return 0, fmt.Errorf("decode guest-exec response: %w", err)
	}
	if resp.PID == 0 {
		return 0, fmt.Errorf("guest-exec did not return a PID")
	}
	return resp.PID, nil
}

func (e *Executor) startStreamingCommand(ctx context.Context, domain string, inv Invocation, stdoutPath string, stderrPath string) (int, error) {
	args := []string{"-c", streamingScript, "docker-vm-runner-guest-exec", stdoutPath, stderrPath, inv.Path}
	args = append(args, inv.Args...)
	raw, err := e.Client.Execute(ctx, domain, Command{
		Execute: "guest-exec",
		Arguments: map[string]any{
			"path":           "/bin/sh",
			"arg":            args,
			"capture-output": false,
		},
	})
	if err != nil {
		return 0, err
	}
	var resp struct {
		PID int `json:"pid"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return 0, fmt.Errorf("decode guest-exec response: %w", err)
	}
	if resp.PID == 0 {
		return 0, fmt.Errorf("guest-exec did not return a PID")
	}
	return resp.PID, nil
}

func (e *Executor) waitForCommand(ctx context.Context, domain string, pid int) (Result, error) {
	deadline := time.Now().Add(e.PollTimeout)
	for {
		status, err := e.commandStatus(ctx, domain, pid)
		if err != nil {
			return Result{}, err
		}
		if status.Exited {
			stdout, err := decodeBase64Field("out-data", status.OutData)
			if err != nil {
				return Result{}, err
			}
			stderr, err := decodeBase64Field("err-data", status.ErrData)
			if err != nil {
				return Result{}, err
			}
			return Result{Stdout: stdout, Stderr: stderr, ExitCode: status.ExitCode}, nil
		}
		if time.Now().After(deadline) {
			return Result{}, ErrCommandTimeout
		}
		if err := e.Sleep(ctx, e.PollInterval); err != nil {
			return Result{}, err
		}
	}
}

func (e *Executor) waitForStreamingCommand(ctx context.Context, domain string, pid int, stdoutHandle int, stderrHandle int, stdout io.Writer, stderr io.Writer) (Result, error) {
	deadline := time.Now().Add(e.PollTimeout)
	var result Result
	for {
		if err := e.readAvailable(ctx, domain, stdoutHandle, &result.Stdout, stdout); err != nil {
			return Result{}, err
		}
		if err := e.readAvailable(ctx, domain, stderrHandle, &result.Stderr, stderr); err != nil {
			return Result{}, err
		}
		status, err := e.commandStatus(ctx, domain, pid)
		if err != nil {
			return Result{}, err
		}
		if status.Exited {
			if err := e.readAvailable(ctx, domain, stdoutHandle, &result.Stdout, stdout); err != nil {
				return Result{}, err
			}
			if err := e.readAvailable(ctx, domain, stderrHandle, &result.Stderr, stderr); err != nil {
				return Result{}, err
			}
			result.ExitCode = status.ExitCode
			return result, nil
		}
		if time.Now().After(deadline) {
			return Result{}, ErrCommandTimeout
		}
		if err := e.Sleep(ctx, e.PollInterval); err != nil {
			return Result{}, err
		}
	}
}

func (e *Executor) commandStatus(ctx context.Context, domain string, pid int) (guestExecStatus, error) {
	raw, err := e.Client.Execute(ctx, domain, Command{
		Execute:   "guest-exec-status",
		Arguments: map[string]any{"pid": pid},
	})
	if err != nil {
		if errors.Is(err, ErrAgentNotConnected) {
			return guestExecStatus{}, fmt.Errorf("guest agent disconnected while waiting for command result: %w", err)
		}
		return guestExecStatus{}, err
	}
	var status guestExecStatus
	if err := json.Unmarshal(raw, &status); err != nil {
		return guestExecStatus{}, fmt.Errorf("decode guest-exec-status response: %w", err)
	}
	return status, nil
}

type guestExecStatus struct {
	Exited   bool   `json:"exited"`
	ExitCode int    `json:"exitcode"`
	OutData  string `json:"out-data"`
	ErrData  string `json:"err-data"`
}

func (e *Executor) createGuestFile(ctx context.Context, domain string, path string) error {
	handle, err := e.openGuestFile(ctx, domain, path, "w+")
	if err != nil {
		return err
	}
	return e.closeGuestFile(ctx, domain, handle)
}

func (e *Executor) openGuestFile(ctx context.Context, domain string, path string, mode string) (int, error) {
	raw, err := e.Client.Execute(ctx, domain, Command{
		Execute: "guest-file-open",
		Arguments: map[string]any{
			"path": path,
			"mode": mode,
		},
	})
	if err != nil {
		return 0, err
	}
	var handle int
	if err := json.Unmarshal(raw, &handle); err != nil {
		return 0, fmt.Errorf("decode guest-file-open response: %w", err)
	}
	if handle == 0 {
		return 0, fmt.Errorf("guest-file-open did not return a handle")
	}
	return handle, nil
}

func (e *Executor) closeGuestFile(ctx context.Context, domain string, handle int) error {
	_, err := e.Client.Execute(ctx, domain, Command{
		Execute:   "guest-file-close",
		Arguments: map[string]any{"handle": handle},
	})
	if err != nil {
		return fmt.Errorf("close guest file handle %d: %w", handle, err)
	}
	return nil
}

func (e *Executor) readAvailable(ctx context.Context, domain string, handle int, captured *[]byte, writer io.Writer) error {
	for {
		chunk, eof, err := e.readGuestFile(ctx, domain, handle)
		if err != nil {
			return err
		}
		if len(chunk) > 0 {
			*captured = append(*captured, chunk...)
			if writer != nil {
				if _, err := writer.Write(chunk); err != nil {
					return err
				}
			}
		}
		if eof || len(chunk) == 0 {
			return nil
		}
	}
}

func (e *Executor) readGuestFile(ctx context.Context, domain string, handle int) ([]byte, bool, error) {
	raw, err := e.Client.Execute(ctx, domain, Command{
		Execute: "guest-file-read",
		Arguments: map[string]any{
			"handle": handle,
			"count":  65536,
		},
	})
	if err != nil {
		return nil, false, err
	}
	var resp struct {
		Count int    `json:"count"`
		Buf   string `json:"buf-b64"`
		EOF   bool   `json:"eof"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, false, fmt.Errorf("decode guest-file-read response: %w", err)
	}
	chunk, err := decodeBase64Field("buf-b64", resp.Buf)
	if err != nil {
		return nil, false, err
	}
	return chunk, resp.EOF, nil
}

func (e *Executor) removeGuestFiles(ctx context.Context, domain string, paths ...string) error {
	args := append([]string{"-f"}, paths...)
	_, err := e.Client.Execute(ctx, domain, Command{
		Execute: "guest-exec",
		Arguments: map[string]any{
			"path":           "rm",
			"arg":            args,
			"capture-output": false,
		},
	})
	if err != nil {
		return fmt.Errorf("remove guest output files: %w", err)
	}
	return nil
}

func decodeBase64Field(name string, value string) ([]byte, error) {
	if value == "" {
		return nil, nil
	}
	decoded, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		return nil, fmt.Errorf("decode %s: %w", name, err)
	}
	return decoded, nil
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

func defaultTempPath(pid int, stream string) string {
	var token [8]byte
	if _, err := rand.Read(token[:]); err == nil {
		return fmt.Sprintf("/tmp/docker-vm-runner-guest-exec-%d-%s-%s", pid, hex.EncodeToString(token[:]), stream)
	}
	return fmt.Sprintf("/tmp/docker-vm-runner-guest-exec-%d-%d-%s", pid, time.Now().UnixNano(), stream)
}

const streamingScript = `out=$1
err=$2
shift 2
"$@" >"$out" 2>"$err"`

func Main(ctx context.Context, args []string, stdout io.Writer, stderr io.Writer) int {
	inv, err := ParseArgs(args)
	if err != nil {
		fmt.Fprint(stderr, usage())
		return 1
	}
	result, err := NewExecutor().RunStreaming(ctx, inv, stdout, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "Error: %v\n", err)
		if errors.Is(err, ErrAgentNotConnected) || errors.Is(err, ErrAgentWaitTimeout) {
			return 127
		}
		return 1
	}
	return result.ExitCode
}

func usage() string {
	return "Usage: guest-exec [--wait] <command> [args...]\n\nOptions:\n  --wait  Wait for guest agent to become available\n"
}

type CommandRunner interface {
	Run(context.Context, process.Command) (process.Result, error)
}

type VirshClient struct {
	Runner     CommandRunner
	LibvirtURI string
}

func NewVirshClient(runner CommandRunner) *VirshClient {
	return &VirshClient{Runner: runner}
}

func (c *VirshClient) ListRunningDomains(ctx context.Context) ([]string, error) {
	args := c.virshArgs("list", "--name", "--state-running")
	result, err := c.Runner.Run(ctx, process.Command{Name: "virsh", Args: args})
	if err != nil {
		return nil, err
	}
	var names []string
	for _, line := range strings.Split(result.Stdout, "\n") {
		if strings.TrimSpace(line) != "" {
			names = append(names, strings.TrimSpace(line))
		}
	}
	return names, nil
}

func (c *VirshClient) Execute(ctx context.Context, domain string, command Command) (json.RawMessage, error) {
	payload, err := json.Marshal(command)
	if err != nil {
		return nil, fmt.Errorf("encode qemu guest agent command: %w", err)
	}
	result, err := c.Runner.Run(ctx, process.Command{
		Name: "virsh",
		Args: c.virshArgs("qemu-agent-command", domain, string(payload)),
	})
	if err != nil {
		var exitErr *process.ExitError
		if errors.As(err, &exitErr) && strings.Contains(strings.ToLower(exitErr.Stderr), "not connected") {
			return nil, ErrAgentNotConnected
		}
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("virsh not found")
		}
		return nil, err
	}
	var envelope struct {
		Return json.RawMessage `json:"return"`
	}
	if err := json.Unmarshal([]byte(result.Stdout), &envelope); err != nil {
		return nil, fmt.Errorf("decode qemu guest agent response: %w", err)
	}
	return envelope.Return, nil
}

func (c *VirshClient) virshArgs(args ...string) []string {
	if strings.TrimSpace(c.LibvirtURI) == "" {
		return args
	}
	return append([]string{"-c", c.LibvirtURI}, args...)
}
