package collector

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// defaultCPUSampleInterval is the delay between the two /proc/stat samples
// CPUReader takes to compute a delta-based utilization percentage.
const defaultCPUSampleInterval = 200 * time.Millisecond

// cpuStat holds the aggregate ("cpu ", all-cores-combined) counters from
// one line of /proc/stat, in USER_HZ ticks since boot.
type cpuStat struct {
	user, nice, system, idle, iowait, irq, softirq, steal uint64
}

// total is the sum of all ticks this sample accounts for.
func (s cpuStat) total() uint64 {
	return s.user + s.nice + s.system + s.idle + s.iowait + s.irq + s.softirq + s.steal
}

// idleTotal is idle + iowait, both "not doing work" states.
func (s cpuStat) idleTotal() uint64 {
	return s.idle + s.iowait
}

// parseCPUStatLine finds the aggregate "cpu " line in /proc/stat content
// (as opposed to the per-core "cpu0", "cpu1", ... lines) and parses its
// tick counters. Older kernels may report fewer than 8 fields (e.g. no
// "steal" pre-2.6.24); missing trailing fields default to 0.
func parseCPUStatLine(data string) (cpuStat, error) {
	for _, line := range strings.Split(data, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 5 || fields[0] != "cpu" {
			continue
		}

		var vals [8]uint64
		for i := 1; i < len(fields) && i <= 8; i++ {
			v, err := strconv.ParseUint(fields[i], 10, 64)
			if err != nil {
				return cpuStat{}, fmt.Errorf("parsing /proc/stat field %d: %w", i, err)
			}
			vals[i-1] = v
		}

		return cpuStat{
			user: vals[0], nice: vals[1], system: vals[2], idle: vals[3],
			iowait: vals[4], irq: vals[5], softirq: vals[6], steal: vals[7],
		}, nil
	}
	return cpuStat{}, fmt.Errorf("no aggregate cpu line found in /proc/stat")
}

// cpuPercent computes aggregate CPU utilization (0-100) from two
// /proc/stat samples of the same counters taken some interval apart. It
// returns 0 for a zero or backward-moving total delta (e.g. counter reset)
// rather than a nonsensical or divide-by-zero result.
func cpuPercent(prev, curr cpuStat) float64 {
	prevTotal, currTotal := prev.total(), curr.total()
	if currTotal <= prevTotal {
		return 0
	}
	totalDelta := currTotal - prevTotal

	prevIdle, currIdle := prev.idleTotal(), curr.idleTotal()
	if currIdle < prevIdle {
		return 0
	}
	idleDelta := currIdle - prevIdle
	if idleDelta > totalDelta {
		return 0
	}

	pct := float64(totalDelta-idleDelta) / float64(totalDelta) * 100
	switch {
	case pct < 0:
		return 0
	case pct > 100:
		return 100
	default:
		return pct
	}
}
