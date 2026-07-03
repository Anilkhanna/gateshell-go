package collector

import (
	"bufio"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// DockerServiceChecker probes `docker ps -a` for container status. Docker
// is optional infrastructure, not a hard dependency of the agent: if the
// CLI isn't installed, or the daemon isn't reachable (permissions, not
// running, etc.), Check reports no services rather than an error.
type DockerServiceChecker struct{}

func (DockerServiceChecker) Kind() ServiceKind { return ServiceKindDocker }

func (DockerServiceChecker) Check() ([]ServiceStatus, error) {
	if runtime.GOOS != "linux" {
		return nil, nil
	}
	if _, err := exec.LookPath("docker"); err != nil {
		return nil, nil // docker not installed: not detected, not an error
	}

	out, err := exec.Command("docker", "ps", "-a", "--format",
		"{{.Names}}\t{{.Image}}\t{{.Status}}\t{{.Ports}}\t{{.ID}}").Output()
	if err != nil {
		return nil, nil // daemon unreachable / permission denied: not detected
	}

	containers := parseDockerPS(string(out))
	now := time.Now()
	statuses := make([]ServiceStatus, 0, len(containers))
	for _, c := range containers {
		statuses = append(statuses, ServiceStatus{
			Kind:      ServiceKindDocker,
			Name:      c.name,
			Running:   c.running,
			Detail:    c.status,
			LastCheck: now,
		})
	}
	return statuses, nil
}

// dockerContainer is one row parsed from `docker ps -a --format`.
type dockerContainer struct {
	name    string
	image   string
	status  string
	ports   string
	id      string
	running bool
}

// parseDockerPS parses `docker ps -a --format
// '{{.Names}}\t{{.Image}}\t{{.Status}}\t{{.Ports}}\t{{.ID}}'` tab-separated
// output. Mirrors the iOS DockerParser.parseContainers semantics: a
// container is "running" when its status starts with "Up "; rows missing a
// name or ID are dropped.
func parseDockerPS(output string) []dockerContainer {
	var containers []dockerContainer

	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) < 5 {
			continue
		}

		name := strings.TrimSpace(parts[0])
		id := strings.TrimSpace(parts[4])
		if name == "" || id == "" {
			continue
		}
		status := strings.TrimSpace(parts[2])

		containers = append(containers, dockerContainer{
			name:    name,
			image:   strings.TrimSpace(parts[1]),
			status:  status,
			ports:   strings.TrimSpace(parts[3]),
			id:      id,
			running: strings.HasPrefix(status, "Up "),
		})
	}

	return containers
}
