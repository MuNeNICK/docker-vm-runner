package seediso

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/munenick/docker-vm-runner/internal/process"
)

type Runner interface {
	Run(context.Context, process.Command) (process.Result, error)
}

type Builder struct {
	Runner Runner
}

func NewBuilder(runner Runner) *Builder {
	if runner == nil {
		runner = process.NewCommandRunner()
	}
	return &Builder{Runner: runner}
}

func (b *Builder) Build(ctx context.Context, outputPath string, content SeedContent) error {
	dir := filepath.Dir(outputPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create seed ISO directory: %w", err)
	}
	metaPath := filepath.Join(dir, "meta-data")
	userPath := filepath.Join(dir, "user-data")
	vendorPath := filepath.Join(dir, "vendor-data")
	if err := os.WriteFile(metaPath, []byte(content.MetaData), 0o644); err != nil {
		return fmt.Errorf("write meta-data: %w", err)
	}
	if err := os.WriteFile(userPath, []byte(content.UserData), 0o644); err != nil {
		return fmt.Errorf("write user-data: %w", err)
	}
	if err := os.WriteFile(vendorPath, []byte(content.VendorData), 0o644); err != nil {
		return fmt.Errorf("write vendor-data: %w", err)
	}
	cmd := GenISOCommand(GenISORequest{
		OutputPath: outputPath,
		MetaData:   metaPath,
		UserData:   userPath,
		VendorData: vendorPath,
	})
	if _, err := b.Runner.Run(ctx, process.Command{Name: cmd[0], Args: cmd[1:]}); err != nil {
		return fmt.Errorf("generate seed ISO: %w", err)
	}
	return nil
}
