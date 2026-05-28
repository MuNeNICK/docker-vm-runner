package filesystems

import (
	"encoding/xml"
	"strings"
	"testing"
)

func TestRenderVirtioFS(t *testing.T) {
	doc, err := RenderXML(Share{
		Source:     "/host/share",
		Target:     "share",
		Driver:     "virtiofs",
		AccessMode: "passthrough",
	})
	if err != nil {
		t.Fatalf("RenderXML returned error: %v", err)
	}
	requireXMLElement(t, doc, "filesystem", map[string]string{"type": "mount", "accessmode": "passthrough"})
	requireXMLElement(t, doc, "driver", map[string]string{"type": "virtiofs"})
	requireXMLElement(t, doc, "binary", map[string]string{"path": "/usr/lib/qemu/virtiofsd"})
	requireXMLElement(t, doc, "source", map[string]string{"dir": "/host/share"})
	requireXMLElement(t, doc, "target", map[string]string{"dir": "share"})
}

func TestRender9P(t *testing.T) {
	doc, err := RenderXML(Share{
		Source:     "/host/share",
		Target:     "share",
		Driver:     "9p",
		AccessMode: "mapped",
		Readonly:   true,
	})
	if err != nil {
		t.Fatalf("RenderXML returned error: %v", err)
	}
	requireXMLElement(t, doc, "filesystem", map[string]string{"type": "mount", "accessmode": "mapped"})
	requireXMLElement(t, doc, "driver", map[string]string{"type": "path"})
	requireXMLElement(t, doc, "readonly", map[string]string{})
	if strings.Contains(doc, "virtiofsd") {
		t.Fatalf("9p XML should not include virtiofsd binary:\n%s", doc)
	}
}

func TestRenderEscapesAttributeValues(t *testing.T) {
	source := `/host/a&b<"share"`
	target := `target&<"dir"`
	doc, err := RenderXML(Share{
		Source:     source,
		Target:     target,
		Driver:     "virtiofs",
		AccessMode: "passthrough",
	})
	if err != nil {
		t.Fatalf("RenderXML returned error: %v", err)
	}
	requireXMLElement(t, doc, "source", map[string]string{"dir": source})
	requireXMLElement(t, doc, "target", map[string]string{"dir": target})
}

func TestRenderRejectsMissingFields(t *testing.T) {
	if _, err := RenderXML(Share{Target: "share", Driver: "virtiofs", AccessMode: "passthrough"}); err == nil {
		t.Fatal("expected source error")
	}
	if _, err := RenderXML(Share{Source: "/host", Driver: "virtiofs", AccessMode: "passthrough"}); err == nil {
		t.Fatal("expected target error")
	}
}

func requireXMLElement(t *testing.T, doc string, name string, attrs map[string]string) {
	t.Helper()
	decoder := xml.NewDecoder(strings.NewReader(doc))
	for {
		token, err := decoder.Token()
		if err != nil {
			if err.Error() == "EOF" {
				t.Fatalf("missing <%s> with attrs %#v in:\n%s", name, attrs, doc)
			}
			t.Fatalf("decode XML: %v", err)
		}
		start, ok := token.(xml.StartElement)
		if !ok || start.Name.Local != name {
			continue
		}
		got := map[string]string{}
		for _, attr := range start.Attr {
			got[attr.Name.Local] = attr.Value
		}
		match := true
		for key, value := range attrs {
			if got[key] != value {
				match = false
				break
			}
		}
		if match {
			return
		}
	}
}
