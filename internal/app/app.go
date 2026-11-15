// Package app defines the forgemirror command line and wires the service together.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"

	"github.com/urfave/cli/v3"

	"go.bitfoo.de/forgemirror/internal/config"
	"go.bitfoo.de/forgemirror/internal/dispatch"
	"go.bitfoo.de/forgemirror/internal/filter"
	"go.bitfoo.de/forgemirror/internal/logging"
	"go.bitfoo.de/forgemirror/internal/mirror"
	"go.bitfoo.de/forgemirror/internal/server"
	"go.bitfoo.de/forgemirror/internal/telemetry"
	"go.bitfoo.de/forgemirror/internal/version"
	"go.bitfoo.de/forgemirror/internal/webhook"
)

const (
	flagConfig        = "config"
	flagListenAddress = "listen-address"
	flagLogLevel      = "log-level"
	flagLogFormat     = "log-format"
)

// flagOverrides maps CLI flags to the config keys they override.
func flagOverrides() map[string]string {
	return map[string]string{
		flagListenAddress: "server.listen_address",
		flagLogLevel:      "log.level",
		flagLogFormat:     "log.format",
	}
}

// NewCommand returns the forgemirror root command.
func NewCommand() *cli.Command {
	return &cli.Command{
		Name:    "forgemirror",
		Usage:   "mirror Forgejo repositories to other forges on push",
		Version: version.Get().String(),
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:      flagConfig,
				Aliases:   []string{"c"},
				Usage:     "config file (default: first of " + fmt.Sprint(config.DefaultFiles()) + " that exists)",
				Sources:   cli.EnvVars(config.EnvConfigFile),
				TakesFile: true,
			},
			&cli.StringFlag{Name: flagListenAddress, Usage: "override server.listen_address, e.g. :8080"},
			&cli.StringFlag{Name: flagLogLevel, Usage: "override log.level (debug, info, warn, error)"},
			&cli.StringFlag{Name: flagLogFormat, Usage: "override log.format (json, text)"},
		},
		Action: serve,
		Commands: []*cli.Command{
			{
				Name:   "validate",
				Usage:  "load and validate the configuration, then exit",
				Action: validate,
			},
		},
	}
}

func loadConfig(cmd *cli.Command) (*config.Config, error) {
	path := cmd.String(flagConfig)
	if path == "" {
		found, err := config.FindFile(config.DefaultFiles())
		if err != nil {
			return nil, err
		}
		path = found
	}

	overrides := make(map[string]any)
	for flag, key := range flagOverrides() {
		if cmd.IsSet(flag) {
			overrides[key] = cmd.String(flag)
		}
	}

	return config.Load(config.Options{File: path, Overrides: overrides})
}

func serve(ctx context.Context, cmd *cli.Command) error {
	cfg, err := loadConfig(cmd)
	if err != nil {
		return err
	}

	logger := logging.New(cmd.Root().ErrWriter, cfg.Log)
	logger.InfoContext(ctx, "forgemirror starting",
		slog.String("version", cmd.Root().Version),
		slog.String("listen_address", cfg.Server.ListenAddress),
		slog.Int("targets", len(cfg.Targets)),
	)

	return run(ctx, cfg, logger)
}

// run wires the service together and blocks until ctx is cancelled and the
// graceful shutdown has finished.
func run(ctx context.Context, cfg *config.Config, logger *slog.Logger) error {
	flt, err := filter.New(cfg.Filter)
	if err != nil {
		return err
	}

	metrics, err := telemetry.New(version.Get().Version)
	if err != nil {
		return err
	}

	// ctx is already cancelled here; WithoutCancel keeps its values only.
	return errors.Join(
		serveAndDrain(ctx, cfg, logger, flt, metrics),
		metrics.Shutdown(context.WithoutCancel(ctx)),
	)
}

// serveAndDrain serves HTTP until ctx is cancelled, then drains HTTP requests
// and running mirrors within the shutdown timeout.
func serveAndDrain(
	ctx context.Context,
	cfg *config.Config,
	logger *slog.Logger,
	flt *filter.Filter,
	metrics *telemetry.Metrics,
) error {
	dispatcher := dispatch.New(dispatch.Options{
		Mirrorer: mirror.NewGit(mirror.GitOptions{
			Binary:  cfg.Mirror.GitBinary,
			WorkDir: cfg.Mirror.WorkDir,
			Timeout: cfg.Mirror.Timeout,
		}),
		Source:        cfg.Source,
		Targets:       cfg.Targets,
		MaxConcurrent: cfg.Mirror.MaxConcurrent,
		Logger:        logger,
		Observer:      metrics,
	})

	srv := server.New(server.Options{
		ReadHeaderTimeout: cfg.Server.ReadHeaderTimeout,
		Webhook: webhook.NewHandler(webhook.Options{
			Secret:       []byte(cfg.Webhook.Secret.Reveal()),
			MaxBodyBytes: cfg.Webhook.MaxBodyBytes,
			Filter:       flt,
			Source:       cfg.Source,
			Dispatcher:   dispatcher,
			Logger:       logger,
			Observer:     metrics,
		}),
		Metrics: metrics.Handler(),
		Logger:  logger,
	})

	ln, err := new(net.ListenConfig).Listen(ctx, "tcp", cfg.Server.ListenAddress)
	if err != nil {
		return fmt.Errorf("listening on %s: %w", cfg.Server.ListenAddress, err)
	}

	// runCtx also ends when the server fails on its own, so the shutdown
	// deadline starts in that case too.
	runCtx, stopRun := context.WithCancel(ctx)
	defer stopRun()
	shutdownCtx, cancelShutdown := shutdownContext(runCtx, cfg.Server.ShutdownTimeout)
	defer cancelShutdown()

	serveErr := srv.Serve(runCtx, shutdownCtx, ln)
	stopRun()

	logger.InfoContext(ctx, "waiting for running mirrors")
	dispatchErr := dispatcher.Shutdown(shutdownCtx)
	logger.InfoContext(ctx, "forgemirror stopped")

	return errors.Join(serveErr, dispatchErr)
}

func validate(_ context.Context, cmd *cli.Command) error {
	cfg, err := loadConfig(cmd)
	if err != nil {
		return err
	}

	_, err = fmt.Fprintf(cmd.Root().Writer, "configuration is valid (%d targets)\n", len(cfg.Targets))

	return err
}
