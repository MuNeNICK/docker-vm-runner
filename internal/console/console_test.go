package console

import (
	"context"
	"os"
	"strings"
	"syscall"
	"testing"

	"github.com/munenick/docker-vm-runner/internal/process"
)

func TestCommand(t *testing.T) {
	cmd := Command("qemu:///system", "vm1")
	want := []string{"-c", "qemu:///system", "console", "vm1"}
	if cmd.Name != "virsh" || strings.Join(cmd.Args, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("command = %#v", cmd)
	}
}

func TestRunWaitsForConsoleExit(t *testing.T) {
	proc := &fakeProcess{waitCode: 3, waitReady: make(chan struct{})}
	close(proc.waitReady)
	runner := NewRunner()
	runner.Start = func(_ context.Context, cmd process.Command) (Process, error) {
		if cmd.Name != "virsh" {
			t.Fatalf("command = %#v", cmd)
		}
		return proc, nil
	}
	runner.Notify = func(chan<- os.Signal, ...os.Signal) {}
	runner.StopNotify = func(chan<- os.Signal) {}

	code, err := runner.Run(context.Background(), "vm1")
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if code != 3 {
		t.Fatalf("code = %d", code)
	}
}

func TestRunForwardsInterruptToConsole(t *testing.T) {
	proc := &fakeProcess{waitCode: 0, waitReady: make(chan struct{})}
	signals := make(chan os.Signal, 1)
	runner := NewRunner()
	runner.Start = func(context.Context, process.Command) (Process, error) { return proc, nil }
	runner.Notify = func(ch chan<- os.Signal, _ ...os.Signal) {
		go func() {
			ch <- os.Interrupt
			close(proc.waitReady)
		}()
	}
	runner.StopNotify = func(chan<- os.Signal) { close(signals) }

	code, err := runner.Run(context.Background(), "vm1")
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if code != 0 {
		t.Fatalf("code = %d", code)
	}
	if len(proc.signals) != 1 || proc.signals[0] != os.Interrupt {
		t.Fatalf("signals = %#v", proc.signals)
	}
}

func TestRunTerminatesOnContextCancel(t *testing.T) {
	proc := &fakeProcess{waitCode: -1, waitReady: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	runner := NewRunner()
	runner.Start = func(context.Context, process.Command) (Process, error) {
		cancel()
		close(proc.waitReady)
		return proc, nil
	}
	runner.Notify = func(chan<- os.Signal, ...os.Signal) {}
	runner.StopNotify = func(chan<- os.Signal) {}

	_, err := runner.Run(ctx, "vm1")
	if err == nil {
		t.Fatal("expected context error")
	}
	if proc.terminated != 1 {
		t.Fatalf("terminated = %d", proc.terminated)
	}
}

func TestRunTerminatesOnSigterm(t *testing.T) {
	proc := &fakeProcess{waitCode: 0, waitReady: make(chan struct{})}
	runner := NewRunner()
	runner.Start = func(context.Context, process.Command) (Process, error) { return proc, nil }
	runner.Notify = func(ch chan<- os.Signal, _ ...os.Signal) {
		go func() {
			ch <- syscall.SIGTERM
			close(proc.waitReady)
		}()
	}
	runner.StopNotify = func(chan<- os.Signal) {}

	if _, err := runner.Run(context.Background(), "vm1"); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if proc.terminated != 1 {
		t.Fatalf("terminated = %d", proc.terminated)
	}
}

type fakeProcess struct {
	waitCode   int
	waitReady  chan struct{}
	signals    []os.Signal
	terminated int
}

func (p *fakeProcess) Wait() (int, error) {
	<-p.waitReady
	return p.waitCode, nil
}

func (p *fakeProcess) Signal(sig os.Signal) error {
	p.signals = append(p.signals, sig)
	return nil
}

func (p *fakeProcess) Terminate() error {
	p.terminated++
	return nil
}
