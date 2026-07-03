package collector

import (
	"runtime"
	"testing"
)

const sampleCrontab = `# Edit this file to introduce tasks to be run by cron.
#
*/5 * * * * /usr/bin/php /var/www/html/artisan schedule:run
0 2 * * * /usr/local/bin/backup.sh
# 30 3 * * 0 /usr/local/bin/weekly-report.sh
# This is just a comment, not a disabled job
`

func TestParseCrontab(t *testing.T) {
	jobs := parseCrontab(sampleCrontab)
	if len(jobs) != 3 {
		t.Fatalf("len(jobs) = %d, want 3: %+v", len(jobs), jobs)
	}

	if jobs[0].schedule != "*/5 * * * *" || jobs[0].command != "/usr/bin/php /var/www/html/artisan schedule:run" || !jobs[0].enabled {
		t.Errorf("jobs[0] = %+v", jobs[0])
	}
	if jobs[1].schedule != "0 2 * * *" || jobs[1].command != "/usr/local/bin/backup.sh" || !jobs[1].enabled {
		t.Errorf("jobs[1] = %+v", jobs[1])
	}
	if jobs[2].schedule != "30 3 * * 0" || jobs[2].command != "/usr/local/bin/weekly-report.sh" || jobs[2].enabled {
		t.Errorf("jobs[2] (commented-out/disabled) = %+v", jobs[2])
	}
}

func TestParseCrontab_EmptyInput(t *testing.T) {
	if jobs := parseCrontab(""); len(jobs) != 0 {
		t.Errorf("len(jobs) = %d, want 0", len(jobs))
	}
}

func TestParseCrontab_OnlyComments(t *testing.T) {
	const data = "# nothing to see here\n# still nothing\n"
	if jobs := parseCrontab(data); len(jobs) != 0 {
		t.Errorf("len(jobs) = %d, want 0", len(jobs))
	}
}

func TestParseCrontab_MalformedLineSkipped(t *testing.T) {
	// Fewer than 6 whitespace-separated tokens (5 schedule fields + a
	// command) can't be a valid cron line, regardless of content.
	const data = "too few fields\n*/5 * * * * echo hi\n"
	jobs := parseCrontab(data)
	if len(jobs) != 1 {
		t.Fatalf("len(jobs) = %d, want 1", len(jobs))
	}
	if jobs[0].command != "echo hi" {
		t.Errorf("command = %q, want %q", jobs[0].command, "echo hi")
	}
}

func TestCronServiceChecker_Kind(t *testing.T) {
	if got := (CronServiceChecker{}).Kind(); got != ServiceKindCron {
		t.Errorf("Kind() = %v, want %v", got, ServiceKindCron)
	}
}

func TestCronServiceChecker_Check_ToolAbsentIsNotAnError(t *testing.T) {
	if runtime.GOOS == "linux" {
		t.Skip("only exercises the non-Linux/tool-absent fallback path")
	}
	statuses, err := (CronServiceChecker{}).Check()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if statuses != nil {
		t.Errorf("statuses = %v, want nil", statuses)
	}
}
