// Package config loads GateShell Agent configuration from flags, environment
// variables, and an optional config file, in that order of precedence
// (flags > env > file > defaults).
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Default values used when a setting is not supplied by any other source.
const (
	DefaultListenAddr   = "127.0.0.1:8443"
	DefaultDBPath       = "gateshell-agent.db"
	DefaultPollInterval = 5 * time.Minute
	DefaultServerName   = "gateshell-agent"
)

// Config holds all runtime configuration for the agent.
//
// Precedence (highest to lowest): CLI flags > environment variables >
// config file (JSON or TOML) > built-in defaults.
type Config struct {
	// ListenAddr is the host:port the REST/WebSocket API binds to.
	// Reachability (port-forwarding, firewalls, reverse proxies) is left
	// entirely to the operator -- the agent does not attempt NAT traversal,
	// UPnP, or tunneling of any kind.
	ListenAddr string `json:"listen_addr" toml:"listen_addr"`

	// DBPath is the filesystem path to the embedded SQLite database file
	// used for metric history. Ignored by the in-memory store build.
	DBPath string `json:"db_path" toml:"db_path"`

	// PollInterval is how often the collector gathers a new Sample.
	PollInterval time.Duration `json:"poll_interval" toml:"poll_interval"`

	// PairingToken is the bearer token the mobile app must present to
	// authenticate against the REST/WebSocket API. Generated once during
	// `pair` / install and persisted by the operator (e.g. in the systemd
	// unit's environment file). See internal/pair for generation helpers.
	PairingToken string `json:"pairing_token" toml:"pairing_token"`

	// NtfyTopic is the https://ntfy.sh (or self-hosted ntfy) topic URL or
	// name that threshold alerts are published to. Empty disables alerting.
	NtfyTopic string `json:"ntfy_topic" toml:"ntfy_topic"`

	// ServerName is a human-friendly label for this host, included in API
	// responses and alert payloads so a multi-server user can tell agents
	// apart.
	ServerName string `json:"server_name" toml:"server_name"`

	// FilePath is the resolved path of the config file this Config was
	// loaded from (empty if none was supplied). It is not itself a
	// persisted setting -- it lets runtime consumers (e.g. the config API
	// persisting an app-driven poll-interval change) know which file to
	// write back to. Never marshaled.
	FilePath string `json:"-" toml:"-"`
}

// Defaults returns a Config populated with built-in defaults.
func Defaults() Config {
	hostname, err := os.Hostname()
	if err != nil || hostname == "" {
		hostname = DefaultServerName
	}
	return Config{
		ListenAddr:   DefaultListenAddr,
		DBPath:       DefaultDBPath,
		PollInterval: DefaultPollInterval,
		PairingToken: "",
		NtfyTopic:    "",
		ServerName:   hostname,
	}
}

// FlagOverrides carries values parsed from CLI flags. A pointer field left
// nil means "flag not set" and should not override lower-precedence sources.
// cmd/gateshell-agent wires cobra flags into this struct.
type FlagOverrides struct {
	ConfigFile   string
	ListenAddr   *string
	DBPath       *string
	PollInterval *time.Duration
	PairingToken *string
	NtfyTopic    *string
	ServerName   *string
}

// Load builds the final Config by layering, from lowest to highest
// precedence: built-in defaults, an optional config file, environment
// variables, then CLI flag overrides.
//
// Supported env vars: GATESHELL_AGENT_LISTEN_ADDR, GATESHELL_AGENT_DB_PATH,
// GATESHELL_AGENT_POLL_INTERVAL, GATESHELL_AGENT_PAIRING_TOKEN,
// GATESHELL_AGENT_NTFY_TOPIC, GATESHELL_AGENT_SERVER_NAME.
func Load(flags FlagOverrides) (Config, error) {
	cfg := Defaults()

	if flags.ConfigFile != "" {
		fileCfg, err := loadFile(flags.ConfigFile)
		if err != nil {
			return Config{}, fmt.Errorf("config: loading file %q: %w", flags.ConfigFile, err)
		}
		cfg = mergeNonZero(cfg, fileCfg)
	}

	cfg = applyEnv(cfg)
	cfg = applyFlags(cfg, flags)
	cfg.FilePath = flags.ConfigFile

	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// SavePollInterval persists the poll interval to the JSON config file at
// path, preserving any other keys already present in the file. It is used
// by the runtime config API (PATCH /api/v1/config) so an app-driven
// interval change survives a restart.
//
// Note on precedence: this writes to the config *file* only. A
// --poll-interval flag or GATESHELL_AGENT_POLL_INTERVAL env var still wins
// over the file on the next start (see Load's precedence). Deployments that
// want app-driven changes to stick should configure the interval via the
// file (as install.sh does), not via a flag/env override.
func SavePollInterval(path string, d time.Duration) error {
	if path == "" {
		return errors.New("config: no config file path configured; cannot persist poll interval")
	}
	if ext := strings.ToLower(filepath.Ext(path)); ext != ".json" && ext != "" {
		// loadFile only decodes JSON today; refuse to write a JSON body
		// into a differently-typed file rather than corrupt it.
		return fmt.Errorf("config: cannot persist to non-JSON config file %q", path)
	}

	// Load existing contents into a generic map so unrelated keys (e.g.
	// listen_addr, ntfy_topic) are preserved verbatim.
	raw := map[string]any{}
	if data, err := os.ReadFile(path); err == nil {
		if len(data) > 0 {
			if err := json.Unmarshal(data, &raw); err != nil {
				return fmt.Errorf("config: parsing existing file %q: %w", path, err)
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("config: reading file %q: %w", path, err)
	}

	raw["poll_interval"] = d.String()

	out, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return fmt.Errorf("config: marshaling config: %w", err)
	}

	// Write atomically (temp file + rename) so a crash mid-write can't
	// leave a truncated config behind.
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(out, '\n'), 0o600); err != nil {
		return fmt.Errorf("config: writing temp file: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("config: renaming temp file into place: %w", err)
	}
	return nil
}

// loadFile reads a JSON or TOML config file based on its extension.
//
// TODO: wire in a TOML decoder (e.g. github.com/BurntSushi/toml) once a
// dependency budget is agreed for the release build. JSON is fully
// supported today; ".toml" currently returns an error so callers get a
// clear signal instead of silently ignoring the file.
func loadFile(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}

	var raw struct {
		ListenAddr   string `json:"listen_addr"`
		DBPath       string `json:"db_path"`
		PollInterval string `json:"poll_interval"`
		PairingToken string `json:"pairing_token"`
		NtfyTopic    string `json:"ntfy_topic"`
		ServerName   string `json:"server_name"`
	}

	switch ext := strings.ToLower(filepath.Ext(path)); ext {
	case ".json", "":
		if err := json.Unmarshal(data, &raw); err != nil {
			return Config{}, fmt.Errorf("parsing JSON: %w", err)
		}
	case ".toml":
		// TODO(toml): decode TOML here. See doc comment above.
		return Config{}, errors.New("TOML config files are not yet supported; use JSON")
	default:
		return Config{}, fmt.Errorf("unrecognized config file extension %q", ext)
	}

	var cfg Config
	cfg.ListenAddr = raw.ListenAddr
	cfg.DBPath = raw.DBPath
	cfg.PairingToken = raw.PairingToken
	cfg.NtfyTopic = raw.NtfyTopic
	cfg.ServerName = raw.ServerName
	if raw.PollInterval != "" {
		d, err := time.ParseDuration(raw.PollInterval)
		if err != nil {
			return Config{}, fmt.Errorf("parsing poll_interval: %w", err)
		}
		cfg.PollInterval = d
	}
	return cfg, nil
}

// mergeNonZero overlays non-zero-valued fields from override onto base.
func mergeNonZero(base, override Config) Config {
	if override.ListenAddr != "" {
		base.ListenAddr = override.ListenAddr
	}
	if override.DBPath != "" {
		base.DBPath = override.DBPath
	}
	if override.PollInterval != 0 {
		base.PollInterval = override.PollInterval
	}
	if override.PairingToken != "" {
		base.PairingToken = override.PairingToken
	}
	if override.NtfyTopic != "" {
		base.NtfyTopic = override.NtfyTopic
	}
	if override.ServerName != "" {
		base.ServerName = override.ServerName
	}
	return base
}

func applyEnv(cfg Config) Config {
	if v := os.Getenv("GATESHELL_AGENT_LISTEN_ADDR"); v != "" {
		cfg.ListenAddr = v
	}
	if v := os.Getenv("GATESHELL_AGENT_DB_PATH"); v != "" {
		cfg.DBPath = v
	}
	if v := os.Getenv("GATESHELL_AGENT_POLL_INTERVAL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			cfg.PollInterval = d
		}
	}
	if v := os.Getenv("GATESHELL_AGENT_PAIRING_TOKEN"); v != "" {
		cfg.PairingToken = v
	}
	if v := os.Getenv("GATESHELL_AGENT_NTFY_TOPIC"); v != "" {
		cfg.NtfyTopic = v
	}
	if v := os.Getenv("GATESHELL_AGENT_SERVER_NAME"); v != "" {
		cfg.ServerName = v
	}
	return cfg
}

func applyFlags(cfg Config, flags FlagOverrides) Config {
	if flags.ListenAddr != nil && *flags.ListenAddr != "" {
		cfg.ListenAddr = *flags.ListenAddr
	}
	if flags.DBPath != nil && *flags.DBPath != "" {
		cfg.DBPath = *flags.DBPath
	}
	if flags.PollInterval != nil && *flags.PollInterval != 0 {
		cfg.PollInterval = *flags.PollInterval
	}
	if flags.PairingToken != nil && *flags.PairingToken != "" {
		cfg.PairingToken = *flags.PairingToken
	}
	if flags.NtfyTopic != nil && *flags.NtfyTopic != "" {
		cfg.NtfyTopic = *flags.NtfyTopic
	}
	if flags.ServerName != nil && *flags.ServerName != "" {
		cfg.ServerName = *flags.ServerName
	}
	return cfg
}

// Validate performs basic sanity checks on the config. It intentionally does
// NOT require a PairingToken -- `gateshell-agent pair` may be used to
// generate one interactively after first boot -- but `serve` should refuse
// to start without one (see cmd/gateshell-agent).
func (c Config) Validate() error {
	if c.ListenAddr == "" {
		return errors.New("config: listen address must not be empty")
	}
	if c.PollInterval <= 0 {
		return errors.New("config: poll interval must be positive")
	}
	if c.DBPath == "" {
		return errors.New("config: db path must not be empty")
	}
	return nil
}

// ParsePollIntervalFlag is a small helper for CLI wiring that needs to parse
// a duration-like flag value (e.g. "15s", "1m") with a friendlier error.
func ParsePollIntervalFlag(s string) (time.Duration, error) {
	if s == "" {
		return 0, nil
	}
	// Allow bare integers to mean seconds, matching common CLI conventions.
	if n, err := strconv.Atoi(s); err == nil {
		return time.Duration(n) * time.Second, nil
	}
	return time.ParseDuration(s)
}
