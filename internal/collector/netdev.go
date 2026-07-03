package collector

import (
	"strconv"
	"strings"
	"time"
)

// defaultNetSampleInterval is the delay between the two /proc/net/dev
// samples NetReader takes to compute a bytes/sec rate.
const defaultNetSampleInterval = 200 * time.Millisecond

// netIface holds cumulative rx/tx byte counters for one network interface,
// as read from /proc/net/dev.
type netIface struct {
	name             string
	rxBytes, txBytes uint64
}

// parseNetDev parses /proc/net/dev content and returns the first
// non-loopback interface's cumulative rx/tx byte counters. Mirrors the iOS
// NetworkParser.parseProcNetDev semantics: skip the two header rows, skip
// "lo"/"loop*" interfaces, and take the first interface with a
// well-formed counters line. ok is false if no such interface is found
// (e.g. only loopback is up).
func parseNetDev(data string) (iface netIface, ok bool) {
	lines := strings.Split(data, "\n")
	if len(lines) <= 2 {
		return netIface{}, false
	}

	for _, line := range lines[2:] { // skip the two header rows
		fields := strings.FieldsFunc(line, func(r rune) bool {
			return r == ' ' || r == '\t' || r == ':'
		})
		if len(fields) < 10 {
			continue
		}

		name := fields[0]
		if name == "lo" || strings.HasPrefix(name, "loop") {
			continue
		}

		rx, err1 := strconv.ParseUint(fields[1], 10, 64)
		tx, err2 := strconv.ParseUint(fields[9], 10, 64)
		if err1 != nil || err2 != nil {
			continue
		}

		return netIface{name: name, rxBytes: rx, txBytes: tx}, true
	}
	return netIface{}, false
}
