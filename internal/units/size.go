package units

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var diskSizePattern = regexp.MustCompile(`^\d+[KMGTkmgt]?$`)

func ValidateDiskSize(raw string) error {
	if !diskSizePattern.MatchString(raw) {
		return fmt.Errorf("invalid disk size %q: use a number with optional suffix K, M, G, or T", raw)
	}
	return nil
}

func ParseSizeBytes(raw string) (int64, error) {
	if raw == "" {
		return 0, nil
	}
	suffix := raw[len(raw)-1:]
	multiplier := int64(1)
	number := raw
	switch strings.ToUpper(suffix) {
	case "K":
		multiplier = 1024
		number = raw[:len(raw)-1]
	case "M":
		multiplier = 1024 * 1024
		number = raw[:len(raw)-1]
	case "G":
		multiplier = 1024 * 1024 * 1024
		number = raw[:len(raw)-1]
	case "T":
		multiplier = 1024 * 1024 * 1024 * 1024
		number = raw[:len(raw)-1]
	}
	value, err := strconv.ParseInt(number, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse size %q: %w", raw, err)
	}
	return value * multiplier, nil
}

type ResourceType string

const (
	MemoryResource ResourceType = "memory"
	CPUResource    ResourceType = "cpus"
	DiskResource   ResourceType = "disk"
)

type ResourceProbe struct {
	AvailableMemoryBytes func() int64
	CPUCount             func() int
}

func ParseResourceSize(raw string, resource ResourceType, probe ResourceProbe) (int64, error) {
	value := strings.ToLower(strings.TrimSpace(raw))
	switch value {
	case "max":
		switch resource {
		case MemoryResource:
			available := probe.AvailableMemoryBytes()
			mb := available/(1024*1024) - 512
			if mb < 512 {
				return 512, nil
			}
			return mb, nil
		case CPUResource:
			return int64(probe.CPUCount()), nil
		case DiskResource:
			return 0, nil
		}
	case "half":
		switch resource {
		case MemoryResource:
			mb := probe.AvailableMemoryBytes() / (1024 * 1024) / 2
			if mb < 512 {
				return 512, nil
			}
			return mb, nil
		case CPUResource:
			cpus := probe.CPUCount() / 2
			if cpus < 1 {
				cpus = 1
			}
			return int64(cpus), nil
		case DiskResource:
			return 0, nil
		}
	}
	return 0, fmt.Errorf("invalid %s value %q: use an integer, max, or half", resource, raw)
}
