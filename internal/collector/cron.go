package collector

import (
	"bufio"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// CronServiceChecker inspects the invoking user's crontab (`crontab -l`)
// and reports each entry's enabled/disabled state as a ServiceStatus.
// Absent crontab (tool not installed) or no crontab configured for this
// user (a normal, common state -- `crontab -l` exits non-zero) both yield
// no services rather than an error.
type CronServiceChecker struct{}

func (CronServiceChecker) Kind() ServiceKind { return ServiceKindCron }

func (CronServiceChecker) Check() ([]ServiceStatus, error) {
	if runtime.GOOS != "linux" {
		return nil, nil
	}
	if _, err := exec.LookPath("crontab"); err != nil {
		return nil, nil
	}

	out, err := exec.Command("crontab", "-l").Output()
	if err != nil {
		return nil, nil // e.g. "no crontab for user": not detected, not an error
	}

	jobs := parseCrontab(string(out))
	now := time.Now()
	statuses := make([]ServiceStatus, 0, len(jobs))
	for i, j := range jobs {
		name := j.command
		if name == "" {
			name = fmt.Sprintf("cron-job-%d", i+1)
		}
		statuses = append(statuses, ServiceStatus{
			Kind:      ServiceKindCron,
			Name:      name,
			Running:   j.enabled,
			Detail:    j.schedule,
			LastCheck: now,
		})
	}
	return statuses, nil
}

// cronJob is one entry parsed from `crontab -l`.
type cronJob struct {
	schedule string
	command  string
	enabled  bool
}

// parseCrontab parses `crontab -l` output. Mirrors the iOS
// CronJobParser.parse semantics: blank lines and pure comments are
// skipped, but a commented-out line that still looks like a 5-field cron
// schedule (contains " *" or "\t*") is kept as a disabled entry rather
// than discarded, since that's the standard way operators disable a job
// without deleting it.
func parseCrontab(output string) []cronJob {
	var jobs []cronJob

	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		isComment := strings.HasPrefix(line, "#")
		if isComment && !strings.Contains(line, " *") && !strings.Contains(line, "\t*") {
			continue // pure comment, not a disabled cron entry
		}

		content := line
		if isComment {
			content = strings.TrimSpace(strings.TrimPrefix(line, "#"))
		}

		parts := strings.SplitN(content, " ", 6)
		if len(parts) < 6 {
			continue
		}

		jobs = append(jobs, cronJob{
			schedule: strings.Join(parts[0:5], " "),
			command:  parts[5],
			enabled:  !isComment,
		})
	}

	return jobs
}
