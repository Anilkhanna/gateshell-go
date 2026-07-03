package collector

import (
	"runtime"
	"testing"
)

const samplePM2JList = `[
  {
    "pid": 4321,
    "name": "api-server",
    "pm2_env": {
      "status": "online",
      "pm_id": 0,
      "restart_time": 2,
      "exec_mode": "cluster"
    },
    "monit": {
      "cpu": 1.2,
      "memory": 52428800
    }
  },
  {
    "pid": 0,
    "name": "worker",
    "pm2_env": {
      "status": "stopped",
      "pm_id": "1"
    }
  },
  {
    "pid": 999,
    "name": "broken-entry",
    "pm2_env": {
      "pm_id": 2
    }
  }
]`

func TestParsePM2JList(t *testing.T) {
	procs := parsePM2JList(samplePM2JList)
	if len(procs) != 2 {
		t.Fatalf("len(procs) = %d, want 2 (entry missing status must be dropped)", len(procs))
	}

	if procs[0].name != "api-server" || procs[0].status != "online" || procs[0].pmID != 0 {
		t.Errorf("procs[0] = %+v", procs[0])
	}
	if procs[1].name != "worker" || procs[1].status != "stopped" || procs[1].pmID != 1 {
		t.Errorf("procs[1] (string pm_id) = %+v", procs[1])
	}
}

func TestParsePM2JList_EmptyArray(t *testing.T) {
	if procs := parsePM2JList("[]"); len(procs) != 0 {
		t.Errorf("len(procs) = %d, want 0", len(procs))
	}
}

func TestParsePM2JList_MalformedJSON(t *testing.T) {
	if procs := parsePM2JList("not json at all"); procs != nil {
		t.Errorf("procs = %v, want nil for malformed JSON", procs)
	}
}

func TestParsePM2JList_MissingPM2Env(t *testing.T) {
	const data = `[{"pid": 1, "name": "x"}]`
	if procs := parsePM2JList(data); len(procs) != 0 {
		t.Errorf("len(procs) = %d, want 0", len(procs))
	}
}

func TestAnyInt(t *testing.T) {
	cases := []struct {
		name string
		in   any
		want *int
	}{
		{"float64", float64(42), intPtr(42)},
		{"int", int(7), intPtr(7)},
		{"numeric string", "13", intPtr(13)},
		{"non-numeric string", "abc", nil},
		{"nil", nil, nil},
		{"bool", true, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := anyInt(tc.in)
			if (got == nil) != (tc.want == nil) {
				t.Fatalf("anyInt(%v) = %v, want %v", tc.in, got, tc.want)
			}
			if got != nil && *got != *tc.want {
				t.Errorf("anyInt(%v) = %d, want %d", tc.in, *got, *tc.want)
			}
		})
	}
}

func intPtr(i int) *int { return &i }

func TestPM2ServiceChecker_Kind(t *testing.T) {
	if got := (PM2ServiceChecker{}).Kind(); got != ServiceKindPM2 {
		t.Errorf("Kind() = %v, want %v", got, ServiceKindPM2)
	}
}

func TestPM2ServiceChecker_Check_ToolAbsentIsNotAnError(t *testing.T) {
	if runtime.GOOS == "linux" {
		t.Skip("only exercises the non-Linux/tool-absent fallback path")
	}
	statuses, err := (PM2ServiceChecker{}).Check()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if statuses != nil {
		t.Errorf("statuses = %v, want nil", statuses)
	}
}
