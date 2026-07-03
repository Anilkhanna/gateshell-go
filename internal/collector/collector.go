// Package collector periodically gathers local host metrics and service
// health into a Sample, for storage (internal/store) and alert evaluation
// (internal/alerts).
package collector

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// Collector ticks every poll interval, gathers a Sample, and hands it to
// every registered Sink (typically a store.Store.SaveSample and an
// alerts evaluator).
//
// The poll interval is reconfigurable at runtime via SetInterval (used by
// the config API's PATCH /api/v1/config endpoint) without restarting the
// process. An interval of 0 pauses collection: the loop idles until a
// positive interval is set again.
type Collector struct {
	// mu guards interval. The Run loop reads it every cycle so a
	// SetInterval call takes effect on the next tick.
	mu       sync.RWMutex
	interval time.Duration

	// reconfig wakes the Run loop early when the interval changes, so a
	// shortened (or un-paused) interval takes effect promptly instead of
	// waiting out the previous, longer sleep.
	reconfig chan struct{}

	sinks  []Sink
	logger *slog.Logger

	loadAvg  LoadAvgReader
	uptime   UptimeReader
	mem      MemoryReader
	disk     DiskReader
	cpu      CPUReader
	net      NetReader
	topProcs TopProcessesReader
	services []ServiceChecker

	// DiskPath is the mount point disk usage is measured against.
	DiskPath string
}

// Sink receives every Sample the Collector produces. Implementations should
// be fast and non-blocking; slow sinks should buffer internally.
type Sink interface {
	Handle(Sample)
}

// SinkFunc adapts a plain function to the Sink interface.
type SinkFunc func(Sample)

func (f SinkFunc) Handle(s Sample) { f(s) }

// New builds a Collector with the given tick interval. Additional sinks can
// be attached with AddSink before calling Run.
func New(interval time.Duration, logger *slog.Logger) *Collector {
	if logger == nil {
		logger = slog.Default()
	}
	return &Collector{
		interval: interval,
		reconfig: make(chan struct{}, 1),
		logger:   logger,
		DiskPath: "/",
		services: []ServiceChecker{
			DockerServiceChecker{},
			SystemdServiceChecker{},
			PM2ServiceChecker{},
			CronServiceChecker{},
		},
	}
}

// AddSink registers a Sink to receive every future Sample.
func (c *Collector) AddSink(s Sink) {
	c.sinks = append(c.sinks, s)
}

// Interval returns the current poll interval. A zero value means collection
// is paused.
func (c *Collector) Interval() time.Duration {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.interval
}

// SetInterval changes the poll interval at runtime. A value <= 0 pauses
// collection (the Run loop idles until a positive interval is set). The
// change takes effect on the next cycle -- the Run loop is woken promptly
// so a shortened interval does not have to wait out the previous sleep.
//
// Callers are responsible for validating the value (e.g. enforcing a sane
// minimum); see the config API in internal/api for the policy applied to
// app-driven changes.
func (c *Collector) SetInterval(d time.Duration) {
	c.mu.Lock()
	c.interval = d
	c.mu.Unlock()

	// Wake the Run loop, non-blocking: if a wake is already pending the
	// buffered slot is full and we simply skip -- the loop will re-read
	// the interval either way.
	select {
	case c.reconfig <- struct{}{}:
	default:
	}
}

// Run blocks until ctx is canceled. It gathers one Sample immediately on
// start (unless paused), then again after each poll interval. The interval
// is re-read every cycle so SetInterval takes effect without a restart; an
// interval of 0 pauses collection until a positive value is set.
func (c *Collector) Run(ctx context.Context) error {
	for {
		if c.Interval() > 0 {
			if err := c.tick(); err != nil {
				c.logger.Error("collection failed", "error", err)
			}
		}

		if !c.wait(ctx, c.Interval()) {
			return ctx.Err()
		}
	}
}

// wait blocks until the next tick is due, returning true to continue or
// false if ctx was canceled. When d <= 0 the collector is paused and wait
// blocks until SetInterval signals a reconfiguration (or ctx is canceled).
// A reconfiguration always wakes wait early so interval changes are picked
// up promptly.
func (c *Collector) wait(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		select {
		case <-ctx.Done():
			return false
		case <-c.reconfig:
			return true
		}
	}

	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	case <-c.reconfig:
		return true
	}
}

// tick gathers one Sample and fans it out to all registered sinks.
func (c *Collector) tick() error {
	sample, err := c.gather()
	if err != nil {
		// Gather returns partial samples on best-effort sub-reader errors;
		// only a hard failure (currently none) would abort here.
		return err
	}
	for _, sink := range c.sinks {
		sink.Handle(sample)
	}
	return nil
}

// gather assembles a single Sample from all sub-readers. Individual reader
// failures are logged and degrade that field to its zero value rather than
// aborting the whole sample -- a host with one broken /proc file shouldn't
// lose all observability.
func (c *Collector) gather() (Sample, error) {
	sample := Sample{Timestamp: time.Now().UTC()}

	load1, load5, load15, err := c.loadAvg.Read()
	if err != nil {
		c.logger.Warn("load average read failed", "error", err)
	}
	sample.LoadAvg1, sample.LoadAvg5, sample.LoadAvg15 = load1, load5, load15

	uptime, err := c.uptime.Read()
	if err != nil {
		c.logger.Warn("uptime read failed", "error", err)
	}
	sample.UptimeSeconds = int64(uptime.Seconds())

	memUsed, memTotal, err := c.mem.Read()
	if err != nil {
		c.logger.Warn("memory read failed", "error", err)
	}
	sample.MemUsedMB, sample.MemTotalMB = memUsed, memTotal

	diskUsed, diskTotal, err := c.disk.Read(c.DiskPath)
	if err != nil {
		c.logger.Warn("disk read failed", "error", err)
	}
	sample.DiskUsedGB, sample.DiskTotalGB = diskUsed, diskTotal

	cpuPct, err := c.cpu.Read()
	if err != nil {
		c.logger.Warn("cpu read failed", "error", err)
	}
	sample.CPUPercent = cpuPct

	rx, tx, err := c.net.Read()
	if err != nil {
		c.logger.Warn("net read failed", "error", err)
	}
	sample.NetRxBytesPerSec, sample.NetTxBytesPerSec = rx, tx

	procs, err := c.topProcs.Read(5)
	if err != nil {
		c.logger.Warn("top processes read failed", "error", err)
	}
	sample.TopProcesses = procs

	for _, checker := range c.services {
		statuses, err := checker.Check()
		if err != nil {
			c.logger.Warn("service check failed", "kind", checker.Kind(), "error", err)
			continue
		}
		sample.Services = append(sample.Services, statuses...)
	}

	return sample, nil
}
