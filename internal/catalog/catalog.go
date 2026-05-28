package catalog

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

type Response struct {
	Meta   Meta    `json:"meta"`
	Images []Image `json:"images"`
}

type Meta struct {
	APIVersion  string `json:"api_version"`
	GeneratedAt string `json:"generated_at"`
	Count       int    `json:"count"`
}

type Image struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	ImageType    string   `json:"image_type"`
	Category     string   `json:"category"`
	Distro       string   `json:"distro"`
	Codename     string   `json:"codename"`
	Version      string   `json:"version"`
	Edition      string   `json:"edition"`
	Arch         string   `json:"arch"`
	ReleaseType  string   `json:"release_type"`
	Format       string   `json:"format"`
	Compression  string   `json:"compression"`
	URL          string   `json:"url"`
	DownloadPage string   `json:"download_page"`
	Homepage     string   `json:"homepage"`
	Notes        string   `json:"notes"`
	Checksum     Checksum `json:"checksum"`
	EOL          EOL      `json:"eol"`
	Status       string   `json:"status"`
}

type Checksum struct {
	Algorithm string `json:"algorithm"`
	Value     string `json:"value"`
}

type EOL struct {
	Standard  string `json:"standard"`
	Extended  string `json:"extended"`
	IsRolling bool   `json:"is_rolling"`
	Note      string `json:"note"`
}

type Query struct {
	ID          string
	Category    string
	Distro      string
	Edition     string
	Arch        string
	ReleaseType string
	Status      string
}

func Load(r io.Reader) (Response, error) {
	var response Response
	decoder := json.NewDecoder(r)
	if err := decoder.Decode(&response); err != nil {
		return Response{}, fmt.Errorf("parse catalog JSON: %w", err)
	}
	if strings.TrimSpace(response.Meta.APIVersion) == "" {
		return Response{}, fmt.Errorf("catalog meta missing api_version")
	}
	for idx, image := range response.Images {
		if err := validateImage(image); err != nil {
			return Response{}, fmt.Errorf("catalog image %d: %w", idx, err)
		}
	}
	return response, nil
}

func Filter(images []Image, query Query) []Image {
	var matches []Image
	for _, image := range images {
		if !matchesQuery(image, query) {
			continue
		}
		matches = append(matches, image)
	}
	return matches
}

func Select(response Response, query Query) (Image, error) {
	matches := Filter(response.Images, query)
	if len(matches) == 0 {
		return Image{}, fmt.Errorf("no catalog image matches query")
	}
	if len(matches) > 1 {
		ids := make([]string, 0, len(matches))
		for _, image := range matches {
			ids = append(ids, image.ID)
		}
		return Image{}, fmt.Errorf("catalog query matched multiple images: %s", strings.Join(ids, ", "))
	}
	return matches[0], nil
}

func matchesQuery(image Image, query Query) bool {
	if query.ID != "" && image.ID != query.ID {
		return false
	}
	if query.Category != "" && !equalFoldTrim(image.Category, query.Category) {
		return false
	}
	if query.Distro != "" && !equalFoldTrim(image.Distro, query.Distro) {
		return false
	}
	if query.Edition != "" && !equalFoldTrim(image.Edition, query.Edition) {
		return false
	}
	if query.Arch != "" && !sameArchitecture(image.Arch, query.Arch) {
		return false
	}
	if query.ReleaseType != "" && !equalFoldTrim(image.ReleaseType, query.ReleaseType) {
		return false
	}
	if query.Status != "" && !equalFoldTrim(image.Status, query.Status) {
		return false
	}
	return true
}

func validateImage(image Image) error {
	required := map[string]string{
		"id":       image.ID,
		"name":     image.Name,
		"category": image.Category,
		"version":  image.Version,
		"arch":     image.Arch,
		"status":   image.Status,
	}
	for field, value := range required {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("missing required field %s", field)
		}
	}
	if strings.TrimSpace(image.URL) == "" && strings.TrimSpace(image.DownloadPage) == "" {
		return fmt.Errorf("missing required field url or download_page")
	}
	return nil
}

func (image Image) HasDirectDownloadURL() bool {
	return strings.TrimSpace(image.URL) != ""
}

func equalFoldTrim(a string, b string) bool {
	return strings.EqualFold(strings.TrimSpace(a), strings.TrimSpace(b))
}

func sameArchitecture(a string, b string) bool {
	return normalizeCatalogArch(a) == normalizeCatalogArch(b)
}

func normalizeCatalogArch(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "amd64", "x86_64", "x64":
		return "amd64"
	case "arm64", "aarch64":
		return "arm64"
	default:
		return strings.ToLower(strings.TrimSpace(value))
	}
}
