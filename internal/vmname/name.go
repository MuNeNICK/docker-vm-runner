package vmname

import (
	"regexp"
	"strings"
)

var containerIDPattern = regexp.MustCompile(`^[0-9a-f]{12,64}$`)

func Derive(distro string, isoMode bool, lookup func(string) (string, bool)) string {
	if value, ok := lookup("GUEST_NAME"); ok {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	if value, ok := lookup("HOSTNAME"); ok {
		candidate := strings.TrimSpace(value)
		if candidate != "" && !containerIDPattern.MatchString(candidate) {
			return candidate
		}
	}
	if isoMode {
		return "custom-vm"
	}
	return distro
}
