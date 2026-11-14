package app

import (
	"context"
	"time"
)

// shutdownContext returns a context that ends timeout after ctx is done.
//
// Graceful shutdown has two phases (drain HTTP requests, then running
// mirrors) that must share one budget: Kubernetes kills the pod after its
// grace period no matter how the time was split. A plain WithTimeout cannot
// express "deadline starts when ctx ends", hence AfterFunc arming a timer.
func shutdownContext(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	shutdownCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	stop := context.AfterFunc(ctx, func() {
		// If the returned CancelFunc runs first, the timer later calls an
		// already-called cancel, which is a no-op.
		time.AfterFunc(timeout, cancel)
	})

	return shutdownCtx, func() {
		stop()
		cancel()
	}
}
