package catalog

import (
	"strings"
	"testing"
)

func TestLoadSupportedCatalog(t *testing.T) {
	response, err := Load(strings.NewReader(`{
	  "meta": {
	    "api_version": "v1",
	    "generated_at": "2026-05-27T07:24:24Z",
	    "count": 2
	  },
	  "images": [
	    {
	      "id": "ubuntu-24.04-server",
	      "name": "Ubuntu 24.04.4 LTS",
	      "category": "linux",
	      "distro": "ubuntu",
	      "codename": "Noble Numbat",
	      "version": "24.04.4",
	      "edition": "Server",
	      "arch": "amd64",
	      "release_type": "stable",
	      "url": "https://releases.ubuntu.com/24.04/ubuntu-24.04.4-live-server-amd64.iso",
	      "homepage": "https://ubuntu.com/",
	      "checksum": {
	        "algorithm": "sha256",
	        "value": "e907d92eeec9df64163a7e454cbc8d7755e8ddc7ed42f99dbc80c40f1a138433"
	      },
	      "eol": {
	        "standard": "2029-05-31",
	        "extended": "2036-05-31",
	        "is_rolling": false
	      },
	      "status": "supported"
	    },
	    {
	      "id": "fedora-42-workstation",
	      "name": "Fedora 42",
	      "category": "linux",
	      "distro": "fedora",
	      "version": "42",
	      "edition": "Workstation",
	      "arch": "amd64",
	      "release_type": "stable",
	      "url": "https://download.fedoraproject.org/fedora.iso",
	      "checksum": {
	        "algorithm": "sha256",
	        "value": "abc123"
	      },
	      "eol": {
	        "standard": "2026-05-13",
	        "is_rolling": false
	      },
	      "status": "supported"
	    }
	  ]
	}`))
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if response.Meta.APIVersion != "v1" || response.Meta.Count != 2 {
		t.Fatalf("Meta = %#v", response.Meta)
	}
	if len(response.Images) != 2 {
		t.Fatalf("image count = %d", len(response.Images))
	}
	image := response.Images[0]
	if image.ID != "ubuntu-24.04-server" || image.Checksum.Algorithm != "sha256" || image.EOL.Extended != "2036-05-31" {
		t.Fatalf("image = %#v", image)
	}
}

func TestLoadMalformedCatalogJSON(t *testing.T) {
	_, err := Load(strings.NewReader(`{"meta":`))
	if err == nil {
		t.Fatal("expected malformed JSON error")
	}
	if !strings.Contains(err.Error(), "parse catalog JSON") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadCatalogRequiresImageURL(t *testing.T) {
	_, err := Load(strings.NewReader(`{
	  "meta": {"api_version": "v1"},
	  "images": [
	    {
	      "id": "ubuntu",
	      "name": "Ubuntu",
	      "category": "linux",
	      "version": "24.04",
	      "arch": "amd64",
	      "status": "supported",
	      "eol": {"is_rolling": false}
	    }
	  ]
	}`))
	if err == nil {
		t.Fatal("expected missing URL error")
	}
	if !strings.Contains(err.Error(), "missing required field url") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestFilterCatalogImages(t *testing.T) {
	response := testResponse()

	tests := []struct {
		name  string
		query Query
		want  []string
	}{
		{name: "id", query: Query{ID: "ubuntu-24.04-server"}, want: []string{"ubuntu-24.04-server"}},
		{name: "category", query: Query{Category: "linux"}, want: []string{"ubuntu-24.04-server", "ubuntu-24.04-desktop", "ubuntu-26.04-server"}},
		{name: "distro", query: Query{Distro: "ubuntu"}, want: []string{"ubuntu-24.04-server", "ubuntu-24.04-desktop", "ubuntu-26.04-server"}},
		{name: "edition", query: Query{Edition: "server"}, want: []string{"ubuntu-24.04-server", "ubuntu-26.04-server"}},
		{name: "arch alias", query: Query{Arch: "x86_64"}, want: []string{"ubuntu-24.04-server", "ubuntu-24.04-desktop", "freebsd-14.3-dvd"}},
		{name: "release type", query: Query{ReleaseType: "stable"}, want: []string{"ubuntu-24.04-server", "ubuntu-24.04-desktop", "freebsd-14.3-dvd"}},
		{name: "status", query: Query{Status: "beta"}, want: []string{"ubuntu-26.04-server"}},
		{name: "combined", query: Query{Distro: "ubuntu", Edition: "server", Arch: "amd64", Status: "supported"}, want: []string{"ubuntu-24.04-server"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Filter(response.Images, tt.query)
			if ids(got) != strings.Join(tt.want, ",") {
				t.Fatalf("ids = %s want %s", ids(got), strings.Join(tt.want, ","))
			}
		})
	}
}

func TestSelectCatalogImage(t *testing.T) {
	response := testResponse()

	image, err := Select(response, Query{ID: "ubuntu-24.04-server"})
	if err != nil {
		t.Fatalf("Select returned error: %v", err)
	}
	if image.ID != "ubuntu-24.04-server" {
		t.Fatalf("image = %#v", image)
	}

	_, err = Select(response, Query{Distro: "missing"})
	if err == nil || !strings.Contains(err.Error(), "no catalog image matches") {
		t.Fatalf("missing err = %v", err)
	}

	_, err = Select(response, Query{Distro: "ubuntu"})
	if err == nil || !strings.Contains(err.Error(), "matched multiple images") {
		t.Fatalf("multiple err = %v", err)
	}
}

func testResponse() Response {
	return Response{
		Meta: Meta{APIVersion: "v1", Count: 4},
		Images: []Image{
			{
				ID:          "ubuntu-24.04-server",
				Name:        "Ubuntu 24.04 LTS",
				Category:    "linux",
				Distro:      "ubuntu",
				Version:     "24.04",
				Edition:     "Server",
				Arch:        "amd64",
				ReleaseType: "stable",
				URL:         "https://example.com/ubuntu-server.iso",
				Status:      "supported",
			},
			{
				ID:          "ubuntu-24.04-desktop",
				Name:        "Ubuntu 24.04 LTS",
				Category:    "linux",
				Distro:      "ubuntu",
				Version:     "24.04",
				Edition:     "Desktop",
				Arch:        "amd64",
				ReleaseType: "stable",
				URL:         "https://example.com/ubuntu-desktop.iso",
				Status:      "supported",
			},
			{
				ID:          "ubuntu-26.04-server",
				Name:        "Ubuntu 26.04 LTS",
				Category:    "linux",
				Distro:      "ubuntu",
				Version:     "26.04",
				Edition:     "Server",
				Arch:        "arm64",
				ReleaseType: "beta",
				URL:         "https://example.com/ubuntu-beta.iso",
				Status:      "beta",
			},
			{
				ID:          "freebsd-14.3-dvd",
				Name:        "FreeBSD 14.3",
				Category:    "bsd",
				Distro:      "freebsd",
				Version:     "14.3",
				Edition:     "DVD",
				Arch:        "x86_64",
				ReleaseType: "stable",
				URL:         "https://example.com/freebsd.iso",
				Status:      "supported",
			},
		},
	}
}

func ids(images []Image) string {
	values := make([]string, 0, len(images))
	for _, image := range images {
		values = append(values, image.ID)
	}
	return strings.Join(values, ",")
}
