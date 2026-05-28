package seediso

import (
	"strings"
	"testing"
)

func TestBuildCloudInitContent(t *testing.T) {
	content, err := BuildCloudInit(CloudInitRequest{
		VMName:       "test-vm",
		LoginUser:    "user",
		LoginShell:   "/bin/bash",
		Password:     "password",
		PasswordHash: func(string) (string, error) { return "HASH", nil },
		SSHPubKey:    "ssh-rsa AAAATEST",
		UserData:     "#cloud-config\nhostname: demo\n",
		Filesystems: []FilesystemMount{
			{Tag: "data", Driver: "virtiofs"},
			{Tag: "ro-share", Driver: "9p", Readonly: true},
		},
	})
	if err != nil {
		t.Fatalf("BuildCloudInit returned error: %v", err)
	}

	if !strings.Contains(content.MetaData, "instance-id: iid-test-vm") {
		t.Fatalf("MetaData missing instance id:\n%s", content.MetaData)
	}
	if !strings.Contains(content.MetaData, "local-hostname: test-vm") {
		t.Fatalf("MetaData missing hostname:\n%s", content.MetaData)
	}
	if content.UserData != "#cloud-config\nhostname: demo\n" {
		t.Fatalf("UserData = %q", content.UserData)
	}
	if !strings.HasPrefix(content.VendorData, "#cloud-config\n") {
		t.Fatalf("VendorData missing cloud-config header:\n%s", content.VendorData)
	}
	for _, want := range []string{
		"qemu-guest-agent",
		"name: user",
		"shell: /bin/bash",
		"passwd: HASH",
		"ssh_authorized_keys",
		"ssh-rsa AAAATEST",
		"path: /etc/sysconfig/qemu-ga",
		"path: /etc/conf.d/qemu-guest-agent",
		"semanage permissive -a virt_qemu_ga_t",
		"systemctl enable qemu-guest-agent",
		"rc-update add qemu-guest-agent default",
		"- data",
		"- /mnt/data",
		"- virtiofs",
		"defaults,_netdev",
		"- ro-share",
		"- /mnt/ro-share",
		"- 9p",
		"trans=virtio,version=9p2000.L,_netdev,ro",
	} {
		if !strings.Contains(content.VendorData, want) {
			t.Fatalf("VendorData missing %q:\n%s", want, content.VendorData)
		}
	}
}

func TestBuildCloudInitEmptyUserData(t *testing.T) {
	content, err := BuildCloudInit(CloudInitRequest{
		VMName:       "test-vm",
		LoginUser:    "user",
		LoginShell:   "/bin/bash",
		Password:     "password",
		PasswordHash: func(string) (string, error) { return "HASH", nil },
	})
	if err != nil {
		t.Fatalf("BuildCloudInit returned error: %v", err)
	}
	if content.UserData != "" {
		t.Fatalf("UserData = %q", content.UserData)
	}
}

func TestBuildCloudInitRequiresHasher(t *testing.T) {
	_, err := BuildCloudInit(CloudInitRequest{
		VMName:     "test-vm",
		LoginUser:  "user",
		LoginShell: "/bin/bash",
		Password:   "password",
	})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestGenISOCommand(t *testing.T) {
	cmd := GenISOCommand(GenISORequest{
		OutputPath: "/images/vm/seed.iso",
		MetaData:   "/tmp/meta-data",
		UserData:   "/tmp/user-data",
		VendorData: "/tmp/vendor-data",
	})
	want := []string{
		"genisoimage",
		"-output", "/images/vm/seed.iso",
		"-volid", "cidata",
		"-joliet",
		"-rock",
		"/tmp/meta-data",
		"/tmp/user-data",
		"/tmp/vendor-data",
	}
	if len(cmd) != len(want) {
		t.Fatalf("len = %d want %d: %#v", len(cmd), len(want), cmd)
	}
	for i := range want {
		if cmd[i] != want[i] {
			t.Fatalf("cmd[%d] = %q want %q\n%#v", i, cmd[i], want[i], cmd)
		}
	}
}
