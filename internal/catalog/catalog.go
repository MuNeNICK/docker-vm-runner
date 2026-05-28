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
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Category    string   `json:"category"`
	Distro      string   `json:"distro"`
	Codename    string   `json:"codename"`
	Version     string   `json:"version"`
	Edition     string   `json:"edition"`
	Arch        string   `json:"arch"`
	ReleaseType string   `json:"release_type"`
	URL         string   `json:"url"`
	Homepage    string   `json:"homepage"`
	Checksum    Checksum `json:"checksum"`
	EOL         EOL      `json:"eol"`
	Status      string   `json:"status"`
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

func Load(r io.Reader) (Response, error) {
	var response Response
	decoder := json.NewDecoder(r)
	decoder.DisallowUnknownFields()
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

func validateImage(image Image) error {
	required := map[string]string{
		"id":       image.ID,
		"name":     image.Name,
		"category": image.Category,
		"version":  image.Version,
		"arch":     image.Arch,
		"status":   image.Status,
		"url":      image.URL,
	}
	for field, value := range required {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("missing required field %s", field)
		}
	}
	return nil
}
