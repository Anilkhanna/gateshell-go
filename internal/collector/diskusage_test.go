package collector

import "testing"

func TestCalcDiskUsage(t *testing.T) {
	// 4096-byte blocks, 1,000,000 total (~3.9GB), 250,000 available (~0.95GB).
	usedGB, totalGB := calcDiskUsage(4096, 1_000_000, 250_000)

	wantTotalGB := float64(1_000_000*4096) / bytesPerGB
	wantUsedGB := float64(1_000_000*4096-250_000*4096) / bytesPerGB

	if totalGB != wantTotalGB {
		t.Errorf("totalGB = %v, want %v", totalGB, wantTotalGB)
	}
	if usedGB != wantUsedGB {
		t.Errorf("usedGB = %v, want %v", usedGB, wantUsedGB)
	}
}

func TestCalcDiskUsage_AvailExceedsTotal(t *testing.T) {
	// Pathological input (shouldn't happen from a real statfs call) must
	// not produce a negative "used" value.
	usedGB, totalGB := calcDiskUsage(4096, 100, 1000)
	if usedGB != 0 {
		t.Errorf("usedGB = %v, want 0", usedGB)
	}
	if totalGB <= 0 {
		t.Errorf("totalGB = %v, want > 0", totalGB)
	}
}

func TestCalcDiskUsage_Zero(t *testing.T) {
	usedGB, totalGB := calcDiskUsage(0, 0, 0)
	if usedGB != 0 || totalGB != 0 {
		t.Errorf("got usedGB=%v totalGB=%v, want 0, 0", usedGB, totalGB)
	}
}
