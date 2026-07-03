package store

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/Anilkhanna/gateshell-go/internal/collector"
)

// MemoryStore is a dependency-free, in-memory Store. It is the default
// store used when the binary is built WITHOUT the `sqlite` build tag.
// History is lost on restart -- this is intended for quick local testing,
// not production deployment (use the sqlite-tagged build for that).
type MemoryStore struct {
	mu      sync.RWMutex
	samples []collector.Sample

	// MaxSamples caps memory growth by evicting the oldest sample once
	// exceeded. Zero means unbounded (not recommended for long-running use).
	MaxSamples int
}

// NewMemoryStore returns an empty MemoryStore. maxSamples <= 0 means
// unbounded.
func NewMemoryStore(maxSamples int) *MemoryStore {
	return &MemoryStore{MaxSamples: maxSamples}
}

var _ Store = (*MemoryStore)(nil)

func (m *MemoryStore) SaveSample(_ context.Context, sample collector.Sample) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.samples = append(m.samples, sample)
	if m.MaxSamples > 0 && len(m.samples) > m.MaxSamples {
		overflow := len(m.samples) - m.MaxSamples
		m.samples = m.samples[overflow:]
	}
	return nil
}

func (m *MemoryStore) QueryRange(_ context.Context, from, to time.Time) ([]collector.Sample, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// samples are appended in chronological order, so a linear scan with an
	// early break is sufficient at the scale this store targets (dev/test).
	start := sort.Search(len(m.samples), func(i int) bool {
		return !m.samples[i].Timestamp.Before(from)
	})

	result := make([]collector.Sample, 0, len(m.samples)-start)
	for _, s := range m.samples[start:] {
		if s.Timestamp.After(to) {
			break
		}
		result = append(result, s)
	}
	return result, nil
}

func (m *MemoryStore) LatestSample(_ context.Context) (collector.Sample, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if len(m.samples) == 0 {
		return collector.Sample{}, ErrNoSamples
	}
	return m.samples[len(m.samples)-1], nil
}

// ApplyRetention is a no-op for MemoryStore beyond the MaxSamples cap
// already enforced on write; there is no separate downsampled tier to
// maintain in memory.
//
// TODO: if in-memory downsampling is ever needed (e.g. for a "live demo"
// mode with no disk access at all), implement per-tier bucketing here.
func (m *MemoryStore) ApplyRetention(_ context.Context, _ RetentionPolicy) error {
	return nil
}

func (m *MemoryStore) Close() error { return nil }
