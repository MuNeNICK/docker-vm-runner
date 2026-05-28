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
	Key       string
	Name      string
	Arch      string
	User      string
	ImageType string
}

type DistroListFilter struct {
	Arch      string
	ImageType string
	Search    string
}

func ListDistros(path string, archFilter string) ([]DistroSummary, string, error) {
	return ListDistrosFiltered(path, DistroListFilter{Arch: archFilter})
}

func ListDistrosFiltered(path string, filter DistroListFilter) ([]DistroSummary, string, error) {
	if _, _, err := validateDistroListFilter(filter); err != nil {
		return nil, "", err
	}
	response, err := loadCatalog(path)
	if err != nil {
		return nil, "", err
	}
	return listDistrosFromResponse(response, filter)
}

func ListDistrosFromSource(source catalog.SourceOptions, filter DistroListFilter) ([]DistroSummary, string, error) {
	if _, _, err := validateDistroListFilter(filter); err != nil {
		return nil, "", err
	}
	response, err := catalog.LoadSource(source)
	if err != nil {
		return nil, "", err
	}
	return listDistrosFromResponse(response, filter)
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

func ResolveCatalogSource(env MapEnv, configPath string) (catalog.SourceOptions, error) {
	cachePath := strings.TrimSpace(env.Get("CATALOG_CACHE", configPath))
	if cachePath == "" {
		cachePath = paths.DefaultConfigPath
	}
	offline, err := env.Bool("CATALOG_OFFLINE", false)
	if err != nil {
		return catalog.SourceOptions{}, err
	}
	sourceURL, urlSet := env.Lookup("CATALOG_URL")
	_, cacheSet := env.Lookup("CATALOG_CACHE")
	_, offlineSet := env.Lookup("CATALOG_OFFLINE")
	if !urlSet && !cacheSet && !offlineSet && configPath != "" && configPath != paths.DefaultConfigPath {
		return catalog.SourceOptions{CachePath: configPath, Offline: true}, nil
	}
	if !urlSet {
		sourceURL = catalog.DefaultURL
	}
	return catalog.SourceOptions{URL: strings.TrimSpace(sourceURL), CachePath: cachePath, Offline: offline}, nil
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
		return DistroConfig{}, fmt.Errorf("Unknown catalog image %q. Available image IDs include: %s. Use --list-distros --search %q to search the catalog", distro, summarizeIDs(available, 12), distro)
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

func listDistrosFromResponse(response catalog.Response, filter DistroListFilter) ([]DistroSummary, string, error) {
	normalizedArch, normalizedImageType, err := validateDistroListFilter(filter)
	if err != nil {
		return nil, "", err
	}
	searchTerms := strings.Fields(strings.ToLower(strings.TrimSpace(filter.Search)))
	summaries := make([]DistroSummary, 0, len(response.Images))
	for _, image := range response.Images {
		if !image.HasDirectDownloadURL() {
			continue
		}
		resolvedArch, err := NormalizeArchitecture(image.Arch)
		if err != nil {
			continue
		}
		if normalizedArch != "" && resolvedArch != normalizedArch {
			continue
		}
		if normalizedImageType != "" && normalizeImageTypeValue(image.ImageType) != normalizedImageType {
			continue
		}
		if len(searchTerms) > 0 && !matchesSearch(image, searchTerms) {
			continue
		}
		name := image.Name
		if name == "" {
			name = image.ID
		}
		summaries = append(summaries, DistroSummary{Key: image.ID, Name: name, Arch: displayArchitecture(resolvedArch), User: "user", ImageType: image.ImageType})
	}
	sort.Slice(summaries, func(i, j int) bool { return summaries[i].Key < summaries[j].Key })
	return summaries, displayArchitecture(normalizedArch), nil
}

func validateDistroListFilter(filter DistroListFilter) (string, string, error) {
	normalizedArch := ""
	if strings.TrimSpace(filter.Arch) != "" {
		arch, err := NormalizeArchitecture(filter.Arch)
		if err != nil {
			return "", "", err
		}
		normalizedArch = arch
	}
	normalizedImageType, err := normalizeImageTypeFilter(filter.ImageType)
	if err != nil {
		return "", "", err
	}
	return normalizedArch, normalizedImageType, nil
}

func displayArchitecture(arch string) string {
	switch arch {
	case "x86_64":
		return "amd64"
	case "aarch64":
		return "arm64"
	default:
		return arch
	}
}

func normalizeImageTypeFilter(raw string) (string, error) {
	value := normalizeImageTypeValue(raw)
	switch value {
	case "", "all", "cloud-image", "iso", "disk-image":
		if value == "all" {
			return "", nil
		}
		return value, nil
	default:
		return "", fmt.Errorf("unsupported image type %q; use cloud-image, iso, or disk-image", raw)
	}
}

func normalizeImageTypeValue(raw string) string {
	value := strings.ToLower(strings.TrimSpace(raw))
	value = strings.ReplaceAll(value, "_", "-")
	value = strings.ReplaceAll(value, " ", "-")
	switch value {
	case "cloud":
		return "cloud-image"
	case "disk":
		return "disk-image"
	default:
		return value
	}
}

func matchesSearch(image catalog.Image, terms []string) bool {
	haystack := strings.ToLower(strings.Join([]string{
		image.ID,
		image.Name,
		image.ImageType,
		image.Category,
		image.Distro,
		image.Codename,
		image.Version,
		image.Edition,
		image.Arch,
		image.ReleaseType,
		image.Format,
		image.Status,
	}, " "))
	for _, term := range terms {
		if !strings.Contains(haystack, term) {
			return false
		}
	}
	return true
}

func summarizeIDs(ids []string, limit int) string {
	if len(ids) == 0 {
		return "none"
	}
	if limit < 1 || len(ids) <= limit {
		return strings.Join(ids, ", ")
	}
	return fmt.Sprintf("%s, ... (%d total)", strings.Join(ids[:limit], ", "), len(ids))
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
