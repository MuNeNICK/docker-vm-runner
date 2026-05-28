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
