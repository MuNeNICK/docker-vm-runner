package process

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
)

type Command struct {
	Name   string
	Args   []string
	Dir    string
	Env    []string
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
}

type Result struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

type ExitError struct {
	Name     string
	Args     []string
	ExitCode int
	Stderr   string
}

func (e *ExitError) Error() string {
	return fmt.Sprintf("%s exited with code %d", e.Name, e.ExitCode)
}

type CommandRunner struct{}

func NewCommandRunner() *CommandRunner {
	return &CommandRunner{}
}

func (r *CommandRunner) Run(ctx context.Context, command Command) (Result, error) {
	cmd := exec.CommandContext(ctx, command.Name, command.Args...)
	if command.Dir != "" {
		cmd.Dir = command.Dir
	}
	if len(command.Env) > 0 {
		cmd.Env = append(os.Environ(), command.Env...)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdin = command.Stdin
	if command.Stdout != nil {
		cmd.Stdout = io.MultiWriter(command.Stdout, &stdout)
	} else {
		cmd.Stdout = &stdout
	}
	if command.Stderr != nil {
		cmd.Stderr = io.MultiWriter(command.Stderr, &stderr)
	} else {
		cmd.Stderr = &stderr
	}

	err := cmd.Run()
	result := Result{
		Stdout: stdout.String(),
		Stderr: stderr.String(),
	}
	if cmd.ProcessState != nil {
		result.ExitCode = cmd.ProcessState.ExitCode()
	}
	if err == nil {
		return result, nil
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return result, fmt.Errorf("%w: %v", ctxErr, err)
	}
	var execExitErr *exec.ExitError
	if errors.As(err, &execExitErr) {
		result.ExitCode = execExitErr.ExitCode()
		return result, &ExitError{
			Name:     command.Name,
			Args:     command.Args,
			ExitCode: execExitErr.ExitCode(),
			Stderr:   result.Stderr,
		}
	}
	return result, fmt.Errorf("run %s: %w", command.Name, err)
}
