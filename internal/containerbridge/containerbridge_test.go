package containerbridge

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/munenick/docker-vm-runner/internal/process"
)

type fakeRunner struct {
	command process.Command
	err     error
}

func (r *fakeRunner) Run(_ context.Context, command process.Command) (process.Result, error) {
	r.command = command
	return process.Result{}, r.err
}

func TestCommand(t *testing.T) {
	command, err := Command(Request{Interface: "eth1", Bridge: "dvr1abcd"})
	if err != nil {
		t.Fatalf("Command returned error: %v", err)
	}
	if command.Name != "sh" {
		t.Fatalf("Name = %q", command.Name)
	}
	got := strings.Join(command.Args, " ")
	for _, want := range []string{"eth1", "dvr1abcd", "ip link add name", "ip link set dev \"$iface\" master \"$bridge\""} {
		if !strings.Contains(got, want) {
			t.Fatalf("command args missing %q:\n%s", want, got)
		}
	}
}

func TestCommandRejectsInvalidNames(t *testing.T) {
	tests := []Request{
		{Interface: "eth1/../../x", Bridge: "dvr1abcd"},
		{Interface: "eth1", Bridge: "bridge-name-that-is-too-long"},
		{Interface: "eth1", Bridge: "eth1"},
	}
	for _, tt := range tests {
		if _, err := Command(tt); err == nil {
			t.Fatalf("expected error for %#v", tt)
		}
	}
}

func TestEnsureRunsCommand(t *testing.T) {
	runner := &fakeRunner{}
	if err := Ensure(context.Background(), runner, Request{Interface: "eth1", Bridge: "dvr1abcd"}); err != nil {
		t.Fatalf("Ensure returned error: %v", err)
	}
	if runner.command.Name != "sh" {
		t.Fatalf("command = %#v", runner.command)
	}
}

func TestEnsureWrapsRunnerError(t *testing.T) {
	runner := &fakeRunner{err: errors.New("boom")}
	err := Ensure(context.Background(), runner, Request{Interface: "eth1", Bridge: "dvr1abcd"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "prepare container interface eth1 on bridge dvr1abcd") {
		t.Fatalf("unexpected error: %v", err)
	}
}
