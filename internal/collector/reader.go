package collector

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// This file is the intended landing spot for ports of the iOS Swift local
// health parsers (GateShell/Services/ServerHealthService.swift and friends)
// down to a Linux-first, /proc-based implementation. Only LoadAvgReader and
// UptimeReader are wired up for real today; everything else is a stub that
// returns zero values so the collector compiles and runs end-to-end while
// the real parsers are built out.
//
// Linux is the primary target (the agent runs on user servers, which are
// overwhelmingly Linux). macOS support below is a best-effort fallback for
// local development only -- it is NOT a target deployment platform for v1.

// LoadAvgReader reads system load averages (1/5/15 min).
type LoadAvgReader struct{}

// Read returns load1, load5, load15. On Linux it parses /proc/loadavg. On
// other platforms (macOS dev machines) it falls back to zero values rather
// than shelling out, keeping this package dependency-free; a real macOS
// fallback would use the getloadavg(3) syscall via cgo or golang.org/x/sys,
// which is deliberately out of scope for a server-targeted agent.
func (LoadAvgReader) Read() (load1, load5, load15 float64, err error) {
	if runtime.GOOS != "linux" {
		return 0, 0, 0, nil // TODO: macOS dev fallback via getloadavg(3) if ever needed
	}

	data, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return 0, 0, 0, fmt.Errorf("reading /proc/loadavg: %w", err)
	}

	fields := strings.Fields(string(data))
	if len(fields) < 3 {
		return 0, 0, 0, fmt.Errorf("unexpected /proc/loadavg format: %q", string(data))
	}

	load1, err = strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("parsing load1: %w", err)
	}
	load5, err = strconv.ParseFloat(fields[1], 64)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("parsing load5: %w", err)
	}
	load15, err = strconv.ParseFloat(fields[2], 64)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("parsing load15: %w", err)
	}
	return load1, load5, load15, nil
}

// UptimeReader reads system uptime.
type UptimeReader struct{}

// Read returns uptime as a time.Duration. On Linux it parses /proc/uptime
// (first field, seconds as a float). On other platforms it returns 0 with
// no error -- see LoadAvgReader.Read for rationale.
func (UptimeReader) Read() (time.Duration, error) {
	if runtime.GOOS != "linux" {
		return 0, nil // TODO: macOS dev fallback (sysctl kern.boottime) if ever needed
	}

	data, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return 0, fmt.Errorf("reading /proc/uptime: %w", err)
	}

	fields := strings.Fields(string(data))
	if len(fields) < 1 {
		return 0, fmt.Errorf("unexpected /proc/uptime format: %q", string(data))
	}

	seconds, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return 0, fmt.Errorf("parsing uptime seconds: %w", err)
	}
	return time.Duration(seconds * float64(time.Second)), nil
}

// MemoryReader reads memory usage (used/total, MB).
//
// TODO(MemoryParser): port of the iOS Swift MemoryParser. On Linux, parse
// /proc/meminfo (MemTotal, MemAvailable) to compute used = total - available.
type MemoryReader struct{}

// Read returns usedMB, totalMB.
func (MemoryReader) Read() (usedMB, totalMB float64, err error) {
	// TODO: parse /proc/meminfo on Linux.
	return 0, 0, nil
}

// DiskReader reads disk usage for a given mount point (used/total, GB).
//
// TODO(DiskParser): port of the iOS Swift DiskParser. On Linux, use
// golang.org/x/sys/unix.Statfs on the target path, or shell out to `df`.
type DiskReader struct{}

// Read returns usedGB, totalGB for the given mount path (e.g. "/").
func (DiskReader) Read(path string) (usedGB, totalGB float64, err error) {
	// TODO: implement via unix.Statfs(path, &stat) or `df -k path`.
	return 0, 0, nil
}

// CPUReader reads aggregate CPU utilization as a percentage (0-100).
//
// TODO(CPUParser): port of the iOS Swift CPUParser. On Linux, sample
// /proc/stat twice with a short interval and compute the delta of
// (user+nice+system+...) vs idle+iowait.
type CPUReader struct{}

// Read returns the current aggregate CPU percentage.
func (CPUReader) Read() (percent float64, err error) {
	// TODO: implement via two /proc/stat samples across a short interval.
	return 0, nil
}

// NetReader reads network throughput (bytes/sec) since the last sample.
//
// TODO: port of the iOS Swift network throughput logic. On Linux, parse
// /proc/net/dev and diff cumulative rx/tx byte counters across ticks.
type NetReader struct{}

// Read returns rxBytesPerSec, txBytesPerSec.
func (NetReader) Read() (rxBytesPerSec, txBytesPerSec float64, err error) {
	// TODO: implement via /proc/net/dev diffing between collector ticks.
	return 0, 0, nil
}

// TopProcessesReader reads the top-N processes by CPU/memory usage.
//
// TODO(TopProcessesParser): port of the iOS Swift TopProcessesParser. On
// Linux, walk /proc/[pid]/stat and /proc/[pid]/status for each running PID.
type TopProcessesReader struct{}

// Read returns up to n ProcessInfo entries, sorted by CPU usage descending.
func (TopProcessesReader) Read(n int) ([]ProcessInfo, error) {
	// TODO: implement via /proc/[pid] walk.
	return nil, nil
}

// ServiceChecker probes the health of external service supervisors
// (docker, systemd, pm2, cron). Each concrete checker below is a stub that
// returns an empty slice; wiring these up requires shelling out to (or
// linking against) the respective tool's status API.
type ServiceChecker interface {
	Kind() ServiceKind
	Check() ([]ServiceStatus, error)
}

// DockerServiceChecker probes `docker ps` / the Docker Engine API.
//
// TODO: implement via the Docker Engine API (unix socket) or `docker ps
// --format json` if we want to avoid an SDK dependency.
type DockerServiceChecker struct{}

func (DockerServiceChecker) Kind() ServiceKind { return ServiceKindDocker }
func (DockerServiceChecker) Check() ([]ServiceStatus, error) {
	return nil, nil
}

// SystemdServiceChecker probes `systemctl` unit status.
//
// TODO: implement via `systemctl show <unit> --property=ActiveState` or by
// talking to systemd over D-Bus.
type SystemdServiceChecker struct{}

func (SystemdServiceChecker) Kind() ServiceKind { return ServiceKindSystemd }
func (SystemdServiceChecker) Check() ([]ServiceStatus, error) {
	return nil, nil
}

// PM2ServiceChecker probes `pm2 jlist` for Node.js process status.
//
// TODO: implement via `pm2 jlist` (JSON) parsing.
type PM2ServiceChecker struct{}

func (PM2ServiceChecker) Kind() ServiceKind { return ServiceKindPM2 }
func (PM2ServiceChecker) Check() ([]ServiceStatus, error) {
	return nil, nil
}

// CronServiceChecker inspects cron job health (e.g. via a heartbeat file
// convention, or crontab -l presence).
//
// TODO: define and implement a concrete health signal for cron jobs.
type CronServiceChecker struct{}

func (CronServiceChecker) Kind() ServiceKind { return ServiceKindCron }
func (CronServiceChecker) Check() ([]ServiceStatus, error) {
	return nil, nil
}
