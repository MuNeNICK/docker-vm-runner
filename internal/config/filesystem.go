package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type FilesystemShare struct {
	Source     string
	Target     string
	Driver     string
	AccessMode string
	Readonly   bool
}

type FilesystemParseOptions struct{}

func ParseFilesystems(env MapEnv, opts FilesystemParseOptions) ([]FilesystemShare, error) {
	var shares []FilesystemShare
	for index := 1; ; index++ {
		share, ok, err := parseFilesystem(env, index)
		if err != nil {
			return nil, err
		}
		if !ok {
			break
		}
		shares = append(shares, share)
	}
	return shares, nil
}

func parseFilesystem(env MapEnv, index int) (FilesystemShare, bool, error) {
	sourceRaw, sourceOK := lookupFilesystem(env, "FILESYSTEM_SOURCE", index)
	targetRaw, targetOK := lookupFilesystem(env, "FILESYSTEM_TARGET", index)
	driverRaw, driverOK := lookupFilesystem(env, "FILESYSTEM_DRIVER", index)
	accessRaw, accessOK := lookupFilesystem(env, "FILESYSTEM_ACCESSMODE", index)
	readonlyRaw, readonlyOK := lookupFilesystem(env, "FILESYSTEM_READONLY", index)

	hasValue := nonEmpty(sourceRaw, sourceOK) || nonEmpty(targetRaw, targetOK) || nonEmpty(driverRaw, driverOK) || nonEmpty(accessRaw, accessOK)
	if !hasValue && readonlyOK && Truthy[strings.ToLower(strings.TrimSpace(readonlyRaw))] {
		hasValue = true
	}
	if !hasValue {
		return FilesystemShare{}, false, nil
	}

	suffix := filesystemSuffix(index)
	if strings.TrimSpace(sourceRaw) == "" {
		return FilesystemShare{}, false, fmt.Errorf("FILESYSTEM%s_SOURCE is required when configuring a filesystem share", suffix)
	}
	source, err := expandUserPath(strings.TrimSpace(sourceRaw))
	if err != nil {
		return FilesystemShare{}, false, err
	}
	source = filepath.Clean(source)

	target := strings.TrimSpace(targetRaw)
	if target == "" {
		derived := filepath.Base(source)
		if derived == "" || derived == "." || derived == "/" || source == "/" {
			return FilesystemShare{}, false, fmt.Errorf("FILESYSTEM%s_TARGET is required (could not auto-derive from source %q)", suffix, sourceRaw)
		}
		target = derived
	}
	if strings.Contains(target, "/") {
		return FilesystemShare{}, false, fmt.Errorf("FILESYSTEM%s_TARGET %q must be a simple tag without '/' characters", suffix, target)
	}

	readonly := false
	if readonlyOK {
		readonly = Truthy[strings.ToLower(strings.TrimSpace(readonlyRaw))]
	}
	if err := ensureFilesystemSource(source, readonly, suffix); err != nil {
		return FilesystemShare{}, false, err
	}

	driver := strings.ToLower(strings.TrimSpace(driverRaw))
	if driver == "" {
		driver = "virtiofs"
	}
	if driver != "virtiofs" && driver != "9p" {
		return FilesystemShare{}, false, fmt.Errorf("Unsupported FILESYSTEM%s_DRIVER %q. Supported: virtiofs, 9p", suffix, driver)
	}

	accessMode := strings.ToLower(strings.TrimSpace(accessRaw))
	if accessMode == "" {
		accessMode = "passthrough"
	}
	if accessMode != "passthrough" && accessMode != "mapped" && accessMode != "squash" {
		return FilesystemShare{}, false, fmt.Errorf("Unsupported FILESYSTEM%s_ACCESSMODE %q. Supported values: passthrough, mapped, squash.", suffix, accessMode)
	}
	if driver == "virtiofs" && accessMode != "passthrough" {
		return FilesystemShare{}, false, fmt.Errorf("FILESYSTEM%s_ACCESSMODE=%q is not supported with virtiofs. virtiofs only supports passthrough", suffix, accessMode)
	}

	return FilesystemShare{
		Source:     source,
		Target:     target,
		Driver:     driver,
		AccessMode: accessMode,
		Readonly:   readonly,
	}, true, nil
}

func ensureFilesystemSource(source string, readonly bool, suffix string) error {
	info, err := os.Stat(source)
	if err == nil {
		if !info.IsDir() {
			return fmt.Errorf("FILESYSTEM%s_SOURCE %s must point to a directory", suffix, source)
		}
		return nil
	}
	if !os.IsNotExist(err) {
		return fmt.Errorf("stat FILESYSTEM%s_SOURCE %s: %w", suffix, source, err)
	}
	if readonly {
		return fmt.Errorf("FILESYSTEM%s_SOURCE %s does not exist and cannot be created while readonly", suffix, source)
	}
	if err := os.MkdirAll(source, 0o755); err != nil {
		return fmt.Errorf("create FILESYSTEM%s_SOURCE %s: %w", suffix, source, err)
	}
	return nil
}

func lookupFilesystem(env MapEnv, base string, index int) (string, bool) {
	if index == 1 {
		return env.Lookup(base)
	}
	prefix, suffix, ok := strings.Cut(base, "_")
	if !ok {
		return env.Lookup(fmt.Sprintf("%s%d", base, index))
	}
	return env.Lookup(fmt.Sprintf("%s%d_%s", prefix, index, suffix))
}

func expandUserPath(path string) (string, error) {
	if path == "~" || strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("expand FILESYSTEM_SOURCE %q: %w", path, err)
		}
		if path == "~" {
			return home, nil
		}
		return filepath.Join(home, strings.TrimPrefix(path, "~/")), nil
	}
	return path, nil
}

func filesystemSuffix(index int) string {
	if index == 1 {
		return ""
	}
	return strconv.Itoa(index)
}

func nonEmpty(value string, ok bool) bool {
	return ok && strings.TrimSpace(value) != ""
}
