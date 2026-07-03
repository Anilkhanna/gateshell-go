// Package alerts evaluates collector.Sample values against threshold rules
// and publishes triggered alerts to ntfy (https://ntfy.sh or self-hosted).
package alerts

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/Anilkhanna/gateshell-go/internal/collector"
)

// Metric identifies which Sample field a Rule watches.
type Metric string

const (
	MetricCPUPercent  Metric = "cpu_percent"
	MetricMemPercent  Metric = "mem_percent"  // derived: mem_used_mb / mem_total_mb * 100
	MetricDiskPercent Metric = "disk_percent" // derived: disk_used_gb / disk_total_gb * 100
	MetricLoadAvg1    Metric = "load_avg_1"
)

// Comparator is how a Rule's Threshold is compared against the observed
// metric value.
type Comparator string

const (
	ComparatorGreaterThan Comparator = "gt"
	ComparatorLessThan    Comparator = "lt"
)

// Rule is a single threshold alert definition: fire when Metric has been on
// the wrong side of Threshold (per Comparator) continuously for at least
// For duration.
//
// A separate class of rule -- "service up/down" -- is represented by
// ServiceRule below, since it watches collector.ServiceStatus rather than a
// numeric metric.
type Rule struct {
	Name       string
	Metric     Metric
	Comparator Comparator
	Threshold  float64
	For        time.Duration
}

// ServiceRule fires when a named service's Running state changes (flips
// down, or optionally flips back up -- see NotifyOnRecovery).
type ServiceRule struct {
	Name             string // human-readable alert name
	ServiceName      string // must match collector.ServiceStatus.Name
	NotifyOnRecovery bool
}

// TODO(persistence): Rules/ServiceRules are currently supplied in-memory by
// the caller (see Evaluator.SetRules); there is no persistence layer yet.
// A future iteration should load/save these from the Store (or a small
// dedicated config file) so they survive restarts and can be managed
// remotely by the mobile app.

// state tracks how long a numeric Rule's condition has been continuously
// true, to implement the "for N duration" debounce.
type ruleState struct {
	conditionSince time.Time
	firing         bool
}

// Evaluator watches incoming samples against a set of Rules/ServiceRules
// and publishes to a Publisher when a rule transitions into (or, for
// services, out of) an alerting state.
type Evaluator struct {
	mu            sync.Mutex
	rules         []Rule
	serviceRules  []ServiceRule
	ruleStates    map[string]*ruleState
	serviceStates map[string]bool // last known Running state, by service name

	publisher Publisher
	logger    *slog.Logger
}

// NewEvaluator builds an Evaluator that publishes triggered alerts via pub.
func NewEvaluator(pub Publisher, logger *slog.Logger) *Evaluator {
	if logger == nil {
		logger = slog.Default()
	}
	return &Evaluator{
		ruleStates:    make(map[string]*ruleState),
		serviceStates: make(map[string]bool),
		publisher:     pub,
		logger:        logger,
	}
}

// SetRules replaces the current rule set.
func (e *Evaluator) SetRules(rules []Rule, serviceRules []ServiceRule) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.rules = rules
	e.serviceRules = serviceRules
}

// Handle implements collector.Sink, so an Evaluator can be attached directly
// to a collector.Collector via AddSink.
func (e *Evaluator) Handle(sample collector.Sample) {
	e.evaluate(context.Background(), sample)
}

func (e *Evaluator) evaluate(ctx context.Context, sample collector.Sample) {
	e.mu.Lock()
	rules := e.rules
	serviceRules := e.serviceRules
	e.mu.Unlock()

	for _, rule := range rules {
		e.evaluateRule(ctx, rule, sample)
	}
	for _, rule := range serviceRules {
		e.evaluateServiceRule(ctx, rule, sample)
	}
}

func (e *Evaluator) evaluateRule(ctx context.Context, rule Rule, sample collector.Sample) {
	value, ok := metricValue(rule.Metric, sample)
	if !ok {
		return
	}

	conditionMet := false
	switch rule.Comparator {
	case ComparatorGreaterThan:
		conditionMet = value > rule.Threshold
	case ComparatorLessThan:
		conditionMet = value < rule.Threshold
	default:
		e.logger.Warn("unknown comparator", "rule", rule.Name, "comparator", rule.Comparator)
		return
	}

	e.mu.Lock()
	state, exists := e.ruleStates[rule.Name]
	if !exists {
		state = &ruleState{}
		e.ruleStates[rule.Name] = state
	}

	if !conditionMet {
		state.conditionSince = time.Time{}
		wasFiring := state.firing
		state.firing = false
		e.mu.Unlock()
		if wasFiring {
			e.publish(ctx, fmt.Sprintf("%s recovered (%s = %.1f)", rule.Name, rule.Metric, value))
		}
		return
	}

	if state.conditionSince.IsZero() {
		state.conditionSince = sample.Timestamp
	}
	shouldFire := !state.firing && sample.Timestamp.Sub(state.conditionSince) >= rule.For
	if shouldFire {
		state.firing = true
	}
	e.mu.Unlock()

	if shouldFire {
		e.publish(ctx, fmt.Sprintf("%s: %s = %.1f (threshold %.1f for %s)",
			rule.Name, rule.Metric, value, rule.Threshold, rule.For))
	}
}

func (e *Evaluator) evaluateServiceRule(ctx context.Context, rule ServiceRule, sample collector.Sample) {
	var current *collector.ServiceStatus
	for i := range sample.Services {
		if sample.Services[i].Name == rule.ServiceName {
			current = &sample.Services[i]
			break
		}
	}
	if current == nil {
		return // service not observed in this sample; nothing to compare
	}

	e.mu.Lock()
	previous, known := e.serviceStates[rule.ServiceName]
	e.serviceStates[rule.ServiceName] = current.Running
	e.mu.Unlock()

	if !known {
		return // first observation; no transition to alert on
	}

	if previous && !current.Running {
		e.publish(ctx, fmt.Sprintf("%s: service %q is DOWN", rule.Name, rule.ServiceName))
	} else if !previous && current.Running && rule.NotifyOnRecovery {
		e.publish(ctx, fmt.Sprintf("%s: service %q recovered", rule.Name, rule.ServiceName))
	}
}

func (e *Evaluator) publish(ctx context.Context, message string) {
	if e.publisher == nil {
		e.logger.Warn("alert triggered but no publisher configured", "message", message)
		return
	}
	if err := e.publisher.Publish(ctx, message); err != nil {
		e.logger.Error("publishing alert failed", "error", err, "message", message)
	}
}

// metricValue extracts (possibly deriving) a Metric's value from a Sample.
func metricValue(metric Metric, sample collector.Sample) (float64, bool) {
	switch metric {
	case MetricCPUPercent:
		return sample.CPUPercent, true
	case MetricMemPercent:
		if sample.MemTotalMB == 0 {
			return 0, false
		}
		return sample.MemUsedMB / sample.MemTotalMB * 100, true
	case MetricDiskPercent:
		if sample.DiskTotalGB == 0 {
			return 0, false
		}
		return sample.DiskUsedGB / sample.DiskTotalGB * 100, true
	case MetricLoadAvg1:
		return sample.LoadAvg1, true
	default:
		return 0, false
	}
}

// Publisher delivers an alert message somewhere (ntfy, in v1).
type Publisher interface {
	Publish(ctx context.Context, message string) error
}

// NtfyPublisher publishes alert messages to an ntfy topic via a simple HTTP
// POST, per https://docs.ntfy.sh/publish/.
type NtfyPublisher struct {
	// TopicURL is the full ntfy topic URL, e.g.
	// "https://ntfy.sh/my-gateshell-topic" or a self-hosted equivalent.
	TopicURL string

	Client *http.Client
}

var _ Publisher = (*NtfyPublisher)(nil)

// NewNtfyPublisher builds a publisher for the given topic URL.
func NewNtfyPublisher(topicURL string) *NtfyPublisher {
	return &NtfyPublisher{
		TopicURL: topicURL,
		Client:   &http.Client{Timeout: 10 * time.Second},
	}
}

// Publish sends message as the ntfy notification body.
//
// TODO: support ntfy priority/tags/title headers (e.g. tag "warning" for
// threshold alerts, "rotating_light" for catastrophic ones) once alert
// severity levels are modeled -- currently every alert is a plain
// default-priority message.
func (p *NtfyPublisher) Publish(ctx context.Context, message string) error {
	if p.TopicURL == "" {
		return fmt.Errorf("alerts: ntfy topic not configured")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.TopicURL, bytes.NewBufferString(message))
	if err != nil {
		return fmt.Errorf("alerts: building ntfy request: %w", err)
	}
	req.Header.Set("Content-Type", "text/plain; charset=utf-8")

	resp, err := p.Client.Do(req)
	if err != nil {
		return fmt.Errorf("alerts: publishing to ntfy: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		return fmt.Errorf("alerts: ntfy returned status %d", resp.StatusCode)
	}
	return nil
}
