package process

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestRunnerCapturesOutput(t *testing.T) {
	runner := NewCommandRunner()
	result, err := runner.Run(context.Background(), Command{
		Name: "sh",
		Args: []string{"-c", "printf out; printf err >&2"},
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if result.Stdout != "out" {
		t.Fatalf("stdout = %q", result.Stdout)
	}
	if result.Stderr != "err" {
		t.Fatalf("stderr = %q", result.Stderr)
	}
	if result.ExitCode != 0 {
		t.Fatalf("exit code = %d", result.ExitCode)
	}
}

func TestRunnerReportsExitError(t *testing.T) {
	runner := NewCommandRunner()
	result, err := runner.Run(context.Background(), Command{
		Name: "sh",
		Args: []string{"-c", "printf boom >&2; exit 7"},
	})
	if err == nil {
		t.Fatal("expected error")
	}
	var exitErr *ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("error type = %T, want *ExitError", err)
	}
	if exitErr.ExitCode != 7 {
		t.Fatalf("exit code = %d", exitErr.ExitCode)
	}
	if result.ExitCode != 7 {
		t.Fatalf("result exit code = %d", result.ExitCode)
	}
	if result.Stderr != "boom" {
		t.Fatalf("stderr = %q", result.Stderr)
	}
}

func TestRunnerContextCancellation(t *testing.T) {
	runner := NewCommandRunner()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	_, err := runner.Run(ctx, Command{
		Name: "sh",
		Args: []string{"-c", "sleep 1"},
	})
	if err == nil {
		t.Fatal("expected context cancellation error")
	}
	if !errors.Is(err, context.DeadlineExceeded) && !strings.Contains(err.Error(), "signal: killed") {
		t.Fatalf("unexpected cancellation error: %v", err)
	}
}

func TestRunnerMissingCommand(t *testing.T) {
	runner := NewCommandRunner()
	_, err := runner.Run(context.Background(), Command{Name: "definitely-not-a-real-command"})
	if err == nil {
		t.Fatal("expected missing command error")
	}
}
