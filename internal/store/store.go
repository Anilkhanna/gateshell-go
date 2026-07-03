// Package store persists collector.Sample values with tiered retention and
// serves range queries for the API layer.
//
// Two implementations are provided:
//   - memory.go: an in-memory ring-buffer-ish store with no external
//     dependencies. This is the DEFAULT build (no build tags) so that
//     `go build ./...` works offline and the binary is usable out of the
//     box for short-lived/dev usage. History does not survive a restart.
//   - sqlite.go: a durable, embedded SQLite store using the pure-Go
//     modernc.org/sqlite driver (no cgo). It is gated behind the `sqlite`
//     build tag -- build the real release binary with
//     `go build -tags sqlite ./...` (see Makefile / .goreleaser.yaml).
package store

import (
	"context"
	"time"

	"github.com/Anilkhanna/gateshell-go/internal/collector"
)

// Store is the persistence interface the collector and API depend on.
// Implementations must be safe for concurrent use.
type Store interface {
	// SaveSample persists one collector.Sample.
	SaveSample(ctx context.Context, sample collector.Sample) error

	// QueryRange returns all samples with Timestamp in [from, to], ordered
	// by Timestamp ascending. Implementations may apply retention-based
	// downsampling transparently for older ranges (see RetentionPolicy).
	QueryRange(ctx context.Context, from, to time.Time) ([]collector.Sample, error)

	// LatestSample returns the most recently saved sample, or
	// ErrNoSamples if the store is empty.
	LatestSample(ctx context.Context) (collector.Sample, error)

	// ApplyRetention downsamples/prunes data older than the tiers defined
	// by policy. Intended to be called periodically (e.g. once per hour)
	// by a background task, not on every write.
	ApplyRetention(ctx context.Context, policy RetentionPolicy) error

	// Close releases any resources (file handles, connections) held by
	// the store.
	Close() error
}

// ErrNoSamples is returned by LatestSample when the store has never
// received a sample.
var ErrNoSamples = errNoSamples{}

type errNoSamples struct{}

func (errNoSamples) Error() string { return "store: no samples available" }

// RetentionPolicy defines tiered retention: keep full-resolution samples for
// Raw, then downsample to coarser granularity for older data, finally
// dropping anything past the last tier's cutoff.
//
// Example default tiers (see config defaults / TODO in sqlite.go):
//   - Raw:      keep every sample for 24h
//   - Minutely: downsample to 1-per-minute for 7d
//   - Hourly:   downsample to 1-per-hour for 90d
//   - anything older than Hourly.MaxAge is deleted
type RetentionPolicy struct {
	Raw      RetentionTier
	Minutely RetentionTier
	Hourly   RetentionTier
}

// RetentionTier describes one retention granularity.
type RetentionTier struct {
	// MaxAge is how long data at this tier's resolution is retained,
	// measured from "now" at the time ApplyRetention runs.
	MaxAge time.Duration
}

// DefaultRetentionPolicy returns sensible tiered-retention defaults.
func DefaultRetentionPolicy() RetentionPolicy {
	return RetentionPolicy{
		Raw:      RetentionTier{MaxAge: 24 * time.Hour},
		Minutely: RetentionTier{MaxAge: 7 * 24 * time.Hour},
		Hourly:   RetentionTier{MaxAge: 90 * 24 * time.Hour},
	}
}
