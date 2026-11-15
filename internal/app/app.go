// Package app defines the forgemirror command line and wires the service together.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/urfave/cli/v3"

	"go.bitfoo.de/forgemirror/internal/config"
	"go.bitfoo.de/forgemirror/internal/dispatch"
	"go.bitfoo.de/forgemirror/internal/filter"
	"go.bitfoo.de/forgemirror/internal/forge"
	"go.bitfoo.de/forgemirror/internal/forge/forgejo"
	"go.bitfoo.de/forgemirror/internal/forge/github"
	"go.bitfoo.de/forgemirror/internal/forge/gitlab"
	"go.bitfoo.de/forgemirror/internal/forge/httpclient"
	"go.bitfoo.de/forgemirror/internal/logging"
	"go.bitfoo.de/forgemirror/internal/mirror"
	"go.bitfoo.de/forgemirror/internal/repo"
	"go.bitfoo.de/forgemirror/internal/schedule"
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

	// apiTimeout bounds a single forge API request; a hanging API must not
	// block a mirror run until the git timeout.
	apiTimeout = 30 * time.Second
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

	clients, err := newForgeClients(cfg, httpclient.New(apiTimeout))
	if err != nil {
		return err
	}

	metrics, err := telemetry.New(version.Get().Version)
	if err != nil {
		return err
	}

	// ctx is already cancelled here; WithoutCancel keeps its values only.
	return errors.Join(
		serveAndDrain(ctx, cfg, logger, flt, metrics, clients),
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
	clients forgeClients,
) error {
	dispatcher := dispatch.New(dispatch.Options{
		Mirrorer: mirror.NewGit(mirror.GitOptions{
			Binary:  cfg.Mirror.GitBinary,
			WorkDir: cfg.Mirror.WorkDir,
			Timeout: cfg.Mirror.Timeout,
		}),
		Source:        cfg.Source,
		Targets:       cfg.Targets,
		ForgeSource:   clients.source,
		Forges:        clients.targets,
		MaxConcurrent: cfg.Mirror.MaxConcurrent,
		Logger:        logger,
		Observer:      metrics,
	})

	// Without a secret the endpoint must not exist: an HMAC over an empty key
	// is forgeable by anyone. Config validation allows this only when nothing
	// needs webhooks (on_push false, no delete targets).
	var webhookHandler http.Handler
	if cfg.Webhook.Secret.Reveal() != "" {
		webhookHandler = webhook.NewHandler(webhook.Options{
			Secret:       []byte(cfg.Webhook.Secret.Reveal()),
			MaxBodyBytes: cfg.Webhook.MaxBodyBytes,
			IgnorePush:   !cfg.Sync.OnPush,
			Filter:       flt,
			Source:       cfg.Source,
			Dispatcher:   dispatcher,
			Logger:       logger,
			Observer:     metrics,
		})
	}

	srv := server.New(server.Options{
		ReadHeaderTimeout: cfg.Server.ReadHeaderTimeout,
		Webhook:           webhookHandler,
		Metrics:           metrics.Handler(),
		Logger:            logger,
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

	schedulerDone := startScheduler(runCtx, cfg, logger, flt, metrics, clients.source, dispatcher)

	serveErr := srv.Serve(runCtx, shutdownCtx, ln)
	stopRun()

	// The scheduler must be gone before the dispatcher drains, or a sweep
	// still dispatching would only produce dropped-event warnings. Every
	// step honours runCtx, so this is quick; the deadline guards shutdown
	// against a scheduler stuck anyway.
	select {
	case <-schedulerDone:
	case <-shutdownCtx.Done():
		logger.WarnContext(ctx, "scheduler did not stop before the shutdown deadline")
	}

	logger.InfoContext(ctx, "waiting for running mirrors")
	dispatchErr := dispatcher.Shutdown(shutdownCtx)
	logger.InfoContext(ctx, "forgemirror stopped")

	return errors.Join(serveErr, dispatchErr)
}

// startScheduler runs scheduled syncs until ctx ends and returns a channel
// closed when they have stopped; without a schedule it is closed at once.
func startScheduler(
	ctx context.Context,
	cfg *config.Config,
	logger *slog.Logger,
	flt *filter.Filter,
	metrics *telemetry.Metrics,
	source forge.Source,
	dispatcher webhook.Dispatcher,
) <-chan struct{} {
	done := make(chan struct{})
	if cfg.Sync.Schedule <= 0 {
		close(done)

		return done
	}

	logger.InfoContext(ctx, "scheduled sync enabled", slog.Duration("interval", cfg.Sync.Schedule))
	go func() {
		defer close(done)
		schedule.Run(ctx, schedule.Options{
			Interval:   cfg.Sync.Schedule,
			Source:     source,
			Filter:     flt,
			Dispatcher: dispatcher,
			Logger:     logger,
			Observer:   metrics,
		})
	}()

	return done
}

// forgeClients are the API clients shared by all runs.
type forgeClients struct {
	// source is nil unless a target has a type or a schedule is set.
	source  forge.Source
	targets map[string]forge.Target
}

// newForgeClients builds every API client once at startup without network
// access: the GitHub and GitLab constructors only configure, and Forgejo
// clients connect lazily, because a startup that needs Forgejo would
// crash-loop the pod while Forgejo is down. Configuration mistakes (bad URL
// template, empty token) still fail startup.
func newForgeClients(cfg *config.Config, hc *http.Client) (forgeClients, error) {
	clients := forgeClients{targets: make(map[string]forge.Target)}

	if cfg.Typed() || cfg.Sync.Schedule > 0 {
		clients.source = forgejo.NewLazy(hc, cfg.Source.BaseURL, cfg.Source.Token).Source()
	}

	for i := range cfg.Targets {
		t := &cfg.Targets[i]
		if t.Type == "" {
			continue
		}

		target, err := newTargetClient(t, hc)
		if err != nil {
			return forgeClients{}, fmt.Errorf("building API client of target %q: %w", t.Name, err)
		}
		clients.targets[t.Name] = target
	}

	return clients, nil
}

// newTargetClient builds the client from the address of a sample repository.
// The API URL is derived from the host of the target URL template, and every
// real template keeps the host constant (only the path varies per
// repository), so one client per target serves all repositories.
func newTargetClient(t *config.Target, hc *http.Client) (forge.Target, error) {
	addr, err := t.Address(repo.Repository{Owner: "owner", Name: "name", FullName: "owner/name"})
	if err != nil {
		return nil, err
	}

	switch t.Type {
	case forge.TypeGitHub:
		return github.New(hc, addr.APIURL, t.Token)
	case forge.TypeGitLab:
		return gitlab.New(hc, addr.APIURL, t.Token)
	case forge.TypeForgejo:
		return forgejo.NewLazy(hc, addr.APIURL, t.Token), nil
	default:
		return nil, fmt.Errorf("unknown forge type %q", t.Type)
	}
}

func validate(_ context.Context, cmd *cli.Command) error {
	cfg, err := loadConfig(cmd)
	if err != nil {
		return err
	}

	_, err = fmt.Fprintf(cmd.Root().Writer, "configuration is valid (%d targets)\n", len(cfg.Targets))

	return err
}
