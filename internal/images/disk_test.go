package images

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/munenick/docker-vm-runner/internal/process"
)

type fakeRunner struct {
	results []process.Result
	errs    []error
	calls   []process.Command
}

func (f *fakeRunner) Run(ctx context.Context, command process.Command) (process.Result, error) {
	f.calls = append(f.calls, command)
	idx := len(f.calls) - 1
	if idx < len(f.errs) && f.errs[idx] != nil {
		return process.Result{}, f.errs[idx]
	}
	if idx < len(f.results) {
		return f.results[idx], nil
	}
	return process.Result{}, nil
}

func TestImageInfoParsesQEMUJSON(t *testing.T) {
	runner := &fakeRunner{
		results: []process.Result{{Stdout: `{"format":"qcow2","virtual-size":21474836480}`}},
	}
	manager := NewDiskManager(runner)
	info, err := manager.ImageInfo(context.Background(), "/images/disk.qcow2")
	if err != nil {
		t.Fatalf("ImageInfo returned error: %v", err)
	}
	if info.Format != "qcow2" {
		t.Fatalf("Format = %q", info.Format)
	}
	if info.VirtualSize != 21474836480 {
		t.Fatalf("VirtualSize = %d", info.VirtualSize)
	}
	want := process.Command{Name: "qemu-img", Args: []string{"info", "--output=json", "/images/disk.qcow2"}}
	if !reflect.DeepEqual(runner.calls[0], want) {
		t.Fatalf("command = %#v want %#v", runner.calls[0], want)
	}
}

func TestImageInfoWrapsCommandError(t *testing.T) {
	runner := &fakeRunner{errs: []error{errors.New("boom")}}
	manager := NewDiskManager(runner)
	_, err := manager.ImageInfo(context.Background(), "/images/disk.qcow2")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestImageInfoRejectsMalformedJSON(t *testing.T) {
	runner := &fakeRunner{results: []process.Result{{Stdout: `{`}}}
	manager := NewDiskManager(runner)
	_, err := manager.ImageInfo(context.Background(), "/images/disk.qcow2")
	if err == nil {
		t.Fatal("expected JSON error")
	}
}

func TestCreateDiskCommand(t *testing.T) {
	runner := &fakeRunner{}
	manager := NewDiskManager(runner)
	err := manager.CreateDisk(context.Background(), CreateDiskRequest{
		Path:        "/images/disk.qcow2",
		Format:      "qcow2",
		Size:        "20G",
		Preallocate: true,
	})
	if err != nil {
		t.Fatalf("CreateDisk returned error: %v", err)
	}
	want := process.Command{Name: "qemu-img", Args: []string{"create", "-f", "qcow2", "-o", "preallocation=falloc", "/images/disk.qcow2", "20G"}}
	if !reflect.DeepEqual(runner.calls[0], want) {
		t.Fatalf("command = %#v want %#v", runner.calls[0], want)
	}
}

func TestResizeDiskCommand(t *testing.T) {
	runner := &fakeRunner{}
	manager := NewDiskManager(runner)
	if err := manager.ResizeDisk(context.Background(), "/images/disk.qcow2", "40G"); err != nil {
		t.Fatalf("ResizeDisk returned error: %v", err)
	}
	want := process.Command{Name: "qemu-img", Args: []string{"resize", "/images/disk.qcow2", "40G"}}
	if !reflect.DeepEqual(runner.calls[0], want) {
		t.Fatalf("command = %#v want %#v", runner.calls[0], want)
	}
}

func TestConvertDiskCommand(t *testing.T) {
	runner := &fakeRunner{}
	manager := NewDiskManager(runner)
	if err := manager.ConvertDisk(context.Background(), "/tmp/source.vmdk", "/images/disk.qcow2", "qcow2"); err != nil {
		t.Fatalf("ConvertDisk returned error: %v", err)
	}
	want := process.Command{Name: "qemu-img", Args: []string{"convert", "-p", "-O", "qcow2", "/tmp/source.vmdk", "/images/disk.qcow2"}}
	if !reflect.DeepEqual(runner.calls[0], want) {
		t.Fatalf("command = %#v want %#v", runner.calls[0], want)
	}
}
