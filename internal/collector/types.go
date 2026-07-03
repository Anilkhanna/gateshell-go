package collector

import "time"

// Sample is one point-in-time snapshot of host metrics + service health.
// This mirrors (and will eventually be the wire-format counterpart of) the
// metric shapes already surfaced client-side by the iOS app's local health
// parsers -- see the TODOs in reader.go for the intended ports.
type Sample struct {
	Timestamp time.Time `json:"timestamp"`

	CPUPercent float64 `json:"cpu_percent"` // 0-100, aggregate across all cores

	MemUsedMB  float64 `json:"mem_used_mb"`
	MemTotalMB float64 `json:"mem_total_mb"`

	DiskUsedGB  float64 `json:"disk_used_gb"`
	DiskTotalGB float64 `json:"disk_total_gb"`

	LoadAvg1  float64 `json:"load_avg_1"`
	LoadAvg5  float64 `json:"load_avg_5"`
	LoadAvg15 float64 `json:"load_avg_15"`

	UptimeSeconds int64 `json:"uptime_seconds"`

	NetRxBytesPerSec float64 `json:"net_rx_bytes_per_sec"`
	NetTxBytesPerSec float64 `json:"net_tx_bytes_per_sec"`

	TopProcesses []ProcessInfo   `json:"top_processes,omitempty"`
	Services     []ServiceStatus `json:"services,omitempty"`
}

// ProcessInfo describes one entry in the top-N processes by resource usage.
// TODO(TopProcessesParser): populate from a real process table read.
type ProcessInfo struct {
	PID        int     `json:"pid"`
	Name       string  `json:"name"`
	CPUPercent float64 `json:"cpu_percent"`
	MemMB      float64 `json:"mem_mb"`
}

// ServiceKind identifies which supervisor/manager a ServiceStatus came from.
type ServiceKind string

const (
	ServiceKindDocker  ServiceKind = "docker"
	ServiceKindSystemd ServiceKind = "systemd"
	ServiceKindPM2     ServiceKind = "pm2"
	ServiceKindCron    ServiceKind = "cron"
	ServiceKindUnknown ServiceKind = "unknown"
)

// ServiceStatus is the health of one monitored service/unit/container/job.
type ServiceStatus struct {
	Kind      ServiceKind `json:"kind"`
	Name      string      `json:"name"`
	Running   bool        `json:"running"`
	Detail    string      `json:"detail,omitempty"` // e.g. exit code, restart count
	LastCheck time.Time   `json:"last_check"`
}
