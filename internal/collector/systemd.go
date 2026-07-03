package collector

import (
	"bufio"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// SystemdServiceChecker probes `systemctl list-units --type=service --all`
// to report the running state of every known service unit (loaded, active
// or not, including failed ones). Absent systemctl (non-systemd distros,
// minimal containers, macOS dev) yields no services rather than an error.
type SystemdServiceChecker struct{}

func (SystemdServiceChecker) Kind() ServiceKind { return ServiceKindSystemd }

func (SystemdServiceChecker) Check() ([]ServiceStatus, error) {
	if runtime.GOOS != "linux" {
		return nil, nil
	}
	if _, err := exec.LookPath("systemctl"); err != nil {
		return nil, nil // not a systemd host: not detected, not an error
	}

	out, err := exec.Command("systemctl", "list-units",
		"--type=service", "--all", "--no-legend", "--no-pager", "--plain").Output()
	if err != nil {
		return nil, nil
	}

	units := parseSystemdUnits(string(out))
	now := time.Now()
	statuses := make([]ServiceStatus, 0, len(units))
	for _, u := range units {
		statuses = append(statuses, ServiceStatus{
			Kind:      ServiceKindSystemd,
			Name:      u.name,
			Running:   u.active == "active",
			Detail:    u.active + "/" + u.sub,
			LastCheck: now,
		})
	}
	return statuses, nil
}

// systemdUnit is one row parsed from `systemctl list-units`.
type systemdUnit struct {
	name   string
	load   string
	active string
	sub    string
}

// parseSystemdUnits parses `systemctl list-units --type=service --all
// --no-legend --no-pager --plain` output: whitespace-separated columns
// UNIT LOAD ACTIVE SUB DESCRIPTION. Units in a failed state are sometimes
// prefixed with a "●" bullet column by systemd on UTF-8-capable terminals;
// that leading column is stripped if present.
func parseSystemdUnits(output string) []systemdUnit {
	var units []systemdUnit

	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) > 0 && fields[0] == "●" {
			fields = fields[1:]
		}
		if len(fields) < 4 {
			continue
		}

		units = append(units, systemdUnit{
			name:   fields[0],
			load:   fields[1],
			active: fields[2],
			sub:    fields[3],
		})
	}

	return units
}
