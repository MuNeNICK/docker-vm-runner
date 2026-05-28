package seediso

import (
	"fmt"
	"path"

	"github.com/munenick/docker-vm-runner/internal/filesystems"
	"gopkg.in/yaml.v3"
)

type CloudInitRequest struct {
	VMName       string
	LoginUser    string
	LoginShell   string
	Password     string
	PasswordHash func(string) (string, error)
	SSHPubKey    string
	UserData     string
	Filesystems  []FilesystemMount
}

type FilesystemMount struct {
	Tag      string
	Driver   string
	Readonly bool
}

type SeedContent struct {
	MetaData   string
	UserData   string
	VendorData string
}

func BuildCloudInit(req CloudInitRequest) (SeedContent, error) {
	if req.PasswordHash == nil {
		return SeedContent{}, fmt.Errorf("password hash function is required")
	}
	passwordHash, err := req.PasswordHash(req.Password)
	if err != nil {
		return SeedContent{}, fmt.Errorf("hash cloud-init password: %w", err)
	}

	user := map[string]any{
		"name":        req.LoginUser,
		"lock_passwd": false,
		"sudo":        "ALL=(ALL) NOPASSWD:ALL",
		"shell":       req.LoginShell,
		"passwd":      passwordHash,
	}
	if req.SSHPubKey != "" {
		user["ssh_authorized_keys"] = []string{req.SSHPubKey}
	}

	runcmd := []any{
		[]string{"sh", "-c", "command -v semanage >/dev/null 2>&1 && semanage permissive -a virt_qemu_ga_t || true"},
		[]string{"sh", "-c", "command -v systemctl >/dev/null 2>&1 && systemctl enable qemu-guest-agent && systemctl restart qemu-guest-agent || true"},
		[]string{"sh", "-c", "command -v rc-update >/dev/null 2>&1 && rc-update add qemu-guest-agent default && rc-service qemu-guest-agent restart || true"},
	}

	vendor := map[string]any{
		"packages":   []string{"qemu-guest-agent"},
		"users":      []any{user},
		"chpasswd":   map[string]any{"expire": false},
		"ssh_pwauth": true,
		"write_files": []map[string]string{
			{
				"path":    "/etc/sysconfig/qemu-ga",
				"content": "# Managed by docker-vm-runner\nBLACKLIST_RPC=\n",
			},
			{
				"path": "/etc/conf.d/qemu-guest-agent",
				"content": "# Managed by docker-vm-runner\n" +
					"# Auto-detect virtio guest agent port\n" +
					"GA_PATH=\"$(find /dev -name 'vport*p1' 2>/dev/null | head -1)\"\n",
			},
		},
		"runcmd": runcmd,
	}

	mounts := buildMounts(req.Filesystems, &runcmd)
	if len(mounts) > 0 {
		vendor["mounts"] = mounts
		vendor["runcmd"] = runcmd
	}

	vendorYAML, err := yaml.Marshal(vendor)
	if err != nil {
		return SeedContent{}, fmt.Errorf("marshal vendor-data: %w", err)
	}
	return SeedContent{
		MetaData:   fmt.Sprintf("instance-id: iid-%s\nlocal-hostname: %s\n", req.VMName, req.VMName),
		UserData:   req.UserData,
		VendorData: "#cloud-config\n" + string(vendorYAML),
	}, nil
}

func buildMounts(items []FilesystemMount, runcmd *[]any) [][]string {
	var mounts [][]string
	for _, fs := range items {
		target := filesystems.SanitizeMountTarget(fs.Tag)
		mountDir := path.Join("/mnt", target)
		*runcmd = append(*runcmd, []string{"mkdir", "-p", mountDir})
		fstype := "virtiofs"
		options := "defaults,_netdev"
		if fs.Driver != "virtiofs" {
			fstype = "9p"
			options = "trans=virtio,version=9p2000.L,_netdev"
		}
		if fs.Readonly {
			options += ",ro"
		}
		mounts = append(mounts, []string{fs.Tag, mountDir, fstype, options, "0", "0"})
	}
	return mounts
}

type GenISORequest struct {
	OutputPath string
	MetaData   string
	UserData   string
	VendorData string
}

func GenISOCommand(req GenISORequest) []string {
	return []string{
		"genisoimage",
		"-output", req.OutputPath,
		"-volid", "cidata",
		"-joliet",
		"-rock",
		req.MetaData,
		req.UserData,
		req.VendorData,
	}
}
