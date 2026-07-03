//go:build sqlite

// This file is only compiled when building with `-tags sqlite`, e.g.:
//
//	go build -tags sqlite ./...
//
// The default `go build ./...` (no tags) excludes this file entirely, so
// the module compiles offline with only stdlib + cobra. This keeps
// `go mod tidy` / CI dev loops dependency-light while still letting the
// release build (see .goreleaser.yaml, which always passes -tags sqlite)
// ship a real embedded database. modernc.org/sqlite is a pure-Go SQLite
// driver -- no cgo, no system SQLite library required.

package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	_ "modernc.org/sqlite"

	"github.com/Anilkhanna/gateshell-go/internal/collector"
)

// schema creates the samples table (raw, full-resolution rows) plus the
// indexes the API's range queries rely on. Downsampled tiers (minutely/
// hourly) reuse the same table shape; see TODO in ApplyRetention below.
const schema = `
CREATE TABLE IF NOT EXISTS samples (
	id                   INTEGER PRIMARY KEY AUTOINCREMENT,
	timestamp            INTEGER NOT NULL, -- unix seconds, UTC
	tier                 TEXT    NOT NULL DEFAULT 'raw', -- raw | minutely | hourly
	cpu_percent          REAL    NOT NULL,
	mem_used_mb          REAL    NOT NULL,
	mem_total_mb         REAL    NOT NULL,
	disk_used_gb         REAL    NOT NULL,
	disk_total_gb        REAL    NOT NULL,
	load_avg_1           REAL    NOT NULL,
	load_avg_5           REAL    NOT NULL,
	load_avg_15          REAL    NOT NULL,
	uptime_seconds       INTEGER NOT NULL,
	net_rx_bytes_per_sec REAL    NOT NULL,
	net_tx_bytes_per_sec REAL    NOT NULL,
	top_processes_json   TEXT,             -- JSON-encoded []collector.ProcessInfo
	services_json        TEXT              -- JSON-encoded []collector.ServiceStatus
);

CREATE INDEX IF NOT EXISTS idx_samples_timestamp ON samples (timestamp);
CREATE INDEX IF NOT EXISTS idx_samples_tier_timestamp ON samples (tier, timestamp);
`

// SQLiteStore is a durable Store backed by an embedded SQLite database file.
type SQLiteStore struct {
	db *sql.DB
}

var _ Store = (*SQLiteStore)(nil)

// OpenSQLiteStore opens (creating if necessary) the SQLite database at path
// and ensures the schema exists.
func OpenSQLiteStore(path string) (*SQLiteStore, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("sqlite: opening %q: %w", path, err)
	}
	// A single writer connection avoids SQLITE_BUSY under the agent's
	// modest write volume (one sample per PollInterval); reads are cheap
	// enough to share it too at this scale.
	db.SetMaxOpenConns(1)

	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("sqlite: applying schema: %w", err)
	}

	return &SQLiteStore{db: db}, nil
}

func (s *SQLiteStore) SaveSample(ctx context.Context, sample collector.Sample) error {
	topProcsJSON, err := json.Marshal(sample.TopProcesses)
	if err != nil {
		return fmt.Errorf("sqlite: marshaling top processes: %w", err)
	}
	servicesJSON, err := json.Marshal(sample.Services)
	if err != nil {
		return fmt.Errorf("sqlite: marshaling services: %w", err)
	}

	const insert = `
INSERT INTO samples (
	timestamp, tier, cpu_percent, mem_used_mb, mem_total_mb,
	disk_used_gb, disk_total_gb, load_avg_1, load_avg_5, load_avg_15,
	uptime_seconds, net_rx_bytes_per_sec, net_tx_bytes_per_sec,
	top_processes_json, services_json
) VALUES (?, 'raw', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	_, err = s.db.ExecContext(ctx, insert,
		sample.Timestamp.UTC().Unix(),
		sample.CPUPercent, sample.MemUsedMB, sample.MemTotalMB,
		sample.DiskUsedGB, sample.DiskTotalGB,
		sample.LoadAvg1, sample.LoadAvg5, sample.LoadAvg15,
		sample.UptimeSeconds, sample.NetRxBytesPerSec, sample.NetTxBytesPerSec,
		string(topProcsJSON), string(servicesJSON),
	)
	if err != nil {
		return fmt.Errorf("sqlite: inserting sample: %w", err)
	}
	return nil
}

func (s *SQLiteStore) QueryRange(ctx context.Context, from, to time.Time) ([]collector.Sample, error) {
	const query = `
SELECT timestamp, cpu_percent, mem_used_mb, mem_total_mb, disk_used_gb,
       disk_total_gb, load_avg_1, load_avg_5, load_avg_15, uptime_seconds,
       net_rx_bytes_per_sec, net_tx_bytes_per_sec, top_processes_json, services_json
FROM samples
WHERE timestamp BETWEEN ? AND ?
ORDER BY timestamp ASC`

	rows, err := s.db.QueryContext(ctx, query, from.UTC().Unix(), to.UTC().Unix())
	if err != nil {
		return nil, fmt.Errorf("sqlite: querying range: %w", err)
	}
	defer rows.Close()

	var results []collector.Sample
	for rows.Next() {
		var (
			ts                     int64
			topProcsJSON, svcsJSON string
			sample                 collector.Sample
		)
		if err := rows.Scan(
			&ts, &sample.CPUPercent, &sample.MemUsedMB, &sample.MemTotalMB,
			&sample.DiskUsedGB, &sample.DiskTotalGB,
			&sample.LoadAvg1, &sample.LoadAvg5, &sample.LoadAvg15,
			&sample.UptimeSeconds, &sample.NetRxBytesPerSec, &sample.NetTxBytesPerSec,
			&topProcsJSON, &svcsJSON,
		); err != nil {
			return nil, fmt.Errorf("sqlite: scanning row: %w", err)
		}
		sample.Timestamp = time.Unix(ts, 0).UTC()
		_ = json.Unmarshal([]byte(topProcsJSON), &sample.TopProcesses)
		_ = json.Unmarshal([]byte(svcsJSON), &sample.Services)
		results = append(results, sample)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: iterating rows: %w", err)
	}
	return results, nil
}

func (s *SQLiteStore) LatestSample(ctx context.Context) (collector.Sample, error) {
	const query = `
SELECT timestamp, cpu_percent, mem_used_mb, mem_total_mb, disk_used_gb,
       disk_total_gb, load_avg_1, load_avg_5, load_avg_15, uptime_seconds,
       net_rx_bytes_per_sec, net_tx_bytes_per_sec, top_processes_json, services_json
FROM samples
ORDER BY timestamp DESC
LIMIT 1`

	var (
		ts                     int64
		topProcsJSON, svcsJSON string
		sample                 collector.Sample
	)
	err := s.db.QueryRowContext(ctx, query).Scan(
		&ts, &sample.CPUPercent, &sample.MemUsedMB, &sample.MemTotalMB,
		&sample.DiskUsedGB, &sample.DiskTotalGB,
		&sample.LoadAvg1, &sample.LoadAvg5, &sample.LoadAvg15,
		&sample.UptimeSeconds, &sample.NetRxBytesPerSec, &sample.NetTxBytesPerSec,
		&topProcsJSON, &svcsJSON,
	)
	if err == sql.ErrNoRows {
		return collector.Sample{}, ErrNoSamples
	}
	if err != nil {
		return collector.Sample{}, fmt.Errorf("sqlite: querying latest: %w", err)
	}
	sample.Timestamp = time.Unix(ts, 0).UTC()
	_ = json.Unmarshal([]byte(topProcsJSON), &sample.TopProcesses)
	_ = json.Unmarshal([]byte(svcsJSON), &sample.Services)
	return sample, nil
}

// ApplyRetention downsamples/prunes rows per policy.
//
// TODO: this currently only implements the final "prune anything past the
// oldest tier" step for real; the minutely/hourly downsampling steps
// (aggregate raw rows into one representative row per bucket, re-tag as
// tier='minutely'/'hourly', delete the source rows) are left as TODOs with
// the intended SQL sketched below. Wire up a periodic caller (e.g. hourly
// ticker in cmd/gateshell-agent's serve command) once implemented.
func (s *SQLiteStore) ApplyRetention(ctx context.Context, policy RetentionPolicy) error {
	// TODO(downsample-minutely): bucket raw rows older than policy.Raw.MaxAge
	// into 1-per-minute averages:
	//
	//   INSERT INTO samples (timestamp, tier, cpu_percent, ...)
	//   SELECT (timestamp / 60) * 60, 'minutely', AVG(cpu_percent), ...
	//   FROM samples
	//   WHERE tier = 'raw' AND timestamp < ?
	//   GROUP BY timestamp / 60;
	//
	//   DELETE FROM samples WHERE tier = 'raw' AND timestamp < ?;

	// TODO(downsample-hourly): same idea, bucketing 'minutely' rows older
	// than policy.Minutely.MaxAge into 1-per-hour averages tagged 'hourly'.

	// Final tier: hard-delete anything older than the last retention
	// window regardless of tier. This part is real.
	cutoff := time.Now().Add(-policy.Hourly.MaxAge).UTC().Unix()
	if _, err := s.db.ExecContext(ctx, `DELETE FROM samples WHERE timestamp < ?`, cutoff); err != nil {
		return fmt.Errorf("sqlite: pruning expired samples: %w", err)
	}
	return nil
}

func (s *SQLiteStore) Close() error {
	return s.db.Close()
}
