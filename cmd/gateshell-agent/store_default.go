//go:build !sqlite

package main

import (
	"log/slog"

	"github.com/Anilkhanna/gateshell-go/internal/config"
	"github.com/Anilkhanna/gateshell-go/internal/store"
)

// newStore builds the Store implementation for this build. The default
// (non-"sqlite"-tagged) build uses the dependency-free in-memory store, so
// `go build ./...` works offline with no external drivers. Data does NOT
// survive a restart in this configuration -- build with `-tags sqlite` (see
// Makefile's `build` target) for a durable, production-ready binary.
func newStore(cfg config.Config, logger *slog.Logger) (store.Store, error) {
	logger.Warn("running with the in-memory store; metric history will NOT survive a restart. " +
		"Build with `-tags sqlite` for durable storage.")
	const maxInMemorySamples = 100_000 // ~17 days at a 15s poll interval
	return store.NewMemoryStore(maxInMemorySamples), nil
}
