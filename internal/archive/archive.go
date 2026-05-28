package archive

import (
	"archive/tar"
	"archive/zip"
	"compress/bzip2"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/ulikunitz/xz"
)

type Extractor struct{}

func NewExtractor() *Extractor {
	return &Extractor{}
}

func (e *Extractor) Extract(ctx context.Context, source string, destDir string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
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
	default:
		return "", fmt.Errorf("unsupported compressed format: %s", source)
	}
}

type streamOpener func(*os.File) (io.ReadCloser, error)

func extractStream(source string, destDir string, suffix string, opener streamOpener) (string, error) {
	input, err := os.Open(source)
	if err != nil {
		return "", fmt.Errorf("open %s: %w", source, err)
	}
	defer input.Close()

	reader, err := opener(input)
	if err != nil {
		return "", err
	}
	defer reader.Close()

	outputPath, err := safeJoin(destDir, strings.TrimSuffix(filepath.Base(source), suffix))
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		return "", fmt.Errorf("create output directory: %w", err)
	}
	output, err := os.Create(outputPath)
	if err != nil {
		return "", fmt.Errorf("create %s: %w", outputPath, err)
	}
	defer output.Close()
	if _, err := io.Copy(output, reader); err != nil {
		return "", fmt.Errorf("extract %s: %w", source, err)
	}
	return outputPath, nil
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

func extractZip(source string, destDir string) (string, error) {
	reader, err := zip.OpenReader(source)
	if err != nil {
		return "", fmt.Errorf("open zip archive %s: %w", source, err)
	}
	defer reader.Close()

	var largest *zip.File
	for _, file := range reader.File {
		if file.FileInfo().IsDir() {
			continue
		}
		if largest == nil || file.UncompressedSize64 > largest.UncompressedSize64 {
			largest = file
		}
	}
	if largest == nil {
		return "", fmt.Errorf("empty zip archive: %s", source)
	}

	outputPath, err := safeJoin(destDir, largest.Name)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		return "", fmt.Errorf("create output directory: %w", err)
	}
	input, err := largest.Open()
	if err != nil {
		return "", fmt.Errorf("open zip member %s: %w", largest.Name, err)
	}
	defer input.Close()
	output, err := os.Create(outputPath)
	if err != nil {
		return "", fmt.Errorf("create %s: %w", outputPath, err)
	}
	defer output.Close()
	if _, err := io.Copy(output, input); err != nil {
		return "", fmt.Errorf("extract zip member %s: %w", largest.Name, err)
	}
	return outputPath, nil
}

func extractTar(source string, destDir string) (string, error) {
	member, err := largestTarMember(source)
	if err != nil {
		return "", err
	}
	outputPath, err := safeJoin(destDir, member.Name)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		return "", fmt.Errorf("create output directory: %w", err)
	}

	file, err := os.Open(source)
	if err != nil {
		return "", fmt.Errorf("open tar archive %s: %w", source, err)
	}
	defer file.Close()
	reader := tar.NewReader(file)
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", fmt.Errorf("read tar archive %s: %w", source, err)
		}
		if header.Name != member.Name || header.Typeflag != tar.TypeReg {
			continue
		}
		output, err := os.Create(outputPath)
		if err != nil {
			return "", fmt.Errorf("create %s: %w", outputPath, err)
		}
		defer output.Close()
		if _, err := io.Copy(output, reader); err != nil {
			return "", fmt.Errorf("extract tar member %s: %w", header.Name, err)
		}
		return outputPath, nil
	}
	return "", fmt.Errorf("tar member disappeared: %s", member.Name)
}

type tarMember struct {
	Name string
	Size int64
}

func largestTarMember(source string) (tarMember, error) {
	file, err := os.Open(source)
	if err != nil {
		return tarMember{}, fmt.Errorf("open tar archive %s: %w", source, err)
	}
	defer file.Close()
	reader := tar.NewReader(file)
	var largest tarMember
	found := false
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return tarMember{}, fmt.Errorf("read tar archive %s: %w", source, err)
		}
		if header.Typeflag != tar.TypeReg {
			continue
		}
		if !found || header.Size > largest.Size {
			largest = tarMember{Name: header.Name, Size: header.Size}
			found = true
		}
	}
	if !found {
		return tarMember{}, fmt.Errorf("empty tar archive: %s", source)
	}
	return largest, nil
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
