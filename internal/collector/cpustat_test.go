package collector

import "testing"

const sampleProcStat = `cpu  100 20 60 800 10 0 5 0 0 0
cpu0 50 10 30 400 5 0 2 0 0 0
cpu1 50 10 30 400 5 0 3 0 0 0
intr 12345 0 0 0
ctxt 98765
btime 1690000000
processes 4321
`

func TestParseCPUStatLine(t *testing.T) {
	stat, err := parseCPUStatLine(sampleProcStat)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := cpuStat{user: 100, nice: 20, system: 60, idle: 800, iowait: 10, irq: 0, softirq: 5, steal: 0}
	if stat != want {
		t.Errorf("stat = %+v, want %+v", stat, want)
	}
}

func TestParseCPUStatLine_IgnoresPerCoreLines(t *testing.T) {
	// The per-core "cpu0"/"cpu1" lines must never be mistaken for the
	// aggregate line.
	stat, err := parseCPUStatLine(sampleProcStat)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stat.total() != 995 {
		t.Errorf("total() = %d, want 995 (aggregate line, not per-core)", stat.total())
	}
}

func TestParseCPUStatLine_FewerFields(t *testing.T) {
	// Older kernels (pre-2.6.24) may omit steal/guest fields entirely.
	const data = "cpu  100 20 60 800\n"
	stat, err := parseCPUStatLine(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := cpuStat{user: 100, nice: 20, system: 60, idle: 800}
	if stat != want {
		t.Errorf("stat = %+v, want %+v", stat, want)
	}
}

func TestParseCPUStatLine_NoAggregateLine(t *testing.T) {
	const data = "cpu0 50 10 30 400 5 0 2 0 0 0\n"
	if _, err := parseCPUStatLine(data); err == nil {
		t.Fatal("expected error when no aggregate cpu line is present")
	}
}

func TestParseCPUStatLine_MalformedField(t *testing.T) {
	const data = "cpu  not-a-number 20 60 800\n"
	if _, err := parseCPUStatLine(data); err == nil {
		t.Fatal("expected error for malformed field")
	}
}

func TestCPUPercent(t *testing.T) {
	prev := cpuStat{user: 100, nice: 0, system: 50, idle: 800, iowait: 50}
	// 100 total ticks elapsed, 20 of them idle -> 80% busy.
	curr := cpuStat{user: 150, nice: 0, system: 80, idle: 850, iowait: 20}

	got := cpuPercent(prev, curr)
	if got != 80 {
		t.Errorf("cpuPercent = %v, want 80", got)
	}
}

func TestCPUPercent_NoElapsedTime(t *testing.T) {
	same := cpuStat{user: 100, idle: 900}
	if got := cpuPercent(same, same); got != 0 {
		t.Errorf("cpuPercent = %v, want 0 for identical samples", got)
	}
}

func TestCPUPercent_CounterWentBackward(t *testing.T) {
	prev := cpuStat{user: 500, idle: 900}
	curr := cpuStat{user: 100, idle: 200} // total went down: reset/wrap
	if got := cpuPercent(prev, curr); got != 0 {
		t.Errorf("cpuPercent = %v, want 0 when total ticks decrease", got)
	}
}

func TestCPUPercent_FullyIdle(t *testing.T) {
	prev := cpuStat{idle: 1000}
	curr := cpuStat{idle: 1100}
	if got := cpuPercent(prev, curr); got != 0 {
		t.Errorf("cpuPercent = %v, want 0 for a fully idle window", got)
	}
}

func TestCPUPercent_FullyBusy(t *testing.T) {
	prev := cpuStat{user: 1000, idle: 0}
	curr := cpuStat{user: 1100, idle: 0}
	if got := cpuPercent(prev, curr); got != 100 {
		t.Errorf("cpuPercent = %v, want 100 for a fully busy window", got)
	}
}
