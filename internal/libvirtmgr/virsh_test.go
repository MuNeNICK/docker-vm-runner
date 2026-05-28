package libvirtmgr

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/munenick/docker-vm-runner/internal/process"
)

func TestVirshLookupDomain(t *testing.T) {
	runner := &fakeVirshRunner{results: []process.Result{{Stdout: "Id: 1\n"}}}
	conn := NewVirshConnection(context.Background(), "qemu:///system", runner)

	domain, err := conn.LookupDomain("vm1")
	if err != nil {
		t.Fatalf("LookupDomain returned error: %v", err)
	}
	if domain.Name() != "vm1" {
		t.Fatalf("domain name = %q", domain.Name())
	}
	want := []string{"-c", "qemu:///system", "dominfo", "vm1"}
	if strings.Join(runner.commands[0].Args, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("command = %#v", runner.commands[0])
	}
}

func TestVirshLookupDomainNotFound(t *testing.T) {
	runner := &fakeVirshRunner{err: &process.ExitError{Name: "virsh", ExitCode: 1, Stderr: "Domain not found"}}
	conn := NewVirshConnection(context.Background(), "", runner)

	_, err := conn.LookupDomain("vm1")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
}

func TestVirshDefineDomain(t *testing.T) {
	runner := &fakeVirshRunner{}
	conn := NewVirshConnection(context.Background(), "", runner)

	domain, err := conn.DefineDomain(`<domain><name>vm1</name></domain>`)
	if err != nil {
		t.Fatalf("DefineDomain returned error: %v", err)
	}
	if domain.Name() != "vm1" {
		t.Fatalf("domain name = %q", domain.Name())
	}
	if runner.commands[0].Args[2] != "define" || !strings.HasSuffix(runner.commands[0].Args[3], "definition.xml") {
		t.Fatalf("command = %#v", runner.commands[0])
	}
}

func TestVirshDomainIsActive(t *testing.T) {
	runner := &fakeVirshRunner{results: []process.Result{{Stdout: "running\n"}}}
	conn := NewVirshConnection(context.Background(), "", runner)
	domain := &VirshDomain{NameValue: "vm1", Conn: conn}

	active, err := domain.IsActive()
	if err != nil {
		t.Fatalf("IsActive returned error: %v", err)
	}
	if !active {
		t.Fatal("active = false")
	}
}

func TestVirshStoragePoolLifecycle(t *testing.T) {
	runner := &fakeVirshRunner{results: []process.Result{
		{Stdout: "State: inactive\n"},
		{},
	}}
	conn := NewVirshConnection(context.Background(), "", runner)
	pool := &VirshStoragePool{NameValue: "default", Conn: conn}

	active, err := pool.IsActive()
	if err != nil {
		t.Fatalf("IsActive returned error: %v", err)
	}
	if active {
		t.Fatal("active = true")
	}
	if err := pool.Create(); err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if runner.commands[1].Args[2] != "pool-start" {
		t.Fatalf("commands = %#v", runner.commands)
	}
}

type fakeVirshRunner struct {
	commands []process.Command
	results  []process.Result
	err      error
}

func (r *fakeVirshRunner) Run(_ context.Context, cmd process.Command) (process.Result, error) {
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
