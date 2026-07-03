package collector

import (
	"strconv"
	"strings"
)

// parsePS parses `ps -eo pid,comm,%cpu,rss --sort=-%cpu` output (already
// CPU-descending; not re-sorted here) into ProcessInfo rows. It skips the
// header line and any malformed rows rather than failing the whole batch --
// a single misparsed process shouldn't drop the rest of the top-N list.
//
// The command name (comm) is column 2 and never contains embedded spaces,
// but this parser is tolerant of that anyway: it takes the first field as
// PID and the last two fields as %cpu/rss, joining everything in between
// as the name.
func parsePS(output string) ([]ProcessInfo, error) {
	lines := strings.Split(strings.TrimRight(output, "\n"), "\n")
	procs := make([]ProcessInfo, 0, len(lines))

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		fields := strings.Fields(trimmed)
		if len(fields) < 4 {
			continue
		}
		if strings.EqualFold(fields[0], "PID") {
			continue // header row
		}

		pid, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}
		cpu, err := strconv.ParseFloat(fields[len(fields)-2], 64)
		if err != nil {
			continue
		}
		rssKB, err := strconv.ParseFloat(fields[len(fields)-1], 64)
		if err != nil {
			continue
		}
		name := strings.Join(fields[1:len(fields)-2], " ")

		procs = append(procs, ProcessInfo{
			PID:        pid,
			Name:       name,
			CPUPercent: cpu,
			MemMB:      rssKB / 1024,
		})
	}

	return procs, nil
}
