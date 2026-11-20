// Package app defines the forgemirror command line and wires the service together.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"slices"
	"time"

	"github.com/urfave/cli/v3"

	"go.bitfoo.de/forgemirror/internal/config"
	"go.bitfoo.de/forgemirror/internal/control"
	"go.bitfoo.de/forgemirror/internal/dispatch"
	"go.bitfoo.de/forgemirror/internal/filter"
	"go.bitfoo.de/forgemirror/internal/forge"
	"go.bitfoo.de/forgemirror/internal/forge/forgejo"
	"go.bitfoo.de/forgemirror/internal/forge/github"
	"go.bitfoo.de/forgemirror/internal/forge/gitlab"
	"go.bitfoo.de/forgemirror/internal/forge/httpclient"
	"go.bitfoo.de/forgemirror/internal/logging"
	"go.bitfoo.de/forgemirror/internal/repo"
	"go.bitfoo.de/forgemirror/internal/schedule"
	"go.bitfoo.de/forgemirror/internal/server"
	"go.bitfoo.de/forgemirror/internal/status"
	"go.bitfoo.de/forgemirror/internal/telemetry"
	"go.bitfoo.de/forgemirror/internal/ui"
	"go.bitfoo.de/forgemirror/internal/version"
	"go.bitfoo.de/forgemirror/internal/webhook"
)

const (
	flagConfig           = "config"
	flagListenAddress    = "listen-address"
	flagOpsListenAddress = "ops-listen-address"
	flagUIListenAddress  = "ui-listen-address"
	flagLogLevel         = "log-level"
	flagLogFormat        = "log-format"

	// apiTimeout bounds a single forge API request; a hanging API must not
	// block a mirror run until the git timeout.
	apiTimeout = 30 * time.Second
)

// flagOverrides maps CLI flags to the config keys they override.
func flagOverrides() map[string]string {
	return map[string]string{
		flagListenAddress:    "server.listen_address",
		flagOpsListenAddress: "server.ops_listen_address",
		flagUIListenAddress:  "ui.listen_address",
		flagLogLevel:         "log.level",
		flagLogFormat:        "log.format",
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
			&cli.StringFlag{Name: flagListenAddress, Usage: "override server.listen_address (webhook), e.g. :8081"},
			&cli.StringFlag{
				Name:  flagOpsListenAddress,
				Usage: "override server.ops_listen_address (health, metrics), e.g. :9090",
			},
			&cli.StringFlag{Name: flagUIListenAddress, Usage: "override ui.listen_address, e.g. :8080"},
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

	// The UI's log buffer must be attached before the first record, so the
	// startup lines are part of it.
	var logs *logging.Ring
	if cfg.UI.Enabled {
		logs = logging.NewRing(cfg.UI.LogEntries)
	}
	logger := logging.New(cmd.Root().ErrWriter, cfg.Log, logs)
	logger.InfoContext(ctx, "forgemirror starting",
		slog.String("version", cmd.Root().Version),
		slog.String("listen_address", cfg.Server.ListenAddress),
		slog.String("ops_listen_address", cfg.Server.OpsListenAddress),
		slog.Bool("ui", cfg.UI.Enabled),
		slog.Bool("auto_sync", cfg.Sync.Auto),
		slog.Int("targets", len(cfg.Targets)),
	)

	return run(ctx, cfg, logger, logs)
}

// run wires the service together and blocks until ctx is cancelled and the
// graceful shutdown has finished.
func run(ctx context.Context, cfg *config.Config, logger *slog.Logger, logs *logging.Ring) error {
	if err := checkLFS(ctx, cfg); err != nil {
		return err
	}
	flt, err := filter.New(cfg.Filter, cfg.PrivatePolicy())
	if err != nil {
		return err
	}

	clients, err := newForgeClients(cfg, httpclient.New(apiTimeout))
	if err != nil {
		return err
	}
	logIgnoredSettings(ctx, cfg, logger, clients)

	metrics, err := telemetry.New(version.Get().Version)
	if err != nil {
		return err
	}

	// ctx is already cancelled here; WithoutCancel keeps its values only.
	return errors.Join(
		serveAndDrain(ctx, cfg, logger, logs, flt, metrics, clients),
		metrics.Shutdown(context.WithoutCancel(ctx)),
	)
}

// serveAndDrain serves HTTP until ctx is cancelled, then drains HTTP requests
// and running mirrors within the shutdown timeout.
func serveAndDrain(
	ctx context.Context,
	cfg *config.Config,
	logger *slog.Logger,
	logs *logging.Ring,
	flt *filter.Filter,
	metrics *telemetry.Metrics,
	clients forgeClients,
) (resultErr error) {
	store, closeStore, err := openStatus(ctx, cfg, logger)
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, closeStore()) }()
	dispatchObs := dispatch.Observers{metrics}
	scheduleObs := schedule.Observers{metrics}
	if store != nil {
		dispatchObs = append(dispatchObs, store)
		scheduleObs = append(scheduleObs, store)
	}

	dispatcher := newDispatcher(cfg, logger, clients, store, dispatchObs)

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

	health := &server.Health{}
	listeners := []listener{
		{name: "webhook", address: cfg.Server.ListenAddress, handler: server.WebhookHandler(webhookHandler)},
		{name: "ops", address: cfg.Server.OpsListenAddress, handler: server.OpsHandler(health, metrics.Handler())},
	}
	actions := newController(cfg, logger, store, flt, clients, dispatcher)
	if cfg.UI.Enabled {
		web, _, webErr := newWeb(cfg, logger, logs, store, flt, clients, dispatcher)
		if webErr != nil {
			return webErr
		}
		listeners = append(listeners, web)
	}
	bound, err := listen(ctx, listeners)
	if err != nil {
		return err
	}

	// runCtx also ends when a server fails on its own, so the shutdown
	// deadline starts in that case too.
	runCtx, stopRun := context.WithCancel(ctx)
	defer stopRun()
	shutdownCtx, cancelShutdown := shutdownContext(runCtx, cfg.Server.ShutdownTimeout)
	defer cancelShutdown()

	dispatcher.StartRetries(runCtx)
	schedulerDone := startScheduler(runCtx, cfg, logger, flt, scheduleObs, clients.source, dispatcher)
	refreshDone := startReviewRefresh(runCtx, cfg, logger, actions)

	// Readiness ends as soon as shutdown begins, before the listeners close,
	// so Kubernetes stops routing new webhooks to this pod.
	stopReady := context.AfterFunc(runCtx, func() { health.SetReady(false) })
	defer stopReady()
	health.SetReady(true)

	serveErr := serveAll(runCtx, shutdownCtx, stopRun, cfg, logger, bound)
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
	select {
	case <-refreshDone:
	case <-shutdownCtx.Done():
		logger.WarnContext(ctx, "startup refresh did not stop before the shutdown deadline")
	}

	logger.InfoContext(ctx, "waiting for running mirrors")
	dispatchErr := dispatcher.Shutdown(shutdownCtx)
	logger.InfoContext(ctx, "forgemirror stopped")

	return errors.Join(serveErr, dispatchErr)
}

// listener is one HTTP listener to open.
type listener struct {
	name    string
	address string
	handler http.Handler
	ln      net.Listener
}

// listen binds every listener before any of them serves, so a taken port
// fails startup instead of leaving a half-started service.
func listen(ctx context.Context, listeners []listener) ([]listener, error) {
	lc := new(net.ListenConfig)
	for i := range listeners {
		ln, err := lc.Listen(ctx, "tcp", listeners[i].address)
		if err != nil {
			for _, opened := range listeners[:i] {
				_ = opened.ln.Close()
			}

			return nil, fmt.Errorf("listening on %s (%s): %w", listeners[i].address, listeners[i].name, err)
		}
		listeners[i].ln = ln
	}

	return listeners, nil
}

// serveAll serves every listener until ctx ends; one failing server stops
// all of them through stop.
func serveAll(
	ctx, shutdownCtx context.Context,
	stop context.CancelFunc,
	cfg *config.Config,
	logger *slog.Logger,
	listeners []listener,
) error {
	errs := make(chan error, len(listeners))
	for _, l := range listeners {
		srv := server.New(server.Options{
			Name:              l.name,
			Handler:           l.handler,
			ReadHeaderTimeout: cfg.Server.ReadHeaderTimeout,
			Logger:            logger,
		})
		go func() {
			err := srv.Serve(ctx, shutdownCtx, l.ln)
			if err != nil {
				stop()
			}
			errs <- err
		}()
	}

	var all []error
	for range listeners {
		all = append(all, <-errs)
	}

	return errors.Join(all...)
}

// startScheduler runs scheduled syncs until ctx ends and returns a channel
// closed when they have stopped; without a schedule it is closed at once.
func startScheduler(
	ctx context.Context,
	cfg *config.Config,
	logger *slog.Logger,
	flt *filter.Filter,
	observer schedule.Observer,
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
			Observer:   observer,
		})
	}()

	return done
}

// newWeb builds the UI listener and the controller behind its actions.
func newWeb(
	cfg *config.Config,
	logger *slog.Logger,
	logs *logging.Ring,
	store *status.Store,
	flt *filter.Filter,
	clients forgeClients,
	dispatcher *dispatch.Dispatcher,
) (listener, *control.Controller, error) {
	actions := newController(cfg, logger, store, flt, clients, dispatcher)
	web, err := ui.Handler(ui.Options{
		Config:  cfg,
		Store:   store,
		Logs:    logs,
		Queued:  dispatcher.Queued,
		Known:   func(name string) bool { _, ok := dispatcher.Known(name); return ok },
		Actions: actions,
		Version: version.Get().String(),
		Logger:  logger,
	})
	if err != nil {
		return listener{}, nil, err
	}

	return listener{name: "web", address: cfg.UI.ListenAddress, handler: web}, actions, nil
}

// startReviewRefresh plans every repository once at startup in review mode,
// so the UI shows diffs after a restart without a click. With a schedule the
// first sweep does that already. The returned channel is closed when done.
func startReviewRefresh(
	ctx context.Context,
	cfg *config.Config,
	logger *slog.Logger,
	actions *control.Controller,
) <-chan struct{} {
	done := make(chan struct{})
	if actions == nil || !cfg.UsesSourceAPI() {
		close(done)

		return done
	}

	go func() {
		defer close(done)
		if _, err := actions.RefreshAll(ctx); err != nil && ctx.Err() == nil {
			logger.ErrorContext(ctx, "startup refresh failed; use Refresh all in the UI", slog.Any("error", err))
		}
	}()

	return done
}

// forgeClients are the API clients shared by all runs.
type forgeClients struct {
	// source is nil unless [config.Config.UsesSourceAPI].
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

	if cfg.UsesSourceAPI() {
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

// logIgnoredSettings reports once at startup a has_pull_requests the target
// forge cannot store (GitHub cannot disable pull requests). The settings sync
// skips it silently on every run, so without this line the key would look
// like it has an effect.
func logIgnoredSettings(ctx context.Context, cfg *config.Config, logger *slog.Logger, clients forgeClients) {
	for i := range cfg.Targets {
		t := &cfg.Targets[i]
		client, ok := clients.targets[t.Name]
		if !ok || client.Capabilities().PullRequests || !setsHasPullRequests(t) {
			continue
		}
		logger.InfoContext(ctx, "has_pull_requests is ignored: the target forge cannot disable pull requests",
			slog.String("target", t.Name), slog.String("type", t.Type))
	}
}

func setsHasPullRequests(t *config.Target) bool {
	if t.Settings.HasPullRequests != nil {
		return true
	}

	return slices.ContainsFunc(t.Overrides, func(o config.Override) bool {
		return o.Settings.HasPullRequests != nil
	})
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

	targets := "targets"
	if len(cfg.Targets) == 1 {
		targets = "target"
	}
	_, err = fmt.Fprintf(cmd.Root().Writer, "configuration is valid (%d %s)\n", len(cfg.Targets), targets)

	return err
}
