package units

import "testing"

func TestValidateDiskSize(t *testing.T) {
	for _, size := range []string{"10G", "500M", "1T", "1024K", "100", "20g"} {
		if err := ValidateDiskSize(size); err != nil {
			t.Fatalf("ValidateDiskSize(%q): %v", size, err)
		}
	}
	for _, size := range []string{"abc", "", "-1G", "10X"} {
		if err := ValidateDiskSize(size); err == nil {
			t.Fatalf("ValidateDiskSize(%q) succeeded", size)
		}
	}
}

func TestParseSizeBytes(t *testing.T) {
	tests := map[string]int64{
		"20G":   20 * 1024 * 1024 * 1024,
		"500M":  500 * 1024 * 1024,
		"1T":    1 * 1024 * 1024 * 1024 * 1024,
		"1024K": 1024 * 1024,
		"100":   100,
		"20g":   20 * 1024 * 1024 * 1024,
		"0":     0,
	}
	for raw, want := range tests {
		got, err := ParseSizeBytes(raw)
		if err != nil {
			t.Fatalf("ParseSizeBytes(%q): %v", raw, err)
		}
		if got != want {
			t.Fatalf("ParseSizeBytes(%q) = %d want %d", raw, got, want)
		}
	}
}

func TestParseResourceSize(t *testing.T) {
	probe := ResourceProbe{
		AvailableMemoryBytes: func() int64 { return 8 * 1024 * 1024 * 1024 },
		CPUCount:             func() int { return 8 },
	}
	got, err := ParseResourceSize("max", MemoryResource, probe)
	if err != nil {
		t.Fatalf("ParseResourceSize max memory: %v", err)
	}
	if got < 512 {
		t.Fatalf("max memory = %d", got)
	}
	got, err = ParseResourceSize("half", CPUResource, probe)
	if err != nil {
		t.Fatalf("ParseResourceSize half CPU: %v", err)
	}
	if got != 4 {
		t.Fatalf("half CPU = %d", got)
	}
	got, err = ParseResourceSize("max", DiskResource, probe)
	if err != nil {
		t.Fatalf("ParseResourceSize max disk: %v", err)
	}
	if got != 0 {
		t.Fatalf("max disk = %d", got)
	}
	if _, err := ParseResourceSize("1234", MemoryResource, probe); err == nil {
		t.Fatal("expected invalid resource size error")
	}
}
