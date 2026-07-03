//go:build sqlite

package main

import (
	"log/slog"

	"github.com/Anilkhanna/gateshell-go/internal/config"
	"github.com/Anilkhanna/gateshell-go/internal/store"
)

// newStore builds the durable SQLite-backed Store. Only compiled into
// binaries built with `-tags sqlite` (the release build; see
// .goreleaser.yaml and the Makefile's `build` target).
func newStore(cfg config.Config, logger *slog.Logger) (store.Store, error) {
	logger.Info("opening sqlite store", "path", cfg.DBPath)
	return store.OpenSQLiteStore(cfg.DBPath)
}
