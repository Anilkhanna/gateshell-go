package collector

import (
	"runtime"
	"testing"
)

func TestParseDockerPS(t *testing.T) {
	output := "web\tnginx:latest\tUp 3 days\t0.0.0.0:80->80/tcp\tabc123\n" +
		"worker\tapp:v2\tExited (0) 2 hours ago\t\tdef456\n"

	containers := parseDockerPS(output)
	if len(containers) != 2 {
		t.Fatalf("len(containers) = %d, want 2", len(containers))
	}

	if containers[0].name != "web" || !containers[0].running {
		t.Errorf("containers[0] = %+v, want running web container", containers[0])
	}
	if containers[1].name != "worker" || containers[1].running {
		t.Errorf("containers[1] = %+v, want stopped worker container", containers[1])
	}
}

func TestParseDockerPS_SkipsRowsMissingNameOrID(t *testing.T) {
	output := "\tnginx:latest\tUp 3 days\t\tabc123\n" + // missing name
		"web\tnginx:latest\tUp 3 days\t\t\n" // missing id

	containers := parseDockerPS(output)
	if len(containers) != 0 {
		t.Errorf("len(containers) = %d, want 0", len(containers))
	}
}

func TestParseDockerPS_TooFewColumns(t *testing.T) {
	containers := parseDockerPS("web\tnginx:latest\n")
	if len(containers) != 0 {
		t.Errorf("len(containers) = %d, want 0", len(containers))
	}
}

func TestParseDockerPS_EmptyInput(t *testing.T) {
	if containers := parseDockerPS(""); len(containers) != 0 {
		t.Errorf("len(containers) = %d, want 0", len(containers))
	}
}

func TestDockerServiceChecker_Kind(t *testing.T) {
	if got := (DockerServiceChecker{}).Kind(); got != ServiceKindDocker {
		t.Errorf("Kind() = %v, want %v", got, ServiceKindDocker)
	}
}

func TestDockerServiceChecker_Check_ToolAbsentIsNotAnError(t *testing.T) {
	// On a non-Linux dev machine (or any host without docker installed),
	// Check must report "not detected" rather than erroring.
	if runtime.GOOS == "linux" {
		t.Skip("only exercises the non-Linux/tool-absent fallback path")
	}
	statuses, err := (DockerServiceChecker{}).Check()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if statuses != nil {
		t.Errorf("statuses = %v, want nil", statuses)
	}
}
