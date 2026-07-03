// Command gateshell-agent is the optional, self-hosted binary that runs ON
// a user's server: it collects local metrics + service health on an
// interval, stores them (embedded SQLite with tiered retention, when built
// with `-tags sqlite`), serves them to the GateShell mobile app over a
// token-authed REST + SSE API, and pushes threshold alerts via ntfy.
//
// It has NO web UI. Reachability (port-forwarding, reverse proxy, VPN,
// Tailscale, etc.) is entirely the operator's concern -- this binary just
// binds ListenAddr and serves.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/Anilkhanna/gateshell-go/internal/alerts"
	"github.com/Anilkhanna/gateshell-go/internal/api"
	"github.com/Anilkhanna/gateshell-go/internal/collector"
	"github.com/Anilkhanna/gateshell-go/internal/config"
	"github.com/Anilkhanna/gateshell-go/internal/pair"
)

// version is overridden at build time via:
//
//	go build -ldflags "-X main.version=v1.2.3"
//
// See .goreleaser.yaml for the release wiring.
var version = "dev"

func main() {
	if err := newRootCmd().Execute(); err != nil {
		os.Exit(1)
	}
}

func newRootCmd() *cobra.Command {
	var (
		configFile   string
		listenAddr   string
		dbPath       string
		pollInterval string
		pairingToken string
		ntfyTopic    string
		serverName   string
	)

	root := &cobra.Command{
		Use:   "gateshell-agent",
		Short: "GateShell Agent: optional self-hosted metrics + alerting sidecar",
		Long: `GateShell Agent is an OPTIONAL, self-hosted binary that runs on your own
server. It collects local metrics and service health on an interval,
stores them locally, and serves them to the GateShell mobile app over a
token-authenticated REST + streaming API. It pushes threshold alerts via
ntfy. It has no web UI, and network reachability is entirely up to you.`,
	}

	root.PersistentFlags().StringVar(&configFile, "config", "", "path to JSON config file (optional)")
	root.PersistentFlags().StringVar(&listenAddr, "listen-addr", "", "address to bind the API on (default \""+config.DefaultListenAddr+"\")")
	root.PersistentFlags().StringVar(&dbPath, "db-path", "", "path to the sqlite database file (default \""+config.DefaultDBPath+"\")")
	root.PersistentFlags().StringVar(&pollInterval, "poll-interval", "", "how often to collect metrics, e.g. \"15s\" (default 15s)")
	root.PersistentFlags().StringVar(&pairingToken, "token", "", "pairing token the mobile app must present (required for `serve`)")
	root.PersistentFlags().StringVar(&ntfyTopic, "ntfy-topic", "", "ntfy topic URL to publish threshold alerts to (optional)")
	root.PersistentFlags().StringVar(&serverName, "server-name", "", "human-friendly name for this host (default: hostname)")

	loadConfig := func() (config.Config, error) {
		flags := config.FlagOverrides{ConfigFile: configFile}
		if listenAddr != "" {
			flags.ListenAddr = &listenAddr
		}
		if dbPath != "" {
			flags.DBPath = &dbPath
		}
		if pollInterval != "" {
			d, err := config.ParsePollIntervalFlag(pollInterval)
			if err != nil {
				return config.Config{}, fmt.Errorf("invalid --poll-interval: %w", err)
			}
			flags.PollInterval = &d
		}
		if pairingToken != "" {
			flags.PairingToken = &pairingToken
		}
		if ntfyTopic != "" {
			flags.NtfyTopic = &ntfyTopic
		}
		if serverName != "" {
			flags.ServerName = &serverName
		}
		return config.Load(flags)
	}

	root.AddCommand(newServeCmd(loadConfig))
	root.AddCommand(newVersionCmd())
	root.AddCommand(newPairCmd())

	return root
}

// newServeCmd wires config -> store -> collector -> alerts -> api and runs
// until SIGINT/SIGTERM.
func newServeCmd(loadConfig func() (config.Config, error)) *cobra.Command {
	return &cobra.Command{
		Use:   "serve",
		Short: "Start the metrics collector and API server",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}
			if cfg.PairingToken == "" {
				return fmt.Errorf("no pairing token configured; pass --token, set GATESHELL_AGENT_PAIRING_TOKEN, " +
					"or run `gateshell-agent pair` to generate one")
			}

			logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
			logger.Info("starting gateshell-agent",
				"version", version, "listen_addr", cfg.ListenAddr, "server_name", cfg.ServerName)

			st, err := newStore(cfg, logger)
			if err != nil {
				return fmt.Errorf("initializing store: %w", err)
			}
			defer st.Close()

			coll := collector.New(cfg.PollInterval, logger)

			// persistInterval writes an app-driven poll-interval change
			// (PATCH /api/v1/config) back to the config file so it
			// survives a restart. If no config file is in use, the change
			// still applies at runtime but can't be persisted.
			persistInterval := func(d time.Duration) error {
				if cfg.FilePath == "" {
					logger.Warn("poll interval changed at runtime but no --config file is set; " +
						"the change will not survive a restart")
					return nil
				}
				return config.SavePollInterval(cfg.FilePath, d)
			}

			apiServer := api.NewServer(st, cfg.PairingToken, cfg.ServerName, coll, persistInterval, logger)

			var publisher alerts.Publisher
			if cfg.NtfyTopic != "" {
				publisher = alerts.NewNtfyPublisher(cfg.NtfyTopic)
			}
			evaluator := alerts.NewEvaluator(publisher, logger)
			// TODO: load persisted rules once alerts rule persistence exists
			// (see TODO in internal/alerts). For now, no rules are
			// configured by default -- operators must wire them up once
			// that surface exists.
			evaluator.SetRules(nil, nil)

			coll.AddSink(collector.SinkFunc(func(sample collector.Sample) {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if err := st.SaveSample(ctx, sample); err != nil {
					logger.Error("saving sample failed", "error", err)
				}
			}))
			coll.AddSink(collector.SinkFunc(apiServer.BroadcastSample))
			coll.AddSink(evaluator)

			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()

			errCh := make(chan error, 2)
			go func() {
				errCh <- coll.Run(ctx)
			}()
			go func() {
				errCh <- api.ListenAndServe(ctx, cfg.ListenAddr, apiServer)
			}()

			<-ctx.Done()
			logger.Info("shutting down")

			// Drain the two goroutines' exit errors (both should return
			// promptly now that ctx is canceled); context.Canceled from the
			// collector is expected, not an error worth surfacing.
			for i := 0; i < 2; i++ {
				if err := <-errCh; err != nil && err != context.Canceled {
					logger.Error("component exited with error", "error", err)
				}
			}
			return nil
		},
	}
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the gateshell-agent version",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprintln(cmd.OutOrStdout(), version)
			return nil
		},
	}
}

// newPairCmd generates a new pairing token for the operator to hand to the
// config file / systemd environment / `--token` flag. It does not persist
// anything itself -- see internal/pair's package doc for the v1 design.
func newPairCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "pair",
		Short: "Generate a new pairing token for the mobile app",
		RunE: func(cmd *cobra.Command, args []string) error {
			token, err := pair.GenerateToken()
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), token)
			return nil
		},
	}
}
