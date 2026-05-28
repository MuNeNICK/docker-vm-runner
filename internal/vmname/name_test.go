package vmname

import "testing"

func TestDerive(t *testing.T) {
	tests := []struct {
		name    string
		env     map[string]string
		distro  string
		isoMode bool
		want    string
	}{
		{name: "explicit guest name", env: map[string]string{"GUEST_NAME": "my-vm"}, distro: "ubuntu", want: "my-vm"},
		{name: "hostname used", env: map[string]string{"HOSTNAME": "my-host"}, distro: "ubuntu", want: "my-host"},
		{name: "container id hostname ignored", env: map[string]string{"HOSTNAME": "aaaaaaaaaaaa"}, distro: "ubuntu", want: "ubuntu"},
		{name: "distro fallback", env: map[string]string{}, distro: "ubuntu", want: "ubuntu"},
		{name: "iso fallback", env: map[string]string{}, distro: "ubuntu", isoMode: true, want: "custom-vm"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Derive(tt.distro, tt.isoMode, func(key string) (string, bool) {
				value, ok := tt.env[key]
				return value, ok
			})
			if got != tt.want {
				t.Fatalf("Derive() = %q want %q", got, tt.want)
			}
		})
	}
}

func TestValidateRejectsUnsafeNames(t *testing.T) {
	for _, name := range []string{"", " ../x", "../x", "x/y", `x\y`, ".hidden", "-bad", "bad name"} {
		t.Run(name, func(t *testing.T) {
			if err := Validate(name); err == nil {
				t.Fatalf("Validate(%q) returned nil", name)
			}
		})
	}
}

func TestValidateAcceptsSafeNames(t *testing.T) {
	for _, name := range []string{"ubuntu-24.04-server", "my_vm", "VM.1"} {
		t.Run(name, func(t *testing.T) {
			if err := Validate(name); err != nil {
				t.Fatalf("Validate(%q) returned error: %v", name, err)
			}
		})
	}
}
