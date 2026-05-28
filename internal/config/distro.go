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

type DistroSummary struct {
	Key  string
	Name string
	Arch string
	User string
}

func ListDistros(path string, archFilter string) ([]DistroSummary, string, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, "", fmt.Errorf("distribution config missing: %s", path)
		}
		return nil, "", fmt.Errorf("read distribution config %s: %w", path, err)
	}
	var file distroConfigFile
	if err := yaml.Unmarshal(content, &file); err != nil {
		return nil, "", fmt.Errorf("parse distribution config %s: %w", path, err)
	}
	if len(file.Distributions) == 0 {
		return nil, "", nil
	}
	normalizedArch := ""
	if strings.TrimSpace(archFilter) != "" {
		arch, err := NormalizeArchitecture(archFilter)
		if err != nil {
			return nil, "", err
		}
		normalizedArch = arch
	}
	summaries := make([]DistroSummary, 0, len(file.Distributions))
	for key, distro := range file.Distributions {
		arch := distro.Arch
		if strings.TrimSpace(arch) == "" {
			arch = "x86_64"
		}
		resolvedArch, err := NormalizeArchitecture(arch)
		if err != nil {
			return nil, "", fmt.Errorf("distribution %q declares unsupported arch %q: %w", key, arch, err)
		}
		if normalizedArch != "" && resolvedArch != normalizedArch {
			continue
		}
		name := distro.Name
		if name == "" {
			name = key
		}
		user := distro.User
		if user == "" {
			user = "user"
		}
		summaries = append(summaries, DistroSummary{Key: key, Name: name, Arch: resolvedArch, User: user})
	}
	sort.Slice(summaries, func(i, j int) bool { return summaries[i].Key < summaries[j].Key })
	return summaries, normalizedArch, nil
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
