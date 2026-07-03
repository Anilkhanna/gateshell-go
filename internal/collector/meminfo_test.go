package collector

import "testing"

const sampleMemInfo = `MemTotal:       16302028 kB
MemFree:         1233456 kB
MemAvailable:   10456789 kB
Buffers:          204800 kB
Cached:          8000000 kB
SwapCached:            0 kB
Active:          4000000 kB
Inactive:        3000000 kB
SwapTotal:       2097148 kB
SwapFree:        2097148 kB
`

func TestParseMemInfo(t *testing.T) {
	totalKB, availKB, err := parseMemInfo(sampleMemInfo)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if totalKB != 16302028 {
		t.Errorf("totalKB = %d, want 16302028", totalKB)
	}
	if availKB != 10456789 {
		t.Errorf("availKB = %d, want 10456789", availKB)
	}
}

func TestParseMemInfo_MissingMemTotal(t *testing.T) {
	const data = `MemFree:         1233456 kB
MemAvailable:   10456789 kB
`
	if _, _, err := parseMemInfo(data); err == nil {
		t.Fatal("expected error for missing MemTotal, got nil")
	}
}

func TestParseMemInfo_MissingMemAvailable(t *testing.T) {
	const data = `MemTotal:       16302028 kB
MemFree:         1233456 kB
`
	if _, _, err := parseMemInfo(data); err == nil {
		t.Fatal("expected error for missing MemAvailable, got nil")
	}
}

func TestParseMemInfo_MalformedValue(t *testing.T) {
	const data = `MemTotal:       not-a-number kB
MemAvailable:   10456789 kB
`
	if _, _, err := parseMemInfo(data); err == nil {
		t.Fatal("expected error for malformed MemTotal value, got nil")
	}
}

func TestParseMemInfo_EmptyInput(t *testing.T) {
	if _, _, err := parseMemInfo(""); err == nil {
		t.Fatal("expected error for empty input, got nil")
	}
}
