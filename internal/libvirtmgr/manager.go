package libvirtmgr

import (
	"errors"
	"fmt"
	"strings"
)

var ErrNotFound = errors.New("libvirt domain not found")

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
	domain, err := m.Conn.LookupDomain(name)
	if err == nil {
		return domain, nil
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
