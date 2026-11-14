// Package app defines the forgemirror command line and wires the service together.
package app

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/urfave/cli/v3"

	"go.bitfoo.de/forgemirror/internal/config"
	"go.bitfoo.de/forgemirror/internal/logging"
	"go.bitfoo.de/forgemirror/internal/version"
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

	return nil
}

func validate(_ context.Context, cmd *cli.Command) error {
	cfg, err := loadConfig(cmd)
	if err != nil {
		return err
	}

	_, err = fmt.Fprintf(cmd.Root().Writer, "configuration is valid (%d targets)\n", len(cfg.Targets))

	return err
}
