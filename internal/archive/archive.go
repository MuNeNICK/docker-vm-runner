package archive

import (
	"archive/tar"
	"archive/zip"
	"compress/bzip2"
	"compress/gzip"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/bodgit/sevenzip"
	"github.com/nwaples/rardecode/v2"
	"github.com/ulikunitz/xz"
)

type Extractor struct{}

func NewExtractor() *Extractor {
	return &Extractor{}
}

type ExtractResult struct {
	Path            string
	MemberName      string
	Fallback        bool
	SelectionReason string
}

func (e *Extractor) Extract(ctx context.Context, source string, destDir string) (string, error) {
	result, err := e.ExtractWithResult(ctx, source, destDir)
	if err != nil {
		return "", err
	}
	return result.Path, nil
}

func (e *Extractor) ExtractWithResult(ctx context.Context, source string, destDir string) (ExtractResult, error) {
	if err := ctx.Err(); err != nil {
		return ExtractResult{}, err
	}
	suffix := strings.ToLower(filepath.Ext(source))
	switch suffix {
	case ".gz":
		return extractStream(source, destDir, suffix, openGzip)
	case ".xz":
		return extractStream(source, destDir, suffix, openXZ)
	case ".bz2":
		return extractStream(source, destDir, suffix, openBzip2)
	case ".zip":
		return extractZip(source, destDir)
	case ".tar", ".ova":
		return extractTar(source, destDir)
	case ".7z":
		return extractSevenZip(source, destDir)
	case ".rar":
		return extractRAR(source, destDir)
	default:
		return ExtractResult{}, fmt.Errorf("unsupported compressed format: %s", source)
	}
}

type streamOpener func(*os.File) (io.ReadCloser, error)

func extractStream(source string, destDir string, suffix string, opener streamOpener) (ExtractResult, error) {
	input, err := os.Open(source)
	if err != nil {
		return ExtractResult{}, fmt.Errorf("open %s: %w", source, err)
	}
	defer input.Close()

	reader, err := opener(input)
	if err != nil {
		return ExtractResult{}, err
	}
	defer reader.Close()

	memberName := strings.TrimSuffix(filepath.Base(source), suffix)
	outputPath, err := safeJoin(destDir, memberName)
	if err != nil {
		return ExtractResult{}, err
	}
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		return ExtractResult{}, fmt.Errorf("create output directory: %w", err)
	}
	output, err := os.Create(outputPath)
	if err != nil {
		return ExtractResult{}, fmt.Errorf("create %s: %w", outputPath, err)
	}
	defer output.Close()
	if _, err := io.Copy(output, reader); err != nil {
		return ExtractResult{}, fmt.Errorf("extract %s: %w", source, err)
	}
	return ExtractResult{Path: outputPath, MemberName: memberName, SelectionReason: "single compressed stream"}, nil
}

func openGzip(file *os.File) (io.ReadCloser, error) {
	reader, err := gzip.NewReader(file)
	if err != nil {
		return nil, fmt.Errorf("open gzip stream: %w", err)
	}
	return reader, nil
}

func openXZ(file *os.File) (io.ReadCloser, error) {
	reader, err := xz.NewReader(file)
	if err != nil {
		return nil, fmt.Errorf("open xz stream: %w", err)
	}
	return io.NopCloser(reader), nil
}

func openBzip2(file *os.File) (io.ReadCloser, error) {
	return io.NopCloser(bzip2.NewReader(file)), nil
}

func extractZip(source string, destDir string) (ExtractResult, error) {
	reader, err := zip.OpenReader(source)
	if err != nil {
		return ExtractResult{}, fmt.Errorf("open zip archive %s: %w", source, err)
	}
	defer reader.Close()

	var members []archiveMember
	byName := map[string]*zip.File{}
	for _, file := range reader.File {
		if file.FileInfo().IsDir() {
			continue
		}
		members = append(members, archiveMember{Name: file.Name, Size: file.UncompressedSize64})
		byName[file.Name] = file
	}
	selected, err := selectArchiveMember(members, nil)
	if err != nil {
		return ExtractResult{}, fmt.Errorf("%w: %s", err, source)
	}
	member := byName[selected.Name]

	outputPath, err := safeJoin(destDir, member.Name)
	if err != nil {
		return ExtractResult{}, err
	}
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		return ExtractResult{}, fmt.Errorf("create output directory: %w", err)
	}
	input, err := member.Open()
	if err != nil {
		return ExtractResult{}, fmt.Errorf("open zip member %s: %w", member.Name, err)
	}
	defer input.Close()
	output, err := os.Create(outputPath)
	if err != nil {
		return ExtractResult{}, fmt.Errorf("create %s: %w", outputPath, err)
	}
	defer output.Close()
	if _, err := io.Copy(output, input); err != nil {
		return ExtractResult{}, fmt.Errorf("extract zip member %s: %w", member.Name, err)
	}
	return selected.result(outputPath), nil
}

func extractTar(source string, destDir string) (ExtractResult, error) {
	member, err := selectTarMember(source)
	if err != nil {
		return ExtractResult{}, err
	}
	outputPath, err := safeJoin(destDir, member.Name)
	if err != nil {
		return ExtractResult{}, err
	}
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		return ExtractResult{}, fmt.Errorf("create output directory: %w", err)
	}

	file, err := os.Open(source)
	if err != nil {
		return ExtractResult{}, fmt.Errorf("open tar archive %s: %w", source, err)
	}
	defer file.Close()
	reader := tar.NewReader(file)
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return ExtractResult{}, fmt.Errorf("read tar archive %s: %w", source, err)
		}
		if header.Name != member.Name || header.Typeflag != tar.TypeReg {
			continue
		}
		output, err := os.Create(outputPath)
		if err != nil {
			return ExtractResult{}, fmt.Errorf("create %s: %w", outputPath, err)
		}
		defer output.Close()
		if _, err := io.Copy(output, reader); err != nil {
			return ExtractResult{}, fmt.Errorf("extract tar member %s: %w", header.Name, err)
		}
		return member.result(outputPath), nil
	}
	return ExtractResult{}, fmt.Errorf("tar member disappeared: %s", member.Name)
}

type archiveMember struct {
	Name            string
	Size            uint64
	Fallback        bool
	SelectionReason string
}

func (m archiveMember) result(outputPath string) ExtractResult {
	return ExtractResult{
		Path:            outputPath,
		MemberName:      m.Name,
		Fallback:        m.Fallback,
		SelectionReason: m.SelectionReason,
	}
}

func selectTarMember(source string) (archiveMember, error) {
	file, err := os.Open(source)
	if err != nil {
		return archiveMember{}, fmt.Errorf("open tar archive %s: %w", source, err)
	}
	defer file.Close()
	reader := tar.NewReader(file)
	var members []archiveMember
	var ovfRefs []string
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return archiveMember{}, fmt.Errorf("read tar archive %s: %w", source, err)
		}
		if header.Typeflag != tar.TypeReg {
			continue
		}
		members = append(members, archiveMember{Name: header.Name, Size: uint64(header.Size)})
		if strings.EqualFold(path.Ext(header.Name), ".ovf") {
			content, err := io.ReadAll(reader)
			if err != nil {
				return archiveMember{}, fmt.Errorf("read ovf descriptor %s: %w", header.Name, err)
			}
			ovfRefs = append(ovfRefs, ovfHrefs(content)...)
		}
	}
	member, err := selectArchiveMember(members, ovfRefs)
	if err != nil {
		return archiveMember{}, fmt.Errorf("%w: %s", err, source)
	}
	return member, nil
}

func extractSevenZip(source string, destDir string) (ExtractResult, error) {
	reader, err := sevenzip.OpenReader(source)
	if err != nil {
		return ExtractResult{}, fmt.Errorf("open 7z archive %s: %w", source, err)
	}
	defer reader.Close()

	var members []archiveMember
	byName := map[string]*sevenzip.File{}
	for _, file := range reader.File {
		if file.FileInfo().IsDir() {
			continue
		}
		members = append(members, archiveMember{Name: file.Name, Size: file.UncompressedSize})
		byName[file.Name] = file
	}
	selected, err := selectArchiveMember(members, nil)
	if err != nil {
		return ExtractResult{}, fmt.Errorf("%w: %s", err, source)
	}
	member := byName[selected.Name]

	outputPath, err := safeJoin(destDir, member.Name)
	if err != nil {
		return ExtractResult{}, err
	}
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		return ExtractResult{}, fmt.Errorf("create output directory: %w", err)
	}
	input, err := member.Open()
	if err != nil {
		return ExtractResult{}, fmt.Errorf("open 7z member %s: %w", member.Name, err)
	}
	defer input.Close()
	output, err := os.Create(outputPath)
	if err != nil {
		return ExtractResult{}, fmt.Errorf("create %s: %w", outputPath, err)
	}
	defer output.Close()
	if _, err := io.Copy(output, input); err != nil {
		return ExtractResult{}, fmt.Errorf("extract 7z member %s: %w", member.Name, err)
	}
	return selected.result(outputPath), nil
}

func extractRAR(source string, destDir string) (ExtractResult, error) {
	files, err := rardecode.List(source)
	if err != nil {
		return ExtractResult{}, fmt.Errorf("open rar archive %s: %w", source, err)
	}
	var members []archiveMember
	byName := map[string]*rardecode.File{}
	for _, file := range files {
		if file.IsDir {
			continue
		}
		members = append(members, archiveMember{Name: file.Name, Size: uint64(file.UnPackedSize)})
		byName[file.Name] = file
	}
	selected, err := selectArchiveMember(members, nil)
	if err != nil {
		return ExtractResult{}, fmt.Errorf("%w: %s", err, source)
	}
	member := byName[selected.Name]

	outputPath, err := safeJoin(destDir, member.Name)
	if err != nil {
		return ExtractResult{}, err
	}
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		return ExtractResult{}, fmt.Errorf("create output directory: %w", err)
	}

	reader, err := rardecode.OpenReader(source)
	if err != nil {
		return ExtractResult{}, fmt.Errorf("open rar archive %s: %w", source, err)
	}
	defer reader.Close()
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return ExtractResult{}, fmt.Errorf("read rar archive %s: %w", source, err)
		}
		if header.Name != member.Name || header.IsDir {
			continue
		}
		output, err := os.Create(outputPath)
		if err != nil {
			return ExtractResult{}, fmt.Errorf("create %s: %w", outputPath, err)
		}
		defer output.Close()
		if _, err := io.Copy(output, reader); err != nil {
			return ExtractResult{}, fmt.Errorf("extract rar member %s: %w", header.Name, err)
		}
		return selected.result(outputPath), nil
	}
	return ExtractResult{}, fmt.Errorf("rar member disappeared: %s", member.Name)
}

func selectArchiveMember(members []archiveMember, preferred []string) (archiveMember, error) {
	if len(members) == 0 {
		return archiveMember{}, fmt.Errorf("empty archive")
	}
	preferredSet := make(map[string]bool, len(preferred))
	for _, ref := range preferred {
		preferredSet[cleanArchiveName(ref)] = true
	}
	var preferredMatches []archiveMember
	var diskCandidates []archiveMember
	for _, member := range members {
		cleanName := cleanArchiveName(member.Name)
		if preferredSet[cleanName] || preferredSet[path.Base(cleanName)] {
			preferredMatches = append(preferredMatches, member)
		}
		if isDiskCandidate(member.Name) {
			diskCandidates = append(diskCandidates, member)
		}
	}
	if len(preferredMatches) > 0 {
		selected := largestMember(preferredMatches)
		selected.SelectionReason = "selected disk referenced by ovf descriptor"
		return selected, nil
	}
	if len(diskCandidates) > 0 {
		selected := largestMember(diskCandidates)
		selected.SelectionReason = "selected largest known disk candidate"
		return selected, nil
	}
	selected := largestMember(members)
	selected.Fallback = true
	selected.SelectionReason = "no disk candidate found; selected largest regular file"
	return selected, nil
}

func largestMember(members []archiveMember) archiveMember {
	largest := members[0]
	for _, member := range members[1:] {
		if member.Size > largest.Size {
			largest = member
		}
	}
	return largest
}

func isDiskCandidate(name string) bool {
	cleanName := strings.ToLower(cleanArchiveName(name))
	for _, suffix := range []string{".gz", ".xz", ".bz2"} {
		cleanName = strings.TrimSuffix(cleanName, suffix)
	}
	switch path.Ext(cleanName) {
	case ".qcow2", ".raw", ".img", ".vmdk", ".vdi", ".vhd", ".vhdx":
		return true
	default:
		return false
	}
}

func ovfHrefs(content []byte) []string {
	var refs []string
	decoder := xml.NewDecoder(strings.NewReader(string(content)))
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return refs
		}
		startElement, ok := token.(xml.StartElement)
		if !ok {
			continue
		}
		for _, attr := range startElement.Attr {
			if strings.EqualFold(attr.Name.Local, "href") && strings.TrimSpace(attr.Value) != "" {
				refs = append(refs, attr.Value)
			}
		}
	}
	return refs
}

func cleanArchiveName(name string) string {
	return path.Clean(strings.ReplaceAll(name, "\\", "/"))
}

func safeJoin(destDir string, name string) (string, error) {
	if filepath.IsAbs(name) {
		return "", fmt.Errorf("archive member %q must be relative", name)
	}
	cleanName := filepath.Clean(name)
	if cleanName == "." || strings.HasPrefix(cleanName, ".."+string(os.PathSeparator)) || cleanName == ".." {
		return "", fmt.Errorf("archive member %q escapes destination", name)
	}
	destAbs, err := filepath.Abs(destDir)
	if err != nil {
		return "", fmt.Errorf("resolve destination %s: %w", destDir, err)
	}
	output := filepath.Join(destAbs, cleanName)
	outputAbs, err := filepath.Abs(output)
	if err != nil {
		return "", fmt.Errorf("resolve output %s: %w", output, err)
	}
	if outputAbs != destAbs && !strings.HasPrefix(outputAbs, destAbs+string(os.PathSeparator)) {
		return "", fmt.Errorf("archive member %q escapes destination", name)
	}
	return outputAbs, nil
}
