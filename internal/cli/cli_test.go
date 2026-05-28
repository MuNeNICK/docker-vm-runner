package cli

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/munenick/docker-vm-runner/internal/runner"
)

func TestRunVersion(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"--version"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("code = %d", code)
	}
	if !strings.Contains(stdout.String(), "docker-vm-runner dev") {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestRunListDistrosWithArch(t *testing.T) {
	original := newRunner
	defer func() { newRunner = original }()
	fake := &fakeRunner{}
	newRunner = func() appRunner { return fake }

	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"--list-distros", "arm64"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("code = %d stderr=%q", code, stderr.String())
	}
	if !fake.options.ListDistros || fake.options.ListArch != "arm64" {
		t.Fatalf("options = %#v", fake.options)
	}
}

func TestRunForwardsFlags(t *testing.T) {
	original := newRunner
	defer func() { newRunner = original }()
	fake := &fakeRunner{}
	newRunner = func() appRunner { return fake }

	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"--no-console", "--show-config"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("code = %d stderr=%q", code, stderr.String())
	}
	if !fake.options.NoConsole || !fake.options.ShowConfig {
		t.Fatalf("options = %#v", fake.options)
	}
}

func TestRunReturnsErrorCodeWhenRunnerFails(t *testing.T) {
	original := newRunner
	defer func() { newRunner = original }()
	newRunner = func() appRunner { return &fakeRunner{err: errors.New("bad config")} }

	var stdout, stderr bytes.Buffer
	code := run(context.Background(), nil, &stdout, &stderr)

	if code != 1 {
		t.Fatalf("code = %d", code)
	}
	if !strings.Contains(stderr.String(), "bad config") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestRunRejectsUnexpectedArguments(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"unexpected"}, &stdout, &stderr)

	if code != 2 {
		t.Fatalf("code = %d", code)
	}
}

type fakeRunner struct {
	options runner.Options
	err     error
}

func (r *fakeRunner) Run(_ context.Context, opts runner.Options) error {
	r.options = opts
	return r.err
}
