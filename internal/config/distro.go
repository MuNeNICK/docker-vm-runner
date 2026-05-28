package config

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/munenick/docker-vm-runner/internal/catalog"
	"github.com/munenick/docker-vm-runner/internal/paths"
)

type DistroConfig struct {
	ID                string
	Name              string
	URL               string
	User              string
	Arch              string
	Shell             string
	ImageType         string
	SourceFormat      string
	SourceCompression string
	Category          string
	Distro            string
	Version           string
	Edition           string
	Status            string
	ChecksumAlgorithm string
	ChecksumValue     string
}

type DistroSummary struct {
	Key  string
	Name string
	Arch string
	User string
}

func ListDistros(path string, archFilter string) ([]DistroSummary, string, error) {
	response, err := loadCatalog(path)
	if err != nil {
		return nil, "", err
	}
	return listDistrosFromResponse(response, archFilter)
}

func ListDistrosFromSource(source catalog.SourceOptions, archFilter string) ([]DistroSummary, string, error) {
	response, err := catalog.LoadSource(source)
	if err != nil {
		return nil, "", err
	}
	return listDistrosFromResponse(response, archFilter)
}

func LoadDistroConfig(path string, distro string) (DistroConfig, error) {
	response, err := loadCatalog(path)
	if err != nil {
		return DistroConfig{}, err
	}
	return distroConfigFromResponse(response, distro)
}

func LoadDistroConfigFromSource(source catalog.SourceOptions, distro string) (DistroConfig, error) {
	response, err := catalog.LoadSource(source)
	if err != nil {
		return DistroConfig{}, err
	}
	return distroConfigFromResponse(response, distro)
}

func ResolveCatalogSource(env MapEnv, configPath string) catalog.SourceOptions {
	cachePath := strings.TrimSpace(env.Get("CATALOG_CACHE", configPath))
	if cachePath == "" {
		cachePath = paths.DefaultConfigPath
	}
	offline, _ := env.Bool("CATALOG_OFFLINE", false)
	sourceURL, urlSet := env.Lookup("CATALOG_URL")
	_, cacheSet := env.Lookup("CATALOG_CACHE")
	_, offlineSet := env.Lookup("CATALOG_OFFLINE")
	if !urlSet && !cacheSet && !offlineSet && configPath != "" && configPath != paths.DefaultConfigPath {
		return catalog.SourceOptions{CachePath: configPath, Offline: true}
	}
	if !urlSet {
		sourceURL = catalog.DefaultURL
	}
	return catalog.SourceOptions{URL: strings.TrimSpace(sourceURL), CachePath: cachePath, Offline: offline}
}

func distroConfigFromResponse(response catalog.Response, distro string) (DistroConfig, error) {
	image, err := catalog.Select(response, catalog.Query{ID: distro})
	if err != nil {
		available := make([]string, 0, len(response.Images))
		for _, image := range response.Images {
			if !image.HasDirectDownloadURL() {
				continue
			}
			available = append(available, image.ID)
		}
		sort.Strings(available)
		return DistroConfig{}, fmt.Errorf("Unknown catalog image '%s'. Available image IDs: %s", distro, strings.Join(available, ", "))
	}
	if !image.HasDirectDownloadURL() {
		if strings.TrimSpace(image.DownloadPage) != "" {
			return DistroConfig{}, fmt.Errorf("catalog image %q has no direct download URL; download page is %s", image.ID, image.DownloadPage)
		}
		return DistroConfig{}, fmt.Errorf("catalog image %q has no direct download URL", image.ID)
	}
	return distroConfigFromImage(image), nil
}

func loadCatalog(path string) (catalog.Response, error) {
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return catalog.Response{}, fmt.Errorf("catalog config missing: %s", path)
		}
		return catalog.Response{}, fmt.Errorf("read catalog config %s: %w", path, err)
	}
	defer file.Close()
	response, err := catalog.Load(file)
	if err != nil {
		return catalog.Response{}, fmt.Errorf("load catalog config %s: %w", path, err)
	}
	return response, nil
}

func listDistrosFromResponse(response catalog.Response, archFilter string) ([]DistroSummary, string, error) {
	normalizedArch := ""
	if strings.TrimSpace(archFilter) != "" {
		arch, err := NormalizeArchitecture(archFilter)
		if err != nil {
			return nil, "", err
		}
		normalizedArch = arch
	}
	summaries := make([]DistroSummary, 0, len(response.Images))
	for _, image := range response.Images {
		if !image.HasDirectDownloadURL() {
			continue
		}
		resolvedArch, err := NormalizeArchitecture(image.Arch)
		if err != nil {
			return nil, "", fmt.Errorf("catalog image %q declares unsupported arch %q: %w", image.ID, image.Arch, err)
		}
		if normalizedArch != "" && resolvedArch != normalizedArch {
			continue
		}
		name := image.Name
		if name == "" {
			name = image.ID
		}
		summaries = append(summaries, DistroSummary{Key: image.ID, Name: name, Arch: resolvedArch, User: "user"})
	}
	sort.Slice(summaries, func(i, j int) bool { return summaries[i].Key < summaries[j].Key })
	return summaries, normalizedArch, nil
}

func distroConfigFromImage(image catalog.Image) DistroConfig {
	return DistroConfig{
		ID:                image.ID,
		Name:              displayImageName(image),
		URL:               image.URL,
		User:              "user",
		Arch:              image.Arch,
		ImageType:         image.ImageType,
		SourceFormat:      image.Format,
		SourceCompression: image.Compression,
		Category:          image.Category,
		Distro:            image.Distro,
		Version:           image.Version,
		Edition:           image.Edition,
		Status:            image.Status,
		ChecksumAlgorithm: image.Checksum.Algorithm,
		ChecksumValue:     image.Checksum.Value,
	}
}

func displayImageName(image catalog.Image) string {
	if strings.TrimSpace(image.Edition) == "" {
		return image.Name
	}
	if strings.Contains(strings.ToLower(image.Name), strings.ToLower(image.Edition)) {
		return image.Name
	}
	return strings.TrimSpace(image.Name + " " + image.Edition)
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
