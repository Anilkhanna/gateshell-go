package collector

import (
	"runtime"
	"testing"
)

const sampleSystemctlUnits = `nginx.service loaded active running A high performance web server
sshd.service loaded active running OpenSSH server daemon
● cron-broken.service loaded failed failed A broken cron wrapper
docker.service loaded inactive dead Docker Application Container Engine
`

func TestParseSystemdUnits(t *testing.T) {
	units := parseSystemdUnits(sampleSystemctlUnits)
	if len(units) != 4 {
		t.Fatalf("len(units) = %d, want 4", len(units))
	}

	if units[0].name != "nginx.service" || units[0].active != "active" || units[0].sub != "running" {
		t.Errorf("units[0] = %+v", units[0])
	}
	if units[2].name != "cron-broken.service" || units[2].active != "failed" {
		t.Errorf("units[2] (bullet-prefixed failed unit) = %+v", units[2])
	}
	if units[3].name != "docker.service" || units[3].active != "inactive" {
		t.Errorf("units[3] = %+v", units[3])
	}
}

func TestParseSystemdUnits_TooFewFields(t *testing.T) {
	units := parseSystemdUnits("broken.service loaded\n")
	if len(units) != 0 {
		t.Errorf("len(units) = %d, want 0", len(units))
	}
}

func TestParseSystemdUnits_EmptyInput(t *testing.T) {
	if units := parseSystemdUnits(""); len(units) != 0 {
		t.Errorf("len(units) = %d, want 0", len(units))
	}
}

func TestSystemdServiceChecker_RunningMapping(t *testing.T) {
	units := parseSystemdUnits(sampleSystemctlUnits)
	statuses := make([]ServiceStatus, 0, len(units))
	for _, u := range units {
		statuses = append(statuses, ServiceStatus{
			Kind:    ServiceKindSystemd,
			Name:    u.name,
			Running: u.active == "active",
			Detail:  u.active + "/" + u.sub,
		})
	}

	if !statuses[0].Running {
		t.Errorf("nginx.service should be Running")
	}
	if statuses[2].Running {
		t.Errorf("cron-broken.service (failed) should not be Running")
	}
	if statuses[3].Running {
		t.Errorf("docker.service (inactive) should not be Running")
	}
}

func TestSystemdServiceChecker_Kind(t *testing.T) {
	if got := (SystemdServiceChecker{}).Kind(); got != ServiceKindSystemd {
		t.Errorf("Kind() = %v, want %v", got, ServiceKindSystemd)
	}
}

func TestSystemdServiceChecker_Check_ToolAbsentIsNotAnError(t *testing.T) {
	if runtime.GOOS == "linux" {
		t.Skip("only exercises the non-Linux/tool-absent fallback path")
	}
	statuses, err := (SystemdServiceChecker{}).Check()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if statuses != nil {
		t.Errorf("statuses = %v, want nil", statuses)
	}
}
