package guestexec

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/munenick/docker-vm-runner/internal/process"
)

func TestParseArgsUsesShellForSingleCommandString(t *testing.T) {
	inv, err := ParseArgs([]string{"--wait", "uname -a"})
	if err != nil {
		t.Fatalf("ParseArgs returned error: %v", err)
	}
	if !inv.Wait || inv.Path != "/bin/sh" || strings.Join(inv.Args, "\x00") != "-c\x00uname -a" {
		t.Fatalf("Invocation = %#v", inv)
	}
}

func TestParseArgsUsesArgvForm(t *testing.T) {
	inv, err := ParseArgs([]string{"uname", "-a"})
	if err != nil {
		t.Fatalf("ParseArgs returned error: %v", err)
	}
	if inv.Wait || inv.Path != "uname" || strings.Join(inv.Args, "\x00") != "-a" {
		t.Fatalf("Invocation = %#v", inv)
	}
}

func TestParseArgsRequiresCommand(t *testing.T) {
	if _, err := ParseArgs([]string{"--wait"}); err == nil {
		t.Fatal("expected error")
	}
}

func TestRunDiscoversDomainAndExecutesCommand(t *testing.T) {
	client := &fakeClient{domains: []string{"test-vm"}}
	client.responses = []response{
		{raw: rawJSON(t, `{"pid":42}`)},
		{raw: rawJSON(t, `{"exited":true,"exitcode":7,"out-data":"`+b64("stdout\n")+`","err-data":"`+b64("stderr\n")+`"}`)},
	}
	executor := NewExecutor(client)
	executor.Sleep = func(context.Context, time.Duration) error { return nil }

	result, err := executor.Run(context.Background(), Invocation{Path: "echo", Args: []string{"hello"}})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if result.ExitCode != 7 || string(result.Stdout) != "stdout\n" || string(result.Stderr) != "stderr\n" {
		t.Fatalf("Result = %#v", result)
	}
	if len(client.commands) != 2 {
		t.Fatalf("commands = %#v", client.commands)
	}
	if client.commands[0].Execute != "guest-exec" {
		t.Fatalf("first command = %#v", client.commands[0])
	}
	args := client.commands[0].Arguments.(map[string]any)
	if args["path"] != "echo" || args["capture-output"] != true {
		t.Fatalf("guest-exec args = %#v", args)
	}
	if client.commands[1].Execute != "guest-exec-status" {
		t.Fatalf("second command = %#v", client.commands[1])
	}
}

func TestRunWaitsForAgentWhenRequested(t *testing.T) {
	client := &fakeClient{domains: []string{"test-vm"}}
	client.responses = []response{
		{err: ErrAgentNotConnected},
		{raw: rawJSON(t, `{}`)},
		{raw: rawJSON(t, `{"pid":1}`)},
		{raw: rawJSON(t, `{"exited":true,"exitcode":0}`)},
	}
	sleepCalls := 0
	executor := NewExecutor(client)
	executor.Sleep = func(context.Context, time.Duration) error {
		sleepCalls++
		return nil
	}

	_, err := executor.Run(context.Background(), Invocation{Wait: true, Path: "true"})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if sleepCalls != 1 {
		t.Fatalf("sleepCalls = %d", sleepCalls)
	}
	if client.commands[0].Execute != "guest-ping" || client.commands[1].Execute != "guest-ping" {
		t.Fatalf("commands = %#v", client.commands)
	}
}

func TestRunErrorsWhenNoDomain(t *testing.T) {
	executor := NewExecutor(&fakeClient{})

	_, err := executor.Run(context.Background(), Invocation{Path: "true"})
	if err == nil || !strings.Contains(err.Error(), "no running VM found") {
		t.Fatalf("err = %v", err)
	}
}

func TestRunErrorsWhenMultipleDomains(t *testing.T) {
	executor := NewExecutor(&fakeClient{domains: []string{"a", "b"}})

	_, err := executor.Run(context.Background(), Invocation{Path: "true"})
	if err == nil || !strings.Contains(err.Error(), "multiple running VMs found") {
		t.Fatalf("err = %v", err)
	}
}

func TestRunReturnsCommandTimeout(t *testing.T) {
	client := &fakeClient{domains: []string{"test-vm"}}
	client.responses = []response{
		{raw: rawJSON(t, `{"pid":1}`)},
		{raw: rawJSON(t, `{"exited":false}`)},
	}
	executor := NewExecutor(client)
	executor.PollTimeout = time.Nanosecond
	executor.Sleep = func(context.Context, time.Duration) error { return nil }

	_, err := executor.Run(context.Background(), Invocation{Path: "sleep"})
	if !errors.Is(err, ErrCommandTimeout) {
		t.Fatalf("err = %v", err)
	}
}

func TestRunReturnsDisconnectedAgent(t *testing.T) {
	client := &fakeClient{domains: []string{"test-vm"}}
	client.responses = []response{{err: ErrAgentNotConnected}}
	executor := NewExecutor(client)

	_, err := executor.Run(context.Background(), Invocation{Path: "true"})
	if !errors.Is(err, ErrAgentNotConnected) {
		t.Fatalf("err = %v", err)
	}
}

func TestVirshClientBuildsCommands(t *testing.T) {
	runner := &fakeRunner{}
	runner.results = []process.Result{
		{Stdout: "vm-a\n\n"},
		{Stdout: `{"return":{"pid":9}}`},
	}
	client := NewVirshClient(runner)

	domains, err := client.ListRunningDomains(context.Background())
	if err != nil {
		t.Fatalf("ListRunningDomains returned error: %v", err)
	}
	if len(domains) != 1 || domains[0] != "vm-a" {
		t.Fatalf("domains = %#v", domains)
	}
	raw, err := client.Execute(context.Background(), "vm-a", Command{Execute: "guest-ping"})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if string(raw) != `{"pid":9}` {
		t.Fatalf("raw = %s", raw)
	}
	if runner.commands[1].Name != "virsh" || runner.commands[1].Args[0] != "qemu-agent-command" {
		t.Fatalf("command = %#v", runner.commands[1])
	}
}

func TestVirshClientMapsNotConnectedError(t *testing.T) {
	runner := &fakeRunner{err: &process.ExitError{Name: "virsh", ExitCode: 1, Stderr: "error: agent is not connected"}}
	client := NewVirshClient(runner)

	_, err := client.Execute(context.Background(), "vm-a", Command{Execute: "guest-ping"})
	if !errors.Is(err, ErrAgentNotConnected) {
		t.Fatalf("err = %v", err)
	}
}

type response struct {
	raw json.RawMessage
	err error
}

type fakeClient struct {
	domains   []string
	responses []response
	commands  []Command
}

func (c *fakeClient) ListRunningDomains(context.Context) ([]string, error) {
	return c.domains, nil
}

func (c *fakeClient) Execute(_ context.Context, _ string, command Command) (json.RawMessage, error) {
	c.commands = append(c.commands, command)
	if len(c.responses) == 0 {
		return nil, errors.New("unexpected command")
	}
	next := c.responses[0]
	c.responses = c.responses[1:]
	return next.raw, next.err
}

type fakeRunner struct {
	commands []process.Command
	results  []process.Result
	err      error
}

func (r *fakeRunner) Run(_ context.Context, cmd process.Command) (process.Result, error) {
	r.commands = append(r.commands, cmd)
	if r.err != nil {
		return process.Result{}, r.err
	}
	if len(r.results) == 0 {
		return process.Result{}, nil
	}
	result := r.results[0]
	r.results = r.results[1:]
	return result, nil
}

func rawJSON(t *testing.T, value string) json.RawMessage {
	t.Helper()
	return json.RawMessage(value)
}

func b64(value string) string {
	return base64.StdEncoding.EncodeToString([]byte(value))
}
