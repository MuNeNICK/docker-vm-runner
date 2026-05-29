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

func TestRunHelpReturnsZero(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"--help"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("code = %d stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "Usage of docker-vm-runner") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestRunListDistrosWithArch(t *testing.T) {
	original := newRunner
	defer func() { newRunner = original }()
	fake := &fakeRunner{}
	newRunner = func() appRunner { return fake }

	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"--list-distros", "--arch", "arm64"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("code = %d stderr=%q", code, stderr.String())
	}
	if !fake.options.ListDistros || fake.options.ListArch != "arm64" {
		t.Fatalf("options = %#v", fake.options)
	}
}

func TestRunListDistrosRejectsPositionalArch(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"--list-distros", "arm64"}, &stdout, &stderr)

	if code != 2 {
		t.Fatalf("code = %d", code)
	}
	if !strings.Contains(stderr.String(), "unexpected arguments: arm64") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestRunListDistrosWithTypeAndSearch(t *testing.T) {
	original := newRunner
	defer func() { newRunner = original }()
	fake := &fakeRunner{}
	newRunner = func() appRunner { return fake }

	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"--list-distros", "--type", "cloud image", "--search", "ubuntu"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("code = %d stderr=%q", code, stderr.String())
	}
	if !fake.options.ListDistros || fake.options.ListType != "cloud image" || fake.options.ListSearch != "ubuntu" {
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

func TestRunForwardsCleanupFlag(t *testing.T) {
	original := newRunner
	defer func() { newRunner = original }()
	fake := &fakeRunner{}
	newRunner = func() appRunner { return fake }

	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"--cleanup"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("code = %d stderr=%q", code, stderr.String())
	}
	if !fake.options.Cleanup {
		t.Fatalf("options = %#v", fake.options)
	}
}

func TestRunReadsNoConsoleFromEnv(t *testing.T) {
	original := newRunner
	defer func() { newRunner = original }()
	fake := &fakeRunner{}
	newRunner = func() appRunner { return fake }

	var stdout, stderr bytes.Buffer
	code := runWithEnv(context.Background(), nil, &stdout, &stderr, func(key string) (string, bool) {
		if key == "NO_CONSOLE" {
			return "yes", true
		}
		return "", false
	})

	if code != 0 {
		t.Fatalf("code = %d stderr=%q", code, stderr.String())
	}
	if !fake.options.NoConsole {
		t.Fatalf("options = %#v", fake.options)
	}
}

func TestRunDisablesConsoleForNoVNC(t *testing.T) {
	original := newRunner
	defer func() { newRunner = original }()
	fake := &fakeRunner{}
	newRunner = func() appRunner { return fake }

	var stdout, stderr bytes.Buffer
	code := runWithEnv(context.Background(), nil, &stdout, &stderr, func(key string) (string, bool) {
		if key == "GRAPHICS" {
			return "novnc", true
		}
		return "", false
	})

	if code != 0 {
		t.Fatalf("code = %d stderr=%q", code, stderr.String())
	}
	if !fake.options.NoConsole {
		t.Fatalf("options = %#v", fake.options)
	}
}

func TestRunRespectsExplicitNoConsoleFalseForNoVNC(t *testing.T) {
	original := newRunner
	originalTTY := stdinIsTerminal
	originalStdoutTTY := stdoutIsTerminal
	defer func() {
		newRunner = original
		stdinIsTerminal = originalTTY
		stdoutIsTerminal = originalStdoutTTY
	}()
	fake := &fakeRunner{}
	newRunner = func() appRunner { return fake }
	stdinIsTerminal = func() bool { return true }
	stdoutIsTerminal = func() bool { return true }

	var stdout, stderr bytes.Buffer
	code := runWithEnv(context.Background(), nil, &stdout, &stderr, func(key string) (string, bool) {
		switch key {
		case "GRAPHICS":
			return "novnc", true
		case "NO_CONSOLE":
			return "0", true
		default:
			return "", false
		}
	})

	if code != 0 {
		t.Fatalf("code = %d stderr=%q", code, stderr.String())
	}
	if fake.options.NoConsole {
		t.Fatalf("options = %#v", fake.options)
	}
}

func TestRunDisablesConsoleWithoutTTY(t *testing.T) {
	originalRunner := newRunner
	originalTTY := stdinIsTerminal
	originalStdoutTTY := stdoutIsTerminal
	defer func() {
		newRunner = originalRunner
		stdinIsTerminal = originalTTY
		stdoutIsTerminal = originalStdoutTTY
	}()
	fake := &fakeRunner{}
	newRunner = func() appRunner { return fake }
	stdinIsTerminal = func() bool { return false }
	stdoutIsTerminal = func() bool { return true }

	var stdout, stderr bytes.Buffer
	code := runWithEnv(context.Background(), nil, &stdout, &stderr, func(string) (string, bool) { return "", false })

	if code != 0 {
		t.Fatalf("code = %d stderr=%q", code, stderr.String())
	}
	if !fake.options.NoConsole {
		t.Fatalf("options = %#v", fake.options)
	}
}

func TestRunKeepsNoConsoleForNoConsoleFalseWithoutTTY(t *testing.T) {
	originalRunner := newRunner
	originalTTY := stdinIsTerminal
	originalStdoutTTY := stdoutIsTerminal
	defer func() {
		newRunner = originalRunner
		stdinIsTerminal = originalTTY
		stdoutIsTerminal = originalStdoutTTY
	}()
	fake := &fakeRunner{}
	newRunner = func() appRunner { return fake }
	stdinIsTerminal = func() bool { return false }
	stdoutIsTerminal = func() bool { return true }

	var stdout, stderr bytes.Buffer
	code := runWithEnv(context.Background(), nil, &stdout, &stderr, func(key string) (string, bool) {
		if key == "NO_CONSOLE" {
			return "0", true
		}
		return "", false
	})

	if code != 0 {
		t.Fatalf("code = %d stderr=%q", code, stderr.String())
	}
	if !fake.options.NoConsole {
		t.Fatalf("options = %#v", fake.options)
	}
}

func TestRunDisablesConsoleWithoutStdoutTTY(t *testing.T) {
	originalRunner := newRunner
	originalTTY := stdinIsTerminal
	originalStdoutTTY := stdoutIsTerminal
	defer func() {
		newRunner = originalRunner
		stdinIsTerminal = originalTTY
		stdoutIsTerminal = originalStdoutTTY
	}()
	fake := &fakeRunner{}
	newRunner = func() appRunner { return fake }
	stdinIsTerminal = func() bool { return true }
	stdoutIsTerminal = func() bool { return false }

	var stdout, stderr bytes.Buffer
	code := runWithEnv(context.Background(), nil, &stdout, &stderr, func(string) (string, bool) { return "", false })

	if code != 0 {
		t.Fatalf("code = %d stderr=%q", code, stderr.String())
	}
	if !fake.options.NoConsole {
		t.Fatalf("options = %#v", fake.options)
	}
}

func TestRunNoConsoleFlagOverridesEnvDefault(t *testing.T) {
	original := newRunner
	originalTTY := stdinIsTerminal
	originalStdoutTTY := stdoutIsTerminal
	defer func() {
		newRunner = original
		stdinIsTerminal = originalTTY
		stdoutIsTerminal = originalStdoutTTY
	}()
	fake := &fakeRunner{}
	newRunner = func() appRunner { return fake }
	stdinIsTerminal = func() bool { return true }
	stdoutIsTerminal = func() bool { return true }

	var stdout, stderr bytes.Buffer
	code := runWithEnv(context.Background(), []string{"--no-console=false"}, &stdout, &stderr, func(key string) (string, bool) {
		if key == "NO_CONSOLE" {
			return "1", true
		}
		return "", false
	})

	if code != 0 {
		t.Fatalf("code = %d stderr=%q", code, stderr.String())
	}
	if fake.options.NoConsole {
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
