package config

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

type DistroConfig struct {
	Name  string `yaml:"name"`
	URL   string `yaml:"url"`
	User  string `yaml:"user"`
	Arch  string `yaml:"arch"`
	Shell string `yaml:"shell"`
}

type distroConfigFile struct {
	Distributions map[string]DistroConfig `yaml:"distributions"`
}

func LoadDistroConfig(path string, distro string) (DistroConfig, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return DistroConfig{}, fmt.Errorf("distribution config missing: %s", path)
		}
		return DistroConfig{}, fmt.Errorf("read distribution config %s: %w", path, err)
	}

	var file distroConfigFile
	if err := yaml.Unmarshal(content, &file); err != nil {
		return DistroConfig{}, fmt.Errorf("parse distribution config %s: %w", path, err)
	}
	if len(file.Distributions) == 0 {
		return DistroConfig{}, fmt.Errorf("distribution config %s contains no distributions", path)
	}

	cfg, ok := file.Distributions[distro]
	if !ok {
		available := make([]string, 0, len(file.Distributions))
		for name := range file.Distributions {
			available = append(available, name)
		}
		sort.Strings(available)
		return DistroConfig{}, fmt.Errorf("Unknown distro '%s'. Available distributions: %s", distro, strings.Join(available, ", "))
	}
	if cfg.URL == "" {
		return DistroConfig{}, fmt.Errorf("distribution %q missing required field url", distro)
	}
	return cfg, nil
}

func NormalizeArchitecture(raw string) (string, error) {
	candidate := strings.ToLower(strings.TrimSpace(raw))
	if alias, ok := ArchitectureAliases[candidate]; ok {
		candidate = alias
	}
	if _, ok := SupportedArchitectures[candidate]; !ok {
		return "", fmt.Errorf("Unsupported ARCH '%s'", raw)
	}
	return candidate, nil
}

func ResolveArchitecture(distroArch string, override string) (string, error) {
	var overrideArch string
	if strings.TrimSpace(override) != "" {
		arch, err := NormalizeArchitecture(override)
		if err != nil {
			return "", err
		}
		overrideArch = arch
	}

	if strings.TrimSpace(distroArch) == "" {
		if overrideArch != "" {
			return overrideArch, nil
		}
		return "x86_64", nil
	}

	declaredArch, err := NormalizeArchitecture(distroArch)
	if err != nil {
		return "", fmt.Errorf("distribution declares unsupported arch %q: %w", distroArch, err)
	}
	if overrideArch != "" && overrideArch != declaredArch {
		return "", fmt.Errorf("ARCH=%q does not match distribution arch %q", override, distroArch)
	}
	return declaredArch, nil
}
