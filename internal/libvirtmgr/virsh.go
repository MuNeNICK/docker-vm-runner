package libvirtmgr

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/munenick/docker-vm-runner/internal/process"
)

const DefaultURI = "qemu:///system"

type CommandRunner interface {
	Run(context.Context, process.Command) (process.Result, error)
}

type VirshConnection struct {
	URI    string
	Runner CommandRunner
	ctx    context.Context
}

func NewVirshConnection(ctx context.Context, uri string, runner CommandRunner) *VirshConnection {
	if uri == "" {
		uri = DefaultURI
	}
	if runner == nil {
		runner = process.NewCommandRunner()
	}
	return &VirshConnection{URI: uri, Runner: runner, ctx: ctx}
}

func (c *VirshConnection) LookupDomain(name string) (Domain, error) {
	if _, err := c.virsh("dominfo", name); err != nil {
		if isVirshNotFound(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &VirshDomain{NameValue: name, Conn: c}, nil
}

func (c *VirshConnection) DefineDomain(xml string) (Domain, error) {
	path, err := writeTempXML(xml)
	if err != nil {
		return nil, err
	}
	defer os.Remove(path)
	if _, err := c.virsh("define", path); err != nil {
		return nil, err
	}
	name := domainNameFromXML(xml)
	if name == "" {
		return nil, fmt.Errorf("domain XML missing name")
	}
	return &VirshDomain{NameValue: name, Conn: c}, nil
}

func (c *VirshConnection) LookupStoragePool(name string) (StoragePool, error) {
	if _, err := c.virsh("pool-info", name); err != nil {
		if isVirshNotFound(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &VirshStoragePool{NameValue: name, Conn: c}, nil
}

func (c *VirshConnection) DefineStoragePool(xml string) (StoragePool, error) {
	path, err := writeTempXML(xml)
	if err != nil {
		return nil, err
	}
	defer os.Remove(path)
	if _, err := c.virsh("pool-define", path); err != nil {
		return nil, err
	}
	name := domainNameFromXML(xml)
	if name == "" {
		return nil, fmt.Errorf("storage pool XML missing name")
	}
	return &VirshStoragePool{NameValue: name, Conn: c}, nil
}

func (c *VirshConnection) Close() error {
	return nil
}

func (c *VirshConnection) virsh(args ...string) (process.Result, error) {
	fullArgs := []string{"-c", c.URI}
	fullArgs = append(fullArgs, args...)
	result, err := c.Runner.Run(c.ctx, process.Command{Name: "virsh", Args: fullArgs})
	if err != nil {
		if strings.TrimSpace(result.Stderr) != "" {
			return result, fmt.Errorf("virsh %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(result.Stderr))
		}
		return result, fmt.Errorf("virsh %s: %w", strings.Join(args, " "), err)
	}
	return result, nil
}

type VirshDomain struct {
	NameValue string
	Conn      *VirshConnection
}

func (d *VirshDomain) Name() string { return d.NameValue }

func (d *VirshDomain) XML() (string, error) {
	result, err := d.Conn.virsh("dumpxml", d.NameValue)
	if err != nil {
		return "", err
	}
	return result.Stdout, nil
}

func (d *VirshDomain) IsActive() (bool, error) {
	result, err := d.Conn.virsh("domstate", d.NameValue)
	if err != nil {
		if isVirshNotFound(err) {
			return false, ErrNotFound
		}
		return false, err
	}
	return strings.Contains(strings.ToLower(result.Stdout), "running"), nil
}

func (d *VirshDomain) Create() error {
	_, err := d.Conn.virsh("start", d.NameValue)
	return err
}

func (d *VirshDomain) Shutdown() error {
	_, err := d.Conn.virsh("shutdown", d.NameValue)
	return err
}

func (d *VirshDomain) Destroy() error {
	_, err := d.Conn.virsh("destroy", d.NameValue)
	return err
}

func (d *VirshDomain) Undefine() error {
	_, err := d.Conn.virsh("undefine", d.NameValue)
	return err
}

func (d *VirshDomain) UndefineNVRAM() error {
	_, err := d.Conn.virsh("undefine", "--nvram", d.NameValue)
	return err
}

type VirshStoragePool struct {
	NameValue string
	Conn      *VirshConnection
}

func (p *VirshStoragePool) Name() string { return p.NameValue }

func (p *VirshStoragePool) IsActive() (bool, error) {
	result, err := p.Conn.virsh("pool-info", p.NameValue)
	if err != nil {
		return false, err
	}
	for _, line := range strings.Split(result.Stdout, "\n") {
		key, value, ok := strings.Cut(line, ":")
		if ok && strings.EqualFold(strings.TrimSpace(key), "State") {
			return strings.EqualFold(strings.TrimSpace(value), "running"), nil
		}
	}
	return false, nil
}

func (p *VirshStoragePool) Create() error {
	_, err := p.Conn.virsh("pool-start", p.NameValue)
	return err
}

func writeTempXML(xml string) (string, error) {
	dir, err := os.MkdirTemp("", "docker-vm-runner-libvirt-*")
	if err != nil {
		return "", fmt.Errorf("create temp XML directory: %w", err)
	}
	path := filepath.Join(dir, "definition.xml")
	if err := os.WriteFile(path, []byte(xml), 0o600); err != nil {
		_ = os.RemoveAll(dir)
		return "", fmt.Errorf("write temp XML: %w", err)
	}
	return path, nil
}

func domainNameFromXML(xml string) string {
	start := strings.Index(xml, "<name>")
	end := strings.Index(xml, "</name>")
	if start == -1 || end == -1 || end <= start+len("<name>") {
		return ""
	}
	return strings.TrimSpace(xml[start+len("<name>") : end])
}

func isVirshNotFound(err error) bool {
	var exitErr *process.ExitError
	if errors.As(err, &exitErr) {
		message := strings.ToLower(exitErr.Stderr)
		return strings.Contains(message, "not found") ||
			strings.Contains(message, "failed to get domain") ||
			strings.Contains(message, "failed to get pool") ||
			strings.Contains(message, "no domain") ||
			strings.Contains(message, "no storage pool")
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "not found") ||
		strings.Contains(message, "no domain") ||
		strings.Contains(message, "no storage pool")
}
