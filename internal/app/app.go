// Package app defines the forgemirror command line and wires the service together.
package app

import (
	"context"
	"log/slog"

	"github.com/urfave/cli/v3"

	"go.bitfoo.de/forgemirror/internal/version"
)

// NewCommand returns the forgemirror root command.
func NewCommand() *cli.Command {
	return &cli.Command{
		Name:    "forgemirror",
		Usage:   "mirror Forgejo repositories to other forges on push",
		Version: version.Get().String(),
		Action:  serve,
	}
}

func serve(ctx context.Context, cmd *cli.Command) error {
	logger := slog.New(slog.NewTextHandler(cmd.ErrWriter, nil))
	logger.InfoContext(ctx, "forgemirror starting", slog.String("version", cmd.Version))

	return nil
}
