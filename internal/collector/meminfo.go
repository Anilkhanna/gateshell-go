package collector

import (
	"fmt"
	"strconv"
	"strings"
)

// parseMemInfo parses /proc/meminfo content and extracts MemTotal and
// MemAvailable, both in KB. MemAvailable (kernel-estimated, reclaimable
// caches accounted for) is preferred over MemFree for computing "used"
// because MemFree alone overstates usage on any host with an active page
// cache. Returns an error if either field is missing so callers can
// degrade to a zero sample rather than report a misleading number.
func parseMemInfo(data string) (totalKB, availKB uint64, err error) {
	var haveTotal, haveAvail bool

	for _, line := range strings.Split(data, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		key := strings.TrimSuffix(fields[0], ":")
		switch key {
		case "MemTotal":
			v, perr := strconv.ParseUint(fields[1], 10, 64)
			if perr != nil {
				return 0, 0, fmt.Errorf("parsing MemTotal: %w", perr)
			}
			totalKB = v
			haveTotal = true
		case "MemAvailable":
			v, perr := strconv.ParseUint(fields[1], 10, 64)
			if perr != nil {
				return 0, 0, fmt.Errorf("parsing MemAvailable: %w", perr)
			}
			availKB = v
			haveAvail = true
		}
		if haveTotal && haveAvail {
			break
		}
	}

	if !haveTotal {
		return 0, 0, fmt.Errorf("MemTotal not found in /proc/meminfo")
	}
	if !haveAvail {
		return 0, 0, fmt.Errorf("MemAvailable not found in /proc/meminfo")
	}
	return totalKB, availKB, nil
}
