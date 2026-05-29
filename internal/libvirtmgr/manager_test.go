package libvirtmgr

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestEnsureDefinedRejectsExistingUnmanagedDomain(t *testing.T) {
	conn := &fakeConnection{domains: map[string]*fakeDomain{
		"test-vm": {name: "test-vm", xmlText: `<domain><name>test-vm</name></domain>`},
	}}
	manager := New(conn)

	_, err := manager.EnsureDefined("test-vm", "<domain/>")
	if err == nil {
		t.Fatal("expected unmanaged existing domain error")
	}
	if conn.defineCalls != 0 {
		t.Fatalf("defineCalls = %d", conn.defineCalls)
	}
}

func TestEnsureDefinedReplacesStaleManagedDomain(t *testing.T) {
	existing := &fakeDomain{name: "test-vm", active: true, xmlText: managedDomainXML("test-vm")}
	conn := &fakeConnection{domains: map[string]*fakeDomain{"test-vm": existing}}
	manager := New(conn)

	domain, err := manager.EnsureDefined("test-vm", managedDomainXML("test-vm"))
	if err != nil {
		t.Fatalf("EnsureDefined returned error: %v", err)
	}
	if domain.Name() != "test-vm" {
		t.Fatalf("domain name = %q", domain.Name())
	}
	if existing.destroyCalls != 1 || existing.undefineCalls != 1 {
		t.Fatalf("destroyCalls=%d undefineCalls=%d", existing.destroyCalls, existing.undefineCalls)
	}
	if conn.defineCalls != 1 {
		t.Fatalf("defineCalls = %d", conn.defineCalls)
	}
}

func TestReconcileStaleDomainIgnoresMissingDomain(t *testing.T) {
	conn := &fakeConnection{domains: map[string]*fakeDomain{}}
	if err := New(conn).ReconcileStaleDomain("missing"); err != nil {
		t.Fatalf("ReconcileStaleDomain returned error: %v", err)
	}
}

func TestReconcileStaleDomainRejectsUnmanagedDomain(t *testing.T) {
	conn := &fakeConnection{domains: map[string]*fakeDomain{
		"test-vm": {name: "test-vm", xmlText: `<domain><name>test-vm</name></domain>`},
	}}
	err := New(conn).ReconcileStaleDomain("test-vm")
	if err == nil {
		t.Fatal("expected unmanaged domain error")
	}
}

func TestEnsureDefinedUsesNVRAMUndefineForStaleManagedDomain(t *testing.T) {
	existing := &fakeDomain{name: "test-vm", xmlText: strings.Replace(managedDomainXML("test-vm"), "</metadata>", "</metadata><os><nvram>/state/test.fd</nvram></os>", 1)}
	conn := &fakeConnection{domains: map[string]*fakeDomain{"test-vm": existing}}

	if _, err := New(conn).EnsureDefined("test-vm", managedDomainXML("test-vm")); err != nil {
		t.Fatalf("EnsureDefined returned error: %v", err)
	}
	if existing.undefineNVRAMCalls != 1 {
		t.Fatalf("undefineNVRAMCalls = %d", existing.undefineNVRAMCalls)
	}
}

func TestParseRunnerMetadata(t *testing.T) {
	metadata, err := ParseRunnerMetadata(managedDomainXML("vm1"))
	if err != nil {
		t.Fatalf("ParseRunnerMetadata returned error: %v", err)
	}
	if !metadata.Managed || metadata.VMName != "vm1" {
		t.Fatalf("metadata = %#v", metadata)
	}
}

func TestParseRunnerMetadataIgnoresUnmanagedXML(t *testing.T) {
	metadata, err := ParseRunnerMetadata(`<domain><name>vm1</name></domain>`)
	if err != nil {
		t.Fatalf("ParseRunnerMetadata returned error: %v", err)
	}
	if metadata.Managed || metadata.VMName != "" {
		t.Fatalf("metadata = %#v", metadata)
	}
}

func TestEnsureDefinedDefinesMissingDomain(t *testing.T) {
	conn := &fakeConnection{domains: map[string]*fakeDomain{}}
	manager := New(conn)

	domain, err := manager.EnsureDefined("test-vm", "<domain/>")
	if err != nil {
		t.Fatalf("EnsureDefined returned error: %v", err)
	}
	if domain.Name() != "test-vm" {
		t.Fatalf("domain name = %q", domain.Name())
	}
	if conn.defineCalls != 1 || conn.definedXML != "<domain/>" {
		t.Fatalf("define calls = %d xml = %q", conn.defineCalls, conn.definedXML)
	}
}

func TestEnsureDefinedErrorWhenDefineReturnsNil(t *testing.T) {
	conn := &fakeConnection{domains: map[string]*fakeDomain{}, defineNil: true}
	_, err := New(conn).EnsureDefined("test-vm", "<domain/>")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "failed to define libvirt domain") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestStartDomain(t *testing.T) {
	domain := &fakeDomain{name: "test-vm", active: false}
	if err := New(nil).Start(domain); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	if !domain.active || domain.createCalls != 1 {
		t.Fatalf("active = %v createCalls = %d", domain.active, domain.createCalls)
	}

	domain.createCalls = 0
	if err := New(nil).Start(domain); err != nil {
		t.Fatalf("Start active returned error: %v", err)
	}
	if domain.createCalls != 0 {
		t.Fatalf("createCalls for active domain = %d", domain.createCalls)
	}
}

func TestStartDomainWrapsCgroupError(t *testing.T) {
	domain := &fakeDomain{name: "test-vm", createErr: errors.New("cgroup permission denied")}
	err := New(nil).Start(domain)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "--cgroupns=host") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCleanupDestroysAndUndefines(t *testing.T) {
	domain := &fakeDomain{name: "test-vm", active: true}
	err := New(nil).Cleanup(domain, CleanupOptions{HasNVRAM: false})
	if err != nil {
		t.Fatalf("Cleanup returned error: %v", err)
	}
	if domain.destroyCalls != 1 {
		t.Fatalf("destroyCalls = %d", domain.destroyCalls)
	}
	if domain.undefineCalls != 1 {
		t.Fatalf("undefineCalls = %d", domain.undefineCalls)
	}
	if domain.undefineNVRAMCalls != 0 {
		t.Fatalf("undefineNVRAMCalls = %d", domain.undefineNVRAMCalls)
	}
}

func TestCleanupGracefullyShutsDownBeforeUndefine(t *testing.T) {
	domain := &fakeDomain{name: "test-vm", active: true}
	err := New(nil).Cleanup(domain, CleanupOptions{
		Context:         context.Background(),
		ShutdownTimeout: time.Second,
		ShutdownPoll:    time.Millisecond,
	})
	if err != nil {
		t.Fatalf("Cleanup returned error: %v", err)
	}
	if domain.shutdownCalls != 1 {
		t.Fatalf("shutdownCalls = %d", domain.shutdownCalls)
	}
	if domain.destroyCalls != 0 {
		t.Fatalf("destroyCalls = %d", domain.destroyCalls)
	}
	if domain.undefineCalls != 1 {
		t.Fatalf("undefineCalls = %d", domain.undefineCalls)
	}
}

func TestCleanupUsesNVRAMUndefineFlag(t *testing.T) {
	domain := &fakeDomain{name: "test-vm", active: false}
	err := New(nil).Cleanup(domain, CleanupOptions{HasNVRAM: true})
	if err != nil {
		t.Fatalf("Cleanup returned error: %v", err)
	}
	if domain.destroyCalls != 0 {
		t.Fatalf("destroyCalls = %d", domain.destroyCalls)
	}
	if domain.undefineCalls != 0 {
		t.Fatalf("undefineCalls = %d", domain.undefineCalls)
	}
	if domain.undefineNVRAMCalls != 1 {
		t.Fatalf("undefineNVRAMCalls = %d", domain.undefineNVRAMCalls)
	}
}

func TestCleanupUsesTPMUndefineFlag(t *testing.T) {
	domain := &fakeDomain{name: "test-vm", active: false}
	err := New(nil).Cleanup(domain, CleanupOptions{HasTPM: true})
	if err != nil {
		t.Fatalf("Cleanup returned error: %v", err)
	}
	if domain.undefineCalls != 0 || domain.undefineNVRAMCalls != 0 {
		t.Fatalf("unexpected undefine calls plain=%d nvram=%d", domain.undefineCalls, domain.undefineNVRAMCalls)
	}
	if domain.undefineTPMCalls != 1 {
		t.Fatalf("undefineTPMCalls = %d", domain.undefineTPMCalls)
	}
}

func TestCleanupUsesNVRAMAndTPMUndefineFlags(t *testing.T) {
	domain := &fakeDomain{name: "test-vm", active: false}
	err := New(nil).Cleanup(domain, CleanupOptions{HasNVRAM: true, HasTPM: true})
	if err != nil {
		t.Fatalf("Cleanup returned error: %v", err)
	}
	if domain.undefineCalls != 0 || domain.undefineNVRAMCalls != 0 || domain.undefineTPMCalls != 0 {
		t.Fatalf("unexpected undefine calls plain=%d nvram=%d tpm=%d", domain.undefineCalls, domain.undefineNVRAMCalls, domain.undefineTPMCalls)
	}
	if domain.undefineNVRAMTPMCalls != 1 {
		t.Fatalf("undefineNVRAMTPMCalls = %d", domain.undefineNVRAMTPMCalls)
	}
}

func TestCleanupTreatsMissingDomainAsAlreadyCleaned(t *testing.T) {
	domain := &fakeDomain{name: "test-vm", isActiveErr: ErrNotFound}
	err := New(nil).Cleanup(domain, CleanupOptions{HasNVRAM: true})
	if err != nil {
		t.Fatalf("Cleanup returned error: %v", err)
	}
	if domain.destroyCalls != 0 || domain.undefineCalls != 0 || domain.undefineNVRAMCalls != 0 {
		t.Fatalf("cleanup calls destroy=%d undefine=%d nvram=%d", domain.destroyCalls, domain.undefineCalls, domain.undefineNVRAMCalls)
	}
}

func TestCloseConnection(t *testing.T) {
	conn := &fakeConnection{domains: map[string]*fakeDomain{}}
	if err := New(conn).Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}
	if !conn.closed {
		t.Fatalf("closed = false")
	}
}

func TestEnsureStoragePoolUsesExistingActivePool(t *testing.T) {
	conn := &fakeConnection{
		domains: map[string]*fakeDomain{},
		pools: map[string]*fakeStoragePool{
			"redfish": {name: "redfish", active: true},
		},
	}
	pool, err := New(conn).EnsureStoragePool(StoragePoolRequest{Name: "redfish", TargetPath: "/var/lib/redfish"})
	if err != nil {
		t.Fatalf("EnsureStoragePool returned error: %v", err)
	}
	if pool.Name() != "redfish" {
		t.Fatalf("pool name = %q", pool.Name())
	}
	if conn.definePoolCalls != 0 {
		t.Fatalf("definePoolCalls = %d", conn.definePoolCalls)
	}
	if pool.(*fakeStoragePool).createCalls != 0 {
		t.Fatalf("createCalls = %d", pool.(*fakeStoragePool).createCalls)
	}
}

func TestEnsureStoragePoolDefinesAndStartsMissingPool(t *testing.T) {
	conn := &fakeConnection{domains: map[string]*fakeDomain{}, pools: map[string]*fakeStoragePool{}}
	pool, err := New(conn).EnsureStoragePool(StoragePoolRequest{Name: "redfish", TargetPath: "/var/lib/redfish"})
	if err != nil {
		t.Fatalf("EnsureStoragePool returned error: %v", err)
	}
	if conn.definePoolCalls != 1 {
		t.Fatalf("definePoolCalls = %d", conn.definePoolCalls)
	}
	if !strings.Contains(conn.definedPoolXML, "<name>redfish</name>") || !strings.Contains(conn.definedPoolXML, "<path>/var/lib/redfish</path>") {
		t.Fatalf("definedPoolXML = %s", conn.definedPoolXML)
	}
	if pool.(*fakeStoragePool).createCalls != 1 {
		t.Fatalf("createCalls = %d", pool.(*fakeStoragePool).createCalls)
	}
}

type fakeConnection struct {
	domains         map[string]*fakeDomain
	pools           map[string]*fakeStoragePool
	defineCalls     int
	definedXML      string
	defineNil       bool
	definePoolCalls int
	definedPoolXML  string
	closed          bool
}

func (c *fakeConnection) LookupDomain(name string) (Domain, error) {
	if domain, ok := c.domains[name]; ok && !domain.undefined {
		return domain, nil
	}
	return nil, ErrNotFound
}

func (c *fakeConnection) DefineDomain(xml string) (Domain, error) {
	c.defineCalls++
	c.definedXML = xml
	if c.defineNil {
		return nil, nil
	}
	domain := &fakeDomain{name: "test-vm"}
	c.domains[domain.name] = domain
	return domain, nil
}

func (c *fakeConnection) Close() error {
	c.closed = true
	return nil
}

func (c *fakeConnection) LookupStoragePool(name string) (StoragePool, error) {
	if pool, ok := c.pools[name]; ok {
		return pool, nil
	}
	return nil, ErrNotFound
}

func (c *fakeConnection) DefineStoragePool(xml string) (StoragePool, error) {
	c.definePoolCalls++
	c.definedPoolXML = xml
	pool := &fakeStoragePool{name: "redfish"}
	if c.pools == nil {
		c.pools = map[string]*fakeStoragePool{}
	}
	c.pools[pool.name] = pool
	return pool, nil
}

type fakeDomain struct {
	name                  string
	xmlText               string
	active                bool
	createCalls           int
	destroyCalls          int
	shutdownCalls         int
	undefineCalls         int
	undefineNVRAMCalls    int
	undefineNVRAMTPMCalls int
	undefineTPMCalls      int
	createErr             error
	isActiveErr           error
	undefined             bool
}

func (d *fakeDomain) Name() string {
	return d.name
}

func (d *fakeDomain) XML() (string, error) {
	return d.xmlText, nil
}

func (d *fakeDomain) IsActive() (bool, error) {
	if d.isActiveErr != nil {
		return false, d.isActiveErr
	}
	return d.active, nil
}

func (d *fakeDomain) Create() error {
	d.createCalls++
	if d.createErr != nil {
		return d.createErr
	}
	d.active = true
	return nil
}

func (d *fakeDomain) Shutdown() error {
	d.shutdownCalls++
	d.active = false
	return nil
}

func (d *fakeDomain) Destroy() error {
	d.destroyCalls++
	d.active = false
	return nil
}

func (d *fakeDomain) Undefine() error {
	d.undefineCalls++
	d.undefined = true
	return nil
}

func (d *fakeDomain) UndefineNVRAM() error {
	d.undefineNVRAMCalls++
	d.undefined = true
	return nil
}

func (d *fakeDomain) UndefineNVRAMTPM() error {
	d.undefineNVRAMTPMCalls++
	d.undefined = true
	return nil
}

func (d *fakeDomain) UndefineTPM() error {
	d.undefineTPMCalls++
	d.undefined = true
	return nil
}

func managedDomainXML(name string) string {
	return `<domain><name>` + name + `</name><metadata><dvr:managed xmlns:dvr="https://github.com/munenick/docker-qemu/v2">true</dvr:managed><dvr:vm-name xmlns:dvr="https://github.com/munenick/docker-qemu/v2">` + name + `</dvr:vm-name></metadata></domain>`
}

type fakeStoragePool struct {
	name        string
	active      bool
	createCalls int
}

func (p *fakeStoragePool) Name() string {
	return p.name
}

func (p *fakeStoragePool) IsActive() (bool, error) {
	return p.active, nil
}

func (p *fakeStoragePool) Create() error {
	p.createCalls++
	p.active = true
	return nil
}
