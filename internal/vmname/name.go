package vmname

import (
	"fmt"
	"regexp"
	"strings"
)

var containerIDPattern = regexp.MustCompile(`^[0-9a-f]{12,64}$`)
var safeNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)

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

func Validate(name string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("must not be empty")
	}
	if strings.TrimSpace(name) != name {
		return fmt.Errorf("must not contain leading or trailing whitespace")
	}
	if strings.ContainsAny(name, `/\`) {
		return fmt.Errorf("must not contain path separators")
	}
	if name == "." || name == ".." {
		return fmt.Errorf("must not be %q", name)
	}
	if !safeNamePattern.MatchString(name) {
		return fmt.Errorf("must start with an alphanumeric character and contain only letters, numbers, dot, underscore, or hyphen")
	}
	return nil
}
