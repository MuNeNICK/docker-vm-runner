package oci

import (
	"archive/tar"
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
)

type Puller struct {
	Fetcher Fetcher
}

func NewPuller() *Puller {
	return &Puller{Fetcher: RemoteFetcher{}}
}

type Fetcher interface {
	Fetch(context.Context, string) (Image, error)
}

type Image interface {
	Digest() (string, error)
	Layers() ([]Layer, error)
}

type Layer interface {
	Open() (io.ReadCloser, error)
}

type Result struct {
	Path            string
	MemberName      string
	Fallback        bool
	SelectionReason string
}

func IsReference(value string) bool {
	if strings.HasPrefix(value, "http://") || strings.HasPrefix(value, "https://") || strings.HasPrefix(value, "/") {
		return false
	}
	if !strings.Contains(value, "/") {
		return false
	}
	first, _, _ := strings.Cut(value, "/")
	return strings.Contains(first, ".") || strings.Contains(first, ":")
}

func (p *Puller) Pull(ctx context.Context, reference string, cacheDir string) (Result, error) {
	fetcher := p.Fetcher
	if fetcher == nil {
		fetcher = RemoteFetcher{}
	}
	image, err := fetcher.Fetch(ctx, reference)
	if err != nil {
		return Result{}, err
	}
	digest, err := image.Digest()
	if err != nil {
		return Result{}, err
	}
	diskDir, sentinel := cachePaths(cacheDir, digest, reference)
	if cached, ok := cachedDisk(diskDir, sentinel); ok {
		return Result{Path: cached, SelectionReason: "cached OCI disk"}, nil
	}
	if err := os.Remove(sentinel); err != nil && !os.IsNotExist(err) {
		return Result{}, fmt.Errorf("remove stale OCI cache sentinel: %w", err)
	}
	if err := os.RemoveAll(diskDir); err != nil {
		return Result{}, fmt.Errorf("clear stale OCI disk cache: %w", err)
	}
	if err := os.MkdirAll(diskDir, 0o755); err != nil {
		return Result{}, fmt.Errorf("create OCI disk cache directory: %w", err)
	}

	layers, err := image.Layers()
	if err != nil {
		return Result{}, err
	}
	selected, err := selectLayerMember(layers)
	if err != nil {
		return Result{}, fmt.Errorf("no disk image found in OCI image %s: %w", reference, err)
	}
	outputPath := filepath.Join(diskDir, filepath.Base(selected.Name))
	if err := extractLayerMember(selected.Layer, selected.Name, outputPath); err != nil {
		return Result{}, err
	}
	if err := os.WriteFile(sentinel, []byte(digest), 0o644); err != nil {
		return Result{}, fmt.Errorf("write OCI cache sentinel: %w", err)
	}
	return Result{
		Path:            outputPath,
		MemberName:      selected.Name,
		Fallback:        selected.Fallback,
		SelectionReason: selected.SelectionReason,
	}, nil
}

type RemoteFetcher struct{}

func (RemoteFetcher) Fetch(ctx context.Context, reference string) (Image, error) {
	ref, err := name.ParseReference(reference, name.StrictValidation)
	if err != nil {
		return nil, fmt.Errorf("parse OCI reference %s: %w", reference, err)
	}
	image, err := remote.Image(ref, remote.WithContext(ctx), remote.WithUserAgent("docker-vm-runner/1.0"))
	if err != nil {
		return nil, fmt.Errorf("pull OCI image %s: %w", reference, err)
	}
	return remoteImage{image: image}, nil
}

type remoteImage struct {
	image v1.Image
}

func (i remoteImage) Digest() (string, error) {
	digest, err := i.image.Digest()
	if err != nil {
		return "", fmt.Errorf("read OCI image digest: %w", err)
	}
	return digest.String(), nil
}

func (i remoteImage) Layers() ([]Layer, error) {
	layers, err := i.image.Layers()
	if err != nil {
		return nil, fmt.Errorf("read OCI image layers: %w", err)
	}
	result := make([]Layer, 0, len(layers))
	for _, layer := range layers {
		result = append(result, remoteLayer{layer: layer})
	}
	return result, nil
}

type remoteLayer struct {
	layer v1.Layer
}

func (l remoteLayer) Open() (io.ReadCloser, error) {
	return l.layer.Uncompressed()
}

type layerMember struct {
	Layer           Layer
	Name            string
	Size            int64
	Fallback        bool
	SelectionReason string
}

func selectLayerMember(layers []Layer) (layerMember, error) {
	var diskDirCandidates []layerMember
	var diskDirDiskCandidates []layerMember
	var diskCandidates []layerMember
	var regularFiles []layerMember
	for _, layer := range layers {
		reader, err := layer.Open()
		if err != nil {
			return layerMember{}, fmt.Errorf("open OCI layer: %w", err)
		}
		func() {
			defer reader.Close()
			tarReader := tar.NewReader(reader)
			for {
				header, err := tarReader.Next()
				if err == io.EOF {
					return
				}
				if err != nil {
					return
				}
				if header.Typeflag != tar.TypeReg || header.Size <= 0 {
					continue
				}
				member := layerMember{Layer: layer, Name: header.Name, Size: header.Size}
				cleanName := cleanArchiveName(header.Name)
				if strings.HasPrefix(cleanName, "disk/") {
					diskDirCandidates = append(diskDirCandidates, member)
					if isDiskCandidate(header.Name) {
						diskDirDiskCandidates = append(diskDirDiskCandidates, member)
					}
					continue
				}
				if isDiskCandidate(header.Name) {
					diskCandidates = append(diskCandidates, member)
					continue
				}
				regularFiles = append(regularFiles, member)
			}
		}()
	}
	if len(diskDirDiskCandidates) > 0 {
		selected := largestLayerMember(diskDirDiskCandidates)
		selected.SelectionReason = "selected largest known disk candidate under OCI /disk directory"
		return selected, nil
	}
	if len(diskDirCandidates) > 0 {
		selected := largestLayerMember(diskDirCandidates)
		selected.SelectionReason = "selected largest file under OCI /disk directory"
		return selected, nil
	}
	if len(diskCandidates) > 0 {
		selected := largestLayerMember(diskCandidates)
		selected.SelectionReason = "selected largest known disk candidate"
		return selected, nil
	}
	if len(regularFiles) > 0 {
		selected := largestLayerMember(regularFiles)
		selected.Fallback = true
		selected.SelectionReason = "no disk candidate found; selected largest regular file"
		return selected, nil
	}
	return layerMember{}, fmt.Errorf("no regular file members found")
}

func extractLayerMember(layer Layer, memberName string, outputPath string) error {
	reader, err := layer.Open()
	if err != nil {
		return fmt.Errorf("open OCI layer: %w", err)
	}
	defer reader.Close()
	tarReader := tar.NewReader(reader)
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("read OCI layer: %w", err)
		}
		if header.Name != memberName || header.Typeflag != tar.TypeReg {
			continue
		}
		output, err := os.Create(outputPath)
		if err != nil {
			return fmt.Errorf("create OCI disk %s: %w", outputPath, err)
		}
		defer output.Close()
		if _, err := io.Copy(output, tarReader); err != nil {
			return fmt.Errorf("extract OCI disk member %s: %w", memberName, err)
		}
		return nil
	}
	return fmt.Errorf("OCI layer member disappeared: %s", memberName)
}

func largestLayerMember(members []layerMember) layerMember {
	largest := members[0]
	for _, member := range members[1:] {
		if member.Size > largest.Size {
			largest = member
		}
	}
	return largest
}

func cachePaths(cacheDir string, digest string, reference string) (string, string) {
	key := strings.ReplaceAll(digest, ":", "-")
	if len(key) > 19 {
		key = key[:19]
	}
	base := safeReferenceName(reference)
	name := key + "-" + base
	return filepath.Join(cacheDir, name), filepath.Join(cacheDir, name+".done")
}

func cachedDisk(diskDir string, sentinel string) (string, bool) {
	if _, err := os.Stat(sentinel); err != nil {
		return "", false
	}
	entries, err := os.ReadDir(diskDir)
	if err != nil {
		return "", false
	}
	for _, entry := range entries {
		if entry.Type().IsRegular() && !strings.HasSuffix(entry.Name(), ".done") {
			return filepath.Join(diskDir, entry.Name()), true
		}
	}
	return "", false
}

var unsafeNameChars = regexp.MustCompile(`[^A-Za-z0-9._-]`)

func safeReferenceName(reference string) string {
	last := reference
	if idx := strings.LastIndex(reference, "/"); idx >= 0 {
		last = reference[idx+1:]
	}
	if tag, _, ok := strings.Cut(last, "@"); ok {
		last = tag
	}
	return unsafeNameChars.ReplaceAllString(last, "_")
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

func cleanArchiveName(name string) string {
	return path.Clean(strings.ReplaceAll(name, "\\", "/"))
}
