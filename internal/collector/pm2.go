package collector

import (
	"encoding/json"
	"os/exec"
	"runtime"
	"strconv"
	"time"
)

// PM2ServiceChecker probes `pm2 jlist` for Node.js process manager status.
// Absent pm2 (not installed, or nothing managed on this host) yields no
// services rather than an error.
type PM2ServiceChecker struct{}

func (PM2ServiceChecker) Kind() ServiceKind { return ServiceKindPM2 }

func (PM2ServiceChecker) Check() ([]ServiceStatus, error) {
	if runtime.GOOS != "linux" {
		return nil, nil
	}
	if _, err := exec.LookPath("pm2"); err != nil {
		return nil, nil // pm2 not installed: not detected, not an error
	}

	out, err := exec.Command("pm2", "jlist").Output()
	if err != nil {
		return nil, nil
	}

	procs := parsePM2JList(string(out))
	now := time.Now()
	statuses := make([]ServiceStatus, 0, len(procs))
	for _, p := range procs {
		statuses = append(statuses, ServiceStatus{
			Kind:      ServiceKindPM2,
			Name:      p.name,
			Running:   p.status == "online",
			Detail:    p.status,
			LastCheck: now,
		})
	}
	return statuses, nil
}

// pm2Process is one entry parsed from `pm2 jlist`.
type pm2Process struct {
	name   string
	status string
	pid    int
	pmID   int
}

// parsePM2JList parses `pm2 jlist` JSON output. Mirrors the iOS
// PM2Parser.parse semantics: an entry is kept only if it has a name, a
// pid, and a pm2_env with both status and pm_id -- anything less is
// dropped rather than failing the whole batch. PM2's numeric fields arrive
// as a JSON number, or occasionally a string, across versions, so pid/
// pm_id are read flexibly via anyInt. Malformed JSON yields an empty
// (not error) result, matching the tool-absent-is-not-an-error policy for
// service checkers.
func parsePM2JList(output string) []pm2Process {
	var raw []map[string]any
	if err := json.Unmarshal([]byte(output), &raw); err != nil {
		return nil
	}

	procs := make([]pm2Process, 0, len(raw))
	for _, item := range raw {
		name, _ := item["name"].(string)
		pid := anyInt(item["pid"])
		env, _ := item["pm2_env"].(map[string]any)
		if name == "" || env == nil || pid == nil {
			continue
		}

		status, _ := env["status"].(string)
		pmID := anyInt(env["pm_id"])
		if status == "" || pmID == nil {
			continue
		}

		procs = append(procs, pm2Process{name: name, status: status, pid: *pid, pmID: *pmID})
	}
	return procs
}

// anyInt reads an int out of a decoded-JSON value that may already be a
// float64 (the default Go type for JSON numbers), an int, or a numeric
// string. Returns nil if v isn't a recognizable number.
func anyInt(v any) *int {
	switch t := v.(type) {
	case float64:
		i := int(t)
		return &i
	case int:
		return &t
	case string:
		if i, err := strconv.Atoi(t); err == nil {
			return &i
		}
	}
	return nil
}
