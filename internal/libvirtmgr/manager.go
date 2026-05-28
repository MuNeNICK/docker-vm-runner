package libvirtmgr

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strings"
)

var ErrNotFound = errors.New("libvirt domain not found")

const runnerMetadataNamespace = "https://github.com/munenick/docker-qemu/v2"

type Connection interface {
	LookupDomain(name string) (Domain, error)
	DefineDomain(xml string) (Domain, error)
	Close() error
}

type StorageConnection interface {
	LookupStoragePool(name string) (StoragePool, error)
	DefineStoragePool(xml string) (StoragePool, error)
}

type Domain interface {
	Name() string
	XML() (string, error)
	IsActive() (bool, error)
	Create() error
	Shutdown() error
	Destroy() error
	Undefine() error
	UndefineNVRAM() error
}

type StoragePool interface {
	Name() string
	IsActive() (bool, error)
	Create() error
}

type Manager struct {
	Conn Connection
}

type CleanupOptions struct {
	HasNVRAM bool
}

type StoragePoolRequest struct {
	Name       string
	TargetPath string
}

func New(conn Connection) *Manager {
	return &Manager{Conn: conn}
}

func (m *Manager) EnsureDefined(name string, xml string) (Domain, error) {
	if m.Conn == nil {
		return nil, fmt.Errorf("libvirt connection not established")
	}
	if err := m.ReconcileStaleDomain(name); err != nil {
		return nil, err
	}
	domain, err := m.Conn.LookupDomain(name)
	if err == nil {
		return nil, fmt.Errorf("libvirt domain %s still exists after stale cleanup", name)
	}
	if !errors.Is(err, ErrNotFound) {
		return nil, fmt.Errorf("lookup libvirt domain %s: %w", name, err)
	}
	domain, err = m.Conn.DefineDomain(xml)
	if err != nil {
		return nil, fmt.Errorf("define libvirt domain %s: %w", name, err)
	}
	if domain == nil {
		return nil, fmt.Errorf("failed to define libvirt domain %s", name)
	}
	return domain, nil
}

func (m *Manager) ReconcileStaleDomain(name string) error {
	if m.Conn == nil {
		return fmt.Errorf("libvirt connection not established")
	}
	domain, err := m.Conn.LookupDomain(name)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		return fmt.Errorf("lookup libvirt domain %s: %w", name, err)
	}
	inspection, err := inspectDomain(domain, name)
	if err != nil {
		return err
	}
	if !inspection.Metadata.Managed || inspection.Metadata.VMName != name {
		return fmt.Errorf("libvirt domain %s already exists and is not managed by docker-vm-runner", name)
	}
	if err := m.Cleanup(domain, CleanupOptions{HasNVRAM: inspection.HasNVRAM}); err != nil {
		return fmt.Errorf("cleanup stale libvirt domain %s: %w", name, err)
	}
	return nil
}

type domainInspection struct {
	Metadata RunnerMetadata
	HasNVRAM bool
}

func inspectDomain(domain Domain, name string) (domainInspection, error) {
	xmlText, err := domain.XML()
	if err != nil {
		return domainInspection{}, fmt.Errorf("read libvirt domain %s XML: %w", name, err)
	}
	metadata, err := ParseRunnerMetadata(xmlText)
	if err != nil {
		return domainInspection{}, fmt.Errorf("parse libvirt domain %s metadata: %w", name, err)
	}
	return domainInspection{Metadata: metadata, HasNVRAM: domainHasNVRAM(xmlText)}, nil
}

type RunnerMetadata struct {
	Managed bool
	VMName  string
}

func domainHasNVRAM(xmlText string) bool {
	decoder := xml.NewDecoder(strings.NewReader(xmlText))
	for {
		token, err := decoder.Token()
		if err != nil {
			return false
		}
		start, ok := token.(xml.StartElement)
		if ok && start.Name.Local == "nvram" {
			return true
		}
	}
}

func ParseRunnerMetadata(xmlText string) (RunnerMetadata, error) {
	decoder := xml.NewDecoder(strings.NewReader(xmlText))
	var metadata RunnerMetadata
	for {
		token, err := decoder.Token()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return metadata, nil
			}
			return RunnerMetadata{}, err
		}
		start, ok := token.(xml.StartElement)
		if !ok || start.Name.Space != runnerMetadataNamespace {
			continue
		}
		var value string
		if err := decoder.DecodeElement(&value, &start); err != nil {
			return RunnerMetadata{}, err
		}
		switch start.Name.Local {
		case "managed":
			metadata.Managed = strings.EqualFold(strings.TrimSpace(value), "true")
		case "vm-name":
			metadata.VMName = strings.TrimSpace(value)
		}
	}
}

func (m *Manager) Start(domain Domain) error {
	if domain == nil {
		return fmt.Errorf("domain not defined")
	}
	active, err := domain.IsActive()
	if err != nil {
		return fmt.Errorf("check domain active state: %w", err)
	}
	if active {
		return nil
	}
	if err := domain.Create(); err != nil {
		message := err.Error()
		if strings.Contains(strings.ToLower(message), "cgroup") {
			return fmt.Errorf("libvirt could not access host cgroups: %s\nRun the container with --cgroupns=host to fix this", message)
		}
		return fmt.Errorf("failed to start domain: %w", err)
	}
	return nil
}

func (m *Manager) Cleanup(domain Domain, opts CleanupOptions) error {
	if domain == nil {
		return nil
	}
	active, err := domain.IsActive()
	if err == nil && active {
		if err := domain.Destroy(); err != nil {
			return fmt.Errorf("destroy libvirt domain %s: %w", domain.Name(), err)
		}
	}
	if err != nil {
		return fmt.Errorf("check domain active state: %w", err)
	}
	if opts.HasNVRAM {
		if err := domain.UndefineNVRAM(); err != nil {
			return fmt.Errorf("undefine libvirt domain %s with nvram: %w", domain.Name(), err)
		}
		return nil
	}
	if err := domain.Undefine(); err != nil {
		return fmt.Errorf("undefine libvirt domain %s: %w", domain.Name(), err)
	}
	return nil
}

func (m *Manager) Close() error {
	if m.Conn == nil {
		return nil
	}
	if err := m.Conn.Close(); err != nil {
		return fmt.Errorf("close libvirt connection: %w", err)
	}
	return nil
}

func (m *Manager) EnsureStoragePool(req StoragePoolRequest) (StoragePool, error) {
	conn, ok := m.Conn.(StorageConnection)
	if !ok || conn == nil {
		return nil, fmt.Errorf("libvirt connection does not support storage pools")
	}
	pool, err := conn.LookupStoragePool(req.Name)
	if err != nil {
		if !errors.Is(err, ErrNotFound) {
			return nil, fmt.Errorf("lookup libvirt storage pool %s: %w", req.Name, err)
		}
		pool, err = conn.DefineStoragePool(storagePoolXML(req))
		if err != nil {
			return nil, fmt.Errorf("define libvirt storage pool %s: %w", req.Name, err)
		}
		if pool == nil {
			return nil, fmt.Errorf("failed to define libvirt storage pool %s", req.Name)
		}
	}
	active, err := pool.IsActive()
	if err != nil {
		return nil, fmt.Errorf("check libvirt storage pool %s active state: %w", req.Name, err)
	}
	if !active {
		if err := pool.Create(); err != nil {
			return nil, fmt.Errorf("start libvirt storage pool %s: %w", req.Name, err)
		}
	}
	return pool, nil
}

func storagePoolXML(req StoragePoolRequest) string {
	return fmt.Sprintf(`<pool type="dir"><name>%s</name><target><path>%s</path></target></pool>`, req.Name, req.TargetPath)
}
