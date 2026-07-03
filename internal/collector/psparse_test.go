package collector

import "testing"

const samplePSOutput = `  PID COMMAND           %CPU   RSS
    1 systemd            0.0  2048
 1234 nginx              2.3  15360
 5678 node               8.7  204800
`

func TestParsePS(t *testing.T) {
	procs, err := parsePS(samplePSOutput)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(procs) != 3 {
		t.Fatalf("len(procs) = %d, want 3", len(procs))
	}

	want := []ProcessInfo{
		{PID: 1, Name: "systemd", CPUPercent: 0.0, MemMB: 2},
		{PID: 1234, Name: "nginx", CPUPercent: 2.3, MemMB: 15},
		{PID: 5678, Name: "node", CPUPercent: 8.7, MemMB: 200},
	}
	for i, w := range want {
		if procs[i] != w {
			t.Errorf("procs[%d] = %+v, want %+v", i, procs[i], w)
		}
	}
}

func TestParsePS_SkipsHeaderOnly(t *testing.T) {
	procs, err := parsePS("  PID COMMAND           %CPU   RSS\n")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(procs) != 0 {
		t.Errorf("len(procs) = %d, want 0", len(procs))
	}
}

func TestParsePS_EmptyInput(t *testing.T) {
	procs, err := parsePS("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(procs) != 0 {
		t.Errorf("len(procs) = %d, want 0", len(procs))
	}
}

func TestParsePS_SkipsMalformedRows(t *testing.T) {
	const data = `  PID COMMAND           %CPU   RSS
not-a-pid bogus row here
 1234 nginx              2.3  15360
`
	procs, err := parsePS(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(procs) != 1 {
		t.Fatalf("len(procs) = %d, want 1", len(procs))
	}
	if procs[0].PID != 1234 {
		t.Errorf("PID = %d, want 1234", procs[0].PID)
	}
}

func TestParsePS_MultiWordCommandName(t *testing.T) {
	// A command containing spaces (unusual for `comm`, but the parser
	// tolerates it) should be reassembled correctly.
	const data = "  PID COMMAND           %CPU   RSS\n 42 my process name        1.5  4096\n"
	procs, err := parsePS(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(procs) != 1 {
		t.Fatalf("len(procs) = %d, want 1", len(procs))
	}
	if procs[0].Name != "my process name" {
		t.Errorf("Name = %q, want %q", procs[0].Name, "my process name")
	}
}
