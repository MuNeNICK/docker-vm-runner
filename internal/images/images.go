package images

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/munenick/docker-vm-runner/internal/process"
)

type Runner interface {
	Run(context.Context, process.Command) (process.Result, error)
}

type DiskManager struct {
	runner Runner
}

func NewDiskManager(runner Runner) *DiskManager {
	return &DiskManager{runner: runner}
}

type ImageInfo struct {
	Format      string `json:"format"`
	VirtualSize int64  `json:"virtual-size"`
}

type CreateDiskRequest struct {
	Path        string
	Format      string
	Size        string
	Preallocate bool
}

func (m *DiskManager) ImageInfo(ctx context.Context, path string) (ImageInfo, error) {
	result, err := m.runner.Run(ctx, process.Command{
		Name: "qemu-img",
		Args: []string{"info", "--output=json", path},
	})
	if err != nil {
		return ImageInfo{}, fmt.Errorf("qemu-img info %s: %w", path, err)
	}
	var info ImageInfo
	if err := json.Unmarshal([]byte(result.Stdout), &info); err != nil {
		return ImageInfo{}, fmt.Errorf("parse qemu-img info for %s: %w", path, err)
	}
	return info, nil
}

func (m *DiskManager) CreateDisk(ctx context.Context, req CreateDiskRequest) error {
	args := []string{"create", "-f", req.Format}
	if req.Preallocate {
		args = append(args, "-o", "preallocation=falloc")
	}
	args = append(args, req.Path, req.Size)
	if _, err := m.runner.Run(ctx, process.Command{Name: "qemu-img", Args: args}); err != nil {
		return fmt.Errorf("qemu-img create %s: %w", req.Path, err)
	}
	return nil
}

func (m *DiskManager) ResizeDisk(ctx context.Context, path string, size string) error {
	if _, err := m.runner.Run(ctx, process.Command{
		Name: "qemu-img",
		Args: []string{"resize", path, size},
	}); err != nil {
		return fmt.Errorf("qemu-img resize %s: %w", path, err)
	}
	return nil
}

func (m *DiskManager) ConvertDisk(ctx context.Context, source string, dest string, format string) error {
	if _, err := m.runner.Run(ctx, process.Command{
		Name: "qemu-img",
		Args: []string{"convert", "-p", "-O", format, source, dest},
	}); err != nil {
		return fmt.Errorf("qemu-img convert %s to %s: %w", source, dest, err)
	}
	return nil
}
