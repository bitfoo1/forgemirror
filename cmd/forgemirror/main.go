// Command forgemirror mirrors Forgejo repositories to other forges on push.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"go.bitfoo.de/forgemirror/internal/app"
)

func main() {
	os.Exit(run())
}

// run exists so deferred calls execute before [os.Exit], which skips them.
func run() int {
	// SIGTERM is what Kubernetes sends on pod shutdown; cancelling the context
	// starts the graceful shutdown.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := app.NewCommand().Run(ctx, os.Args); err != nil {
		fmt.Fprintln(os.Stderr, "forgemirror:", err)
		return 1
	}

	return 0
}
