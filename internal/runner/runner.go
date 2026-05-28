package runner

import "context"

type Options struct {
	NoConsole   bool
	ListDistros bool
	ShowConfig  bool
}

type Runner struct{}

func New() *Runner {
	return &Runner{}
}

func (r *Runner) Run(ctx context.Context, opts Options) error {
	return nil
}
