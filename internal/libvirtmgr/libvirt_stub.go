//go:build !libvirt

package libvirtmgr

import "fmt"

func Open(uri string) (*Manager, error) {
	return nil, fmt.Errorf("libvirt support is not built in; rebuild with -tags libvirt to open %s", uri)
}
