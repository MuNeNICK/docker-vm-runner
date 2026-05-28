package filesystems

import (
	"fmt"
	"strings"
)

type Share struct {
	Source     string
	Target     string
	Driver     string
	AccessMode string
	Readonly   bool
}

func RenderXML(share Share) (string, error) {
	if strings.TrimSpace(share.Source) == "" {
		return "", fmt.Errorf("filesystem source is required")
	}
	if strings.TrimSpace(share.Target) == "" {
		return "", fmt.Errorf("filesystem target is required")
	}
	driverType := "path"
	if share.Driver == "virtiofs" {
		driverType = "virtiofs"
	}
	accessMode := share.AccessMode
	if accessMode == "" {
		accessMode = "passthrough"
	}

	var b strings.Builder
	writeStart(&b, "filesystem", fsAttr{"type", "mount"}, fsAttr{"accessmode", accessMode})
	writeEmpty(&b, "driver", fsAttr{"type", driverType})
	if share.Driver == "virtiofs" {
		writeEmpty(&b, "binary", fsAttr{"path", "/usr/lib/qemu/virtiofsd"})
	}
	writeEmpty(&b, "source", fsAttr{"dir", share.Source})
	writeEmpty(&b, "target", fsAttr{"dir", share.Target})
	if share.Readonly {
		writeEmpty(&b, "readonly")
	}
	writeEnd(&b, "filesystem")
	return b.String(), nil
}

type fsAttr struct {
	name  string
	value string
}

var attrEscaper = strings.NewReplacer(
	"&", "&amp;",
	"<", "&lt;",
	">", "&gt;",
	`"`, "&#34;",
	"'", "&#39;",
)

func writeStart(b *strings.Builder, name string, attrs ...fsAttr) {
	b.WriteByte('<')
	b.WriteString(name)
	writeFSAttrs(b, attrs)
	b.WriteByte('>')
}

func writeEnd(b *strings.Builder, name string) {
	b.WriteString("</")
	b.WriteString(name)
	b.WriteByte('>')
}

func writeEmpty(b *strings.Builder, name string, attrs ...fsAttr) {
	b.WriteByte('<')
	b.WriteString(name)
	writeFSAttrs(b, attrs)
	b.WriteString("/>")
}

func writeFSAttrs(b *strings.Builder, attrs []fsAttr) {
	for _, attr := range attrs {
		b.WriteByte(' ')
		b.WriteString(attr.name)
		b.WriteString(`="`)
		b.WriteString(escapeAttr(attr.value))
		b.WriteByte('"')
	}
}

func escapeAttr(value string) string {
	return attrEscaper.Replace(value)
}
