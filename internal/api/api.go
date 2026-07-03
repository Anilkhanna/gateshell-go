// Package api serves the REST + streaming API the GateShell mobile app
// polls/subscribes to for metrics and service health.
//
// It also exposes a small config surface (GET/PATCH /api/v1/config) so the
// app can read and change how often the agent polls (V1-10): a PATCH is
// applied to the running collector immediately (no restart) and persisted
// to the config file so it survives one.
//
// Transport choice: this implements the live-update endpoint as
// Server-Sent Events (GET /api/v1/stream) over plain net/http, rather than
// a WebSocket, to keep the module dependency-free by default (stdlib only).
// SSE is sufficient for a one-way "push new samples to the app" feed and
// works fine through the same Bearer-token middleware and TLS termination
// as every other endpoint. If bidirectional messaging is ever needed
// (e.g. the app pushing commands back to the agent over the same
// connection), swap this handler for github.com/coder/websocket -- the
// route and auth middleware are structured so that's a localized change.
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Anilkhanna/gateshell-go/internal/collector"
	"github.com/Anilkhanna/gateshell-go/internal/store"
)

// MinPollInterval is the smallest non-zero poll interval the config API
// will accept, to stop the app from hammering the host. A value of 0 is
// always allowed and pauses collection.
const MinPollInterval = 5 * time.Second

// IntervalController lets the config API read and change the collector's
// poll interval at runtime. *collector.Collector satisfies it.
type IntervalController interface {
	Interval() time.Duration
	SetInterval(d time.Duration)
}

// PersistIntervalFunc persists a changed poll interval so it survives a
// restart (typically config.SavePollInterval bound to the config file
// path). It may be nil, in which case changes apply at runtime only.
type PersistIntervalFunc func(d time.Duration) error

// Server wires the HTTP handlers to a Store and broadcasts live samples to
// connected /api/v1/stream clients.
type Server struct {
	store        store.Store
	pairingToken string
	serverName   string
	logger       *slog.Logger

	intervalCtl IntervalController
	persist     PersistIntervalFunc

	mux *http.ServeMux

	broadcastMu sync.RWMutex
	subscribers map[chan collector.Sample]struct{}
}

// NewServer builds a Server ready to be used as an http.Handler, or run via
// ListenAndServe.
//
// intervalCtl drives GET/PATCH /api/v1/config; persist (may be nil)
// persists a PATCHed interval so it survives a restart.
func NewServer(
	st store.Store,
	pairingToken, serverName string,
	intervalCtl IntervalController,
	persist PersistIntervalFunc,
	logger *slog.Logger,
) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	s := &Server{
		store:        st,
		pairingToken: pairingToken,
		serverName:   serverName,
		intervalCtl:  intervalCtl,
		persist:      persist,
		logger:       logger,
		subscribers:  make(map[chan collector.Sample]struct{}),
	}
	s.mux = s.routes()
	return s
}

// ServeHTTP implements http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

// BroadcastSample fans a newly-collected sample out to every connected
// /api/v1/stream client. Intended to be wired as a collector.Sink by
// cmd/gateshell-agent's `serve` command.
func (s *Server) BroadcastSample(sample collector.Sample) {
	s.broadcastMu.RLock()
	defer s.broadcastMu.RUnlock()

	for ch := range s.subscribers {
		select {
		case ch <- sample:
		default:
			// Slow subscriber; drop the sample rather than blocking the
			// collector tick. The client will catch up via /api/v1/latest
			// or /api/v1/metrics on reconnect.
		}
	}
}

func (s *Server) routes() *http.ServeMux {
	mux := http.NewServeMux()

	// /healthz is intentionally unauthenticated so external uptime
	// monitors / load balancers can probe it without a token.
	mux.HandleFunc("GET /healthz", s.handleHealthz)

	mux.Handle("GET /api/v1/metrics", s.authed(http.HandlerFunc(s.handleMetrics)))
	mux.Handle("GET /api/v1/services", s.authed(http.HandlerFunc(s.handleServices)))
	mux.Handle("GET /api/v1/latest", s.authed(http.HandlerFunc(s.handleLatest)))
	mux.Handle("GET /api/v1/stream", s.authed(http.HandlerFunc(s.handleStream)))
	mux.Handle("GET /api/v1/config", s.authed(http.HandlerFunc(s.handleGetConfig)))
	mux.Handle("PATCH /api/v1/config", s.authed(http.HandlerFunc(s.handlePatchConfig)))

	return mux
}

// authed wraps next with Bearer-token validation against the configured
// pairing token. See internal/pair for token generation/validation
// semantics (constant-time compare, no-token-means-refuse-all).
func (s *Server) authed(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		token, ok := strings.CutPrefix(authHeader, "Bearer ")
		if !ok || !constantTimeTokenEqual(s.pairingToken, token) {
			writeError(w, http.StatusUnauthorized, "missing or invalid bearer token")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// constantTimeTokenEqual is a thin wrapper so this file doesn't need to
// import internal/pair just for the comparison (avoids a package cycle
// risk if pair ever needs api's types); logic mirrors pair.Validate.
func constantTimeTokenEqual(expected, candidate string) bool {
	if expected == "" {
		return false
	}
	if len(expected) != len(candidate) {
		return false
	}
	var diff byte
	for i := 0; i < len(expected); i++ {
		diff |= expected[i] ^ candidate[i]
	}
	return diff == 0
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"status": "ok",
		"server": s.serverName,
	})
}

func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	from, to, err := parseRange(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	samples, err := s.store.QueryRange(r.Context(), from, to)
	if err != nil {
		s.logger.Error("query range failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to query metrics")
		return
	}
	writeJSON(w, http.StatusOK, samples)
}

func (s *Server) handleServices(w http.ResponseWriter, r *http.Request) {
	sample, err := s.store.LatestSample(r.Context())
	if err != nil {
		if err == store.ErrNoSamples {
			writeJSON(w, http.StatusOK, []collector.ServiceStatus{})
			return
		}
		s.logger.Error("latest sample failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to load services")
		return
	}
	writeJSON(w, http.StatusOK, sample.Services)
}

func (s *Server) handleLatest(w http.ResponseWriter, r *http.Request) {
	sample, err := s.store.LatestSample(r.Context())
	if err != nil {
		if err == store.ErrNoSamples {
			writeError(w, http.StatusNotFound, "no samples collected yet")
			return
		}
		s.logger.Error("latest sample failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to load latest sample")
		return
	}
	writeJSON(w, http.StatusOK, sample)
}

// configPayload is the request/response body for the config endpoints.
// PollInterval is a Go duration string (e.g. "60s"); "0s" means paused.
type configPayload struct {
	PollInterval string `json:"pollInterval"`
}

// handleGetConfig returns the collector's current poll interval (V1-10).
func (s *Server) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	if s.intervalCtl == nil {
		writeError(w, http.StatusServiceUnavailable, "config controller not available")
		return
	}
	writeJSON(w, http.StatusOK, configPayload{
		PollInterval: s.intervalCtl.Interval().String(),
	})
}

// handlePatchConfig validates and applies a new poll interval at runtime,
// then persists it so it survives a restart (V1-10). A value of 0 pauses
// collection; negatives are rejected; non-zero values below MinPollInterval
// are rejected.
func (s *Server) handlePatchConfig(w http.ResponseWriter, r *http.Request) {
	if s.intervalCtl == nil {
		writeError(w, http.StatusServiceUnavailable, "config controller not available")
		return
	}

	var payload configPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if payload.PollInterval == "" {
		writeError(w, http.StatusBadRequest, "pollInterval is required")
		return
	}

	d, err := time.ParseDuration(payload.PollInterval)
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid pollInterval: %v", err))
		return
	}
	if d < 0 {
		writeError(w, http.StatusBadRequest, "pollInterval must not be negative")
		return
	}
	if d > 0 && d < MinPollInterval {
		writeError(w, http.StatusBadRequest,
			fmt.Sprintf("pollInterval must be 0 (paused) or at least %s", MinPollInterval))
		return
	}

	// Apply at runtime first -- this cannot fail and is what the user
	// asked for; persistence is best-effort on top.
	s.intervalCtl.SetInterval(d)

	if s.persist != nil {
		if err := s.persist(d); err != nil {
			// The change is live, but won't survive a restart. Surface
			// this rather than silently claiming success.
			s.logger.Error("persisting poll interval failed", "error", err, "interval", d)
			writeError(w, http.StatusInternalServerError,
				"poll interval applied at runtime but could not be persisted; it will not survive a restart")
			return
		}
	}

	writeJSON(w, http.StatusOK, configPayload{PollInterval: d.String()})
}

// handleStream serves live samples as Server-Sent Events. See the package
// doc comment for why SSE was chosen over a WebSocket.
func (s *Server) handleStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming not supported")
		return
	}

	ch := make(chan collector.Sample, 8)
	s.broadcastMu.Lock()
	s.subscribers[ch] = struct{}{}
	s.broadcastMu.Unlock()

	defer func() {
		s.broadcastMu.Lock()
		delete(s.subscribers, ch)
		s.broadcastMu.Unlock()
		close(ch)
	}()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	ctx := r.Context()
	keepAlive := time.NewTicker(30 * time.Second)
	defer keepAlive.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case sample := <-ch:
			data, err := json.Marshal(sample)
			if err != nil {
				s.logger.Error("marshal sample for stream failed", "error", err)
				continue
			}
			fmt.Fprintf(w, "event: sample\ndata: %s\n\n", data)
			flusher.Flush()
		case <-keepAlive.C:
			fmt.Fprint(w, ": keep-alive\n\n")
			flusher.Flush()
		}
	}
}

// parseRange extracts ?from=&to= unix-second query params, defaulting to
// the last hour when absent.
func parseRange(r *http.Request) (from, to time.Time, err error) {
	to = time.Now().UTC()
	from = to.Add(-1 * time.Hour)

	if v := r.URL.Query().Get("from"); v != "" {
		sec, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("invalid 'from': %w", err)
		}
		from = time.Unix(sec, 0).UTC()
	}
	if v := r.URL.Query().Get("to"); v != "" {
		sec, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("invalid 'to': %w", err)
		}
		to = time.Unix(sec, 0).UTC()
	}
	if from.After(to) {
		return time.Time{}, time.Time{}, fmt.Errorf("'from' must not be after 'to'")
	}
	return from, to, nil
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		slog.Default().Error("writeJSON encode failed", "error", err)
	}
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

// ListenAndServe is a convenience wrapper for cmd/gateshell-agent; callers
// needing graceful shutdown should build an *http.Server directly with
// this Server as its Handler instead.
func ListenAndServe(ctx context.Context, addr string, handler http.Handler) error {
	srv := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	case err := <-errCh:
		if err == http.ErrServerClosed {
			return nil
		}
		return err
	}
}
