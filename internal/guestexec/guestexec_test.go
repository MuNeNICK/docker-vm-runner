package guestexec

import (
	"bytes"
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

func TestRunOnDomainExecutesKnownDomain(t *testing.T) {
	client := &fakeClient{}
	client.responses = []response{
		{raw: rawJSON(t, `{"pid":42}`)},
		{raw: rawJSON(t, `{"exited":true,"exitcode":0}`)},
	}
	executor := NewExecutor(client)
	executor.Sleep = func(context.Context, time.Duration) error { return nil }

	if _, err := executor.RunOnDomain(context.Background(), "vm1", Invocation{Path: "true"}); err != nil {
		t.Fatalf("RunOnDomain returned error: %v", err)
	}
	if len(client.domainsRequested) != 0 {
		t.Fatalf("ListRunningDomains was called: %#v", client.domainsRequested)
	}
}

func TestRunStreamingWritesOutputBeforeCommandExit(t *testing.T) {
	client := &fakeClient{domains: []string{"test-vm"}}
	client.responses = []response{
		{raw: rawJSON(t, `{"pid":10}`)},
		{raw: rawJSON(t, `{"exited":true,"exitcode":0}`)},
		{raw: rawJSON(t, `101`)},
		{raw: rawJSON(t, `{}`)},
		{raw: rawJSON(t, `102`)},
		{raw: rawJSON(t, `{}`)},
		{raw: rawJSON(t, `201`)},
		{raw: rawJSON(t, `202`)},
		{raw: rawJSON(t, `{"pid":42}`)},
		{raw: rawJSON(t, `{"count":6,"buf-b64":"`+b64("first\n")+`","eof":true}`)},
		{raw: rawJSON(t, `{"count":0,"eof":true}`)},
		{raw: rawJSON(t, `{"exited":false}`)},
		{raw: rawJSON(t, `{"count":7,"buf-b64":"`+b64("second\n")+`","eof":true}`)},
		{raw: rawJSON(t, `{"count":5,"buf-b64":"`+b64("warn\n")+`","eof":true}`)},
		{raw: rawJSON(t, `{"exited":true,"exitcode":3}`)},
		{raw: rawJSON(t, `{"count":0,"eof":true}`)},
		{raw: rawJSON(t, `{"count":0,"eof":true}`)},
		{raw: rawJSON(t, `{}`)},
		{raw: rawJSON(t, `{}`)},
		{raw: rawJSON(t, `{"pid":43}`)},
		{raw: rawJSON(t, `{"exited":true,"exitcode":0}`)},
	}
	var stdout, stderr bytes.Buffer
	executor := NewExecutor(client)
	executor.Sleep = func(context.Context, time.Duration) error {
		if got := stdout.String(); got != "first\n" {
			t.Fatalf("stdout before command exit = %q", got)
		}
		return nil
	}
	executor.TempDir = func(int) string { return "/tmp/docker-vm-runner-guest-exec-test" }

	result, err := executor.RunStreaming(context.Background(), Invocation{Path: "echo", Args: []string{"hello"}}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("RunStreaming returned error: %v", err)
	}
	if result.ExitCode != 3 {
		t.Fatalf("ExitCode = %d", result.ExitCode)
	}
	if stdout.String() != "first\nsecond\n" || string(result.Stdout) != stdout.String() {
		t.Fatalf("stdout = %q result = %q", stdout.String(), string(result.Stdout))
	}
	if stderr.String() != "warn\n" || string(result.Stderr) != stderr.String() {
		t.Fatalf("stderr = %q result = %q", stderr.String(), string(result.Stderr))
	}
	mkdirArgs := client.commands[0].Arguments.(map[string]any)
	if client.commands[0].Execute != "guest-exec" || mkdirArgs["path"] != "mkdir" {
		t.Fatalf("mkdir command = %#v", client.commands[0])
	}
	if got := strings.Join(mkdirArgs["arg"].([]string), " "); got != "-m 700 /tmp/docker-vm-runner-guest-exec-test" {
		t.Fatalf("mkdir args = %q", got)
	}
	execArgs := client.commands[8].Arguments.(map[string]any)
	if execArgs["path"] != "/bin/sh" || execArgs["capture-output"] != false {
		t.Fatalf("streaming guest-exec args = %#v", execArgs)
	}
	if got := strings.Join(execArgs["arg"].([]string), "\x00"); !strings.Contains(got, "\x00echo\x00hello") {
		t.Fatalf("streaming wrapper args = %q", got)
	}
	if got := execArgs["arg"].([]string)[1]; !strings.HasPrefix(got, "out=$1\nerr=$2\nshift 2\nexec ") {
		t.Fatalf("streaming wrapper script = %q", got)
	}
	cleanup := client.commands[len(client.commands)-2]
	cleanupArgs := cleanup.Arguments.(map[string]any)
	if cleanup.Execute != "guest-exec" || cleanupArgs["path"] != "rm" {
		t.Fatalf("cleanup command = %#v", client.commands[len(client.commands)-1])
	}
	if got := strings.Join(cleanupArgs["arg"].([]string), " "); got != "-rf /tmp/docker-vm-runner-guest-exec-test" {
		t.Fatalf("cleanup args = %q", got)
	}
	if client.commands[len(client.commands)-1].Execute != "guest-exec-status" {
		t.Fatalf("cleanup status command = %#v", client.commands[len(client.commands)-1])
	}
}

func TestStreamWritesOutputWithoutReturningCapturedOutput(t *testing.T) {
	client := &fakeClient{domains: []string{"test-vm"}}
	client.responses = []response{
		{raw: rawJSON(t, `{"pid":10}`)},
		{raw: rawJSON(t, `{"exited":true,"exitcode":0}`)},
		{raw: rawJSON(t, `101`)},
		{raw: rawJSON(t, `{}`)},
		{raw: rawJSON(t, `102`)},
		{raw: rawJSON(t, `{}`)},
		{raw: rawJSON(t, `201`)},
		{raw: rawJSON(t, `202`)},
		{raw: rawJSON(t, `{"pid":42}`)},
		{raw: rawJSON(t, `{"count":6,"buf-b64":"`+b64("stdout")+`","eof":true}`)},
		{raw: rawJSON(t, `{"count":0,"eof":true}`)},
		{raw: rawJSON(t, `{"exited":true,"exitcode":9}`)},
		{raw: rawJSON(t, `{"count":0,"eof":true}`)},
		{raw: rawJSON(t, `{"count":0,"eof":true}`)},
		{raw: rawJSON(t, `{}`)},
		{raw: rawJSON(t, `{}`)},
		{raw: rawJSON(t, `{"pid":43}`)},
		{raw: rawJSON(t, `{"exited":true,"exitcode":0}`)},
	}
	var stdout, stderr bytes.Buffer
	executor := NewExecutor(client)
	executor.TempDir = func(int) string { return "/tmp/docker-vm-runner-guest-exec-test" }

	exitCode, err := executor.Stream(context.Background(), Invocation{Path: "echo", Args: []string{"hello"}}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	if exitCode != 9 {
		t.Fatalf("exitCode = %d", exitCode)
	}
	if stdout.String() != "stdout" || stderr.String() != "" {
		t.Fatalf("stdout = %q stderr = %q", stdout.String(), stderr.String())
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

func TestVirshClientUsesLibvirtURI(t *testing.T) {
	runner := &fakeRunner{}
	runner.results = []process.Result{{Stdout: "vm-a\n"}}
	client := NewVirshClient(runner)
	client.LibvirtURI = "qemu:///session"

	if _, err := client.ListRunningDomains(context.Background()); err != nil {
		t.Fatalf("ListRunningDomains returned error: %v", err)
	}
	if got := strings.Join(runner.commands[0].Args, " "); got != "-c qemu:///session list --name --state-running" {
		t.Fatalf("args = %#v", runner.commands[0].Args)
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
	domains          []string
	domainsRequested []struct{}
	responses        []response
	commands         []Command
}

func (c *fakeClient) ListRunningDomains(context.Context) ([]string, error) {
	c.domainsRequested = append(c.domainsRequested, struct{}{})
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
