package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/munenick/docker-vm-runner/internal/runner"
)

const version = "dev"

type options struct {
	noConsole   bool
	listDistros bool
	showConfig  bool
	version     bool
}

func Main(ctx context.Context, args []string) int {
	return run(ctx, args, os.Stdout, os.Stderr)
}

func run(ctx context.Context, args []string, stdout io.Writer, stderr io.Writer) int {
	opts, err := parse(args, stderr)
	if err != nil {
		return 2
	}
	if opts.version {
		fmt.Fprintf(stdout, "docker-vm-runner %s\n", version)
		return 0
	}

	app := runner.New()
	if err := app.Run(ctx, runner.Options{
		NoConsole:   opts.noConsole,
		ListDistros: opts.listDistros,
		ShowConfig:  opts.showConfig,
	}); err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	return 0
}

func parse(args []string, stderr io.Writer) (options, error) {
	var opts options
	fs := flag.NewFlagSet("docker-vm-runner", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.BoolVar(&opts.noConsole, "no-console", false, "do not attach to the VM console")
	fs.BoolVar(&opts.listDistros, "list-distros", false, "list configured distributions and exit")
	fs.BoolVar(&opts.showConfig, "show-config", false, "print resolved configuration and exit")
	fs.BoolVar(&opts.version, "version", false, "print version and exit")
	if err := fs.Parse(args); err != nil {
		return options{}, err
	}
	return opts, nil
}
