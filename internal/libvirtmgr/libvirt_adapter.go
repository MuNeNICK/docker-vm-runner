//go:build libvirt

package libvirtmgr

import (
	"errors"
	"fmt"

	libvirt "libvirt.org/go/libvirt"
)

func Open(uri string) (*Manager, error) {
	conn, err := libvirt.NewConnect(uri)
	if err != nil {
		return nil, fmt.Errorf("open libvirt connection %s: %w", uri, err)
	}
	return New(&LibvirtConnection{conn: conn}), nil
}

type LibvirtConnection struct {
	conn *libvirt.Connect
}

func (c *LibvirtConnection) LookupDomain(name string) (Domain, error) {
	domain, err := c.conn.LookupDomainByName(name)
	if err != nil {
		if isLibvirtCode(err, libvirt.ERR_NO_DOMAIN) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &LibvirtDomain{domain: domain}, nil
}

func (c *LibvirtConnection) DefineDomain(xml string) (Domain, error) {
	domain, err := c.conn.DomainDefineXML(xml)
	if err != nil {
		return nil, err
	}
	return &LibvirtDomain{domain: domain}, nil
}

func (c *LibvirtConnection) LookupStoragePool(name string) (StoragePool, error) {
	pool, err := c.conn.LookupStoragePoolByName(name)
	if err != nil {
		if isLibvirtCode(err, libvirt.ERR_NO_STORAGE_POOL) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &LibvirtStoragePool{pool: pool}, nil
}

func (c *LibvirtConnection) DefineStoragePool(xml string) (StoragePool, error) {
	pool, err := c.conn.StoragePoolDefineXML(xml, 0)
	if err != nil {
		return nil, err
	}
	return &LibvirtStoragePool{pool: pool}, nil
}

func (c *LibvirtConnection) Close() error {
	_, err := c.conn.Close()
	return err
}

type LibvirtDomain struct {
	domain *libvirt.Domain
}

func (d *LibvirtDomain) Name() string {
	name, err := d.domain.GetName()
	if err != nil {
		return ""
	}
	return name
}

func (d *LibvirtDomain) XML() (string, error) {
	return d.domain.GetXMLDesc(0)
}

func (d *LibvirtDomain) IsActive() (bool, error) {
	return d.domain.IsActive()
}

func (d *LibvirtDomain) Create() error {
	return d.domain.Create()
}

func (d *LibvirtDomain) Shutdown() error {
	return d.domain.Shutdown()
}

func (d *LibvirtDomain) Destroy() error {
	return d.domain.Destroy()
}

func (d *LibvirtDomain) Undefine() error {
	return d.domain.Undefine()
}

func (d *LibvirtDomain) UndefineNVRAM() error {
	return d.domain.UndefineFlags(libvirt.DOMAIN_UNDEFINE_NVRAM)
}

func (d *LibvirtDomain) UndefineNVRAMTPM() error {
	return d.domain.UndefineFlags(libvirt.DOMAIN_UNDEFINE_NVRAM | libvirt.DOMAIN_UNDEFINE_TPM)
}

func (d *LibvirtDomain) UndefineTPM() error {
	return d.domain.UndefineFlags(libvirt.DOMAIN_UNDEFINE_TPM)
}

type LibvirtStoragePool struct {
	pool *libvirt.StoragePool
}

func (p *LibvirtStoragePool) Name() string {
	name, err := p.pool.GetName()
	if err != nil {
		return ""
	}
	return name
}

func (p *LibvirtStoragePool) IsActive() (bool, error) {
	return p.pool.IsActive()
}

func (p *LibvirtStoragePool) Create() error {
	return p.pool.Create(libvirt.STORAGE_POOL_CREATE_NORMAL)
}

func isLibvirtCode(err error, code libvirt.ErrorNumber) bool {
	var libvirtErr libvirt.Error
	if errors.As(err, &libvirtErr) {
		return libvirtErr.Code == code
	}
	return false
}
