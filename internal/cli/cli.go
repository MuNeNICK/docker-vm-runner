package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/munenick/docker-vm-runner/internal/config"
	"github.com/munenick/docker-vm-runner/internal/runner"
)

const version = "dev"

type options struct {
	noConsole   bool
	listDistros bool
	listArch    string
	showConfig  bool
	showXML     bool
	dryRun      bool
	cleanup     bool
	version     bool
}

type appRunner interface {
	Run(context.Context, runner.Options) error
}

var newRunner = func() appRunner {
	return runner.New()
}

func Main(ctx context.Context, args []string) int {
	return runWithEnv(ctx, args, os.Stdout, os.Stderr, config.OSEnv)
}

func run(ctx context.Context, args []string, stdout io.Writer, stderr io.Writer) int {
	return runWithEnv(ctx, args, stdout, stderr, func(string) (string, bool) { return "", false })
}

func runWithEnv(ctx context.Context, args []string, stdout io.Writer, stderr io.Writer, lookup config.LookupFunc) int {
	args = applyEnvDefaults(args, lookup)
	opts, err := parse(args, stderr)
	if err != nil {
		return 2
	}
	if opts.version {
		fmt.Fprintf(stdout, "docker-vm-runner %s\n", version)
		return 0
	}

	app := newRunner()
	if err := app.Run(ctx, runner.Options{
		NoConsole:   opts.noConsole,
		ListDistros: opts.listDistros,
		ListArch:    opts.listArch,
		ShowConfig:  opts.showConfig,
		ShowXML:     opts.showXML,
		DryRun:      opts.dryRun,
		Cleanup:     opts.cleanup,
	}); err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	return 0
}

func applyEnvDefaults(args []string, lookup config.LookupFunc) []string {
	noConsole, _ := config.BoolFrom(lookup, "NO_CONSOLE", false)
	if !noConsole {
		return args
	}
	withDefaults := make([]string, 0, len(args)+1)
	withDefaults = append(withDefaults, "--no-console")
	withDefaults = append(withDefaults, args...)
	return withDefaults
}

func parse(args []string, stderr io.Writer) (options, error) {
	var opts options
	fs := flag.NewFlagSet("docker-vm-runner", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.BoolVar(&opts.noConsole, "no-console", false, "do not attach to the VM console")
	fs.BoolVar(&opts.listDistros, "list-distros", false, "list configured distributions and exit")
	fs.BoolVar(&opts.showConfig, "show-config", false, "print resolved configuration and exit")
	fs.BoolVar(&opts.showXML, "show-xml", false, "print resolved libvirt domain XML and exit")
	fs.BoolVar(&opts.dryRun, "dry-run", false, "validate configuration without starting a VM")
	fs.BoolVar(&opts.cleanup, "cleanup", false, "cleanup stale VM resources and exit")
	fs.BoolVar(&opts.version, "version", false, "print version and exit")
	if err := fs.Parse(args); err != nil {
		return options{}, err
	}
	remaining := fs.Args()
	if opts.listDistros && len(remaining) > 0 {
		opts.listArch = remaining[0]
		remaining = remaining[1:]
	}
	if len(remaining) > 0 {
		return options{}, fmt.Errorf("unexpected arguments: %s", strings.Join(remaining, " "))
	}
	return opts, nil
}
