package console

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"syscall"

	"github.com/munenick/docker-vm-runner/internal/process"
)

const defaultLibvirtURI = "qemu:///system"

type Process interface {
	Wait() (int, error)
	Signal(os.Signal) error
	Terminate() error
}

type Runner struct {
	LibvirtURI string
	Start      func(context.Context, process.Command) (Process, error)
	Notify     func(chan<- os.Signal, ...os.Signal)
	StopNotify func(chan<- os.Signal)
}

func NewRunner() *Runner {
	return &Runner{
		LibvirtURI: defaultLibvirtURI,
		Start:      startProcess,
		Notify:     signal.Notify,
		StopNotify: signal.Stop,
	}
}

func Command(libvirtURI string, vmName string) process.Command {
	if libvirtURI == "" {
		libvirtURI = defaultLibvirtURI
	}
	return process.Command{Name: "virsh", Args: []string{"-c", libvirtURI, "console", vmName}}
}

func (r *Runner) Run(ctx context.Context, vmName string) (int, error) {
	r.applyDefaults()
	proc, err := r.Start(ctx, Command(r.LibvirtURI, vmName))
	if err != nil {
		return 0, fmt.Errorf("start console: %w", err)
	}
	signals := make(chan os.Signal, 1)
	r.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer r.StopNotify(signals)

	waitCh := make(chan waitResult, 1)
	go func() {
		code, err := proc.Wait()
		waitCh <- waitResult{code: code, err: err}
	}()

	for {
		select {
		case <-ctx.Done():
			_ = proc.Terminate()
			result := <-waitCh
			if result.err != nil {
				return result.code, result.err
			}
			return result.code, ctx.Err()
		case sig := <-signals:
			if sig == os.Interrupt {
				_ = proc.Signal(os.Interrupt)
				continue
			}
			_ = proc.Terminate()
		case result := <-waitCh:
			return result.code, result.err
		}
	}
}

func (r *Runner) applyDefaults() {
	if r.LibvirtURI == "" {
		r.LibvirtURI = defaultLibvirtURI
	}
	if r.Start == nil {
		r.Start = startProcess
	}
	if r.Notify == nil {
		r.Notify = signal.Notify
	}
	if r.StopNotify == nil {
		r.StopNotify = signal.Stop
	}
}

type waitResult struct {
	code int
	err  error
}

type osProcess struct {
	cmd *exec.Cmd
}

func startProcess(ctx context.Context, command process.Command) (Process, error) {
	cmd := exec.CommandContext(ctx, command.Name, command.Args...)
	if command.Dir != "" {
		cmd.Dir = command.Dir
	}
	if len(command.Env) > 0 {
		cmd.Env = append(os.Environ(), command.Env...)
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &osProcess{cmd: cmd}, nil
}

func (p *osProcess) Wait() (int, error) {
	err := p.cmd.Wait()
	if p.cmd.ProcessState != nil {
		return p.cmd.ProcessState.ExitCode(), err
	}
	return 0, err
}

func (p *osProcess) Signal(sig os.Signal) error {
	if p.cmd.Process == nil {
		return nil
	}
	return p.cmd.Process.Signal(sig)
}

func (p *osProcess) Terminate() error {
	if p.cmd.Process == nil {
		return nil
	}
	return p.cmd.Process.Signal(syscall.SIGTERM)
}
