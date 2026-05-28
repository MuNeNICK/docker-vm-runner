package libvirtmgr

type Manager struct{}

func New() *Manager {
	return &Manager{}
}
