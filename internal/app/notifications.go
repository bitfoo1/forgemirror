package app

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"go.bitfoo.de/forgemirror/internal/config"
	"go.bitfoo.de/forgemirror/internal/forge/httpclient"
	"go.bitfoo.de/forgemirror/internal/notify"
	"go.bitfoo.de/forgemirror/internal/state"
	"go.bitfoo.de/forgemirror/internal/status"
)

func newNotifications(
	ctx context.Context,
	cfg *config.Config,
	db *state.DB,
	store *status.Store,
	logger *slog.Logger,
) (*notify.Outbox, func(context.Context, string, string, error)) {
	if cfg.Notifications.WebhookURL == "" {
		return nil, nil
	}
	fingerprint := sha256.Sum256([]byte(cfg.Notifications.WebhookURL.Reveal()))
	box, err := notify.New(
		ctx,
		notify.Options{
			URL:         cfg.Notifications.WebhookURL.Reveal(),
			Token:       cfg.Notifications.Token.Reveal(),
			UIURL:       cfg.Notifications.UIURL,
			Client:      httpclient.New(cfg.Notifications.Timeout),
			Persistence: db.Namespace(fmt.Sprintf("notifications/%x", fingerprint)),
			Now:         time.Now,
		},
	)
	if err != nil {
		logger.ErrorContext(ctx, "notification outbox unavailable; outbound delivery disabled")
		return nil, nil
	}
	outcome := func(ctx context.Context, repo, target string, result error) {
		if errors.Is(result, context.Canceled) {
			return
		}
		event := notify.Event{Repo: repo, Target: target, State: "healthy"}
		if result != nil {
			event.State = "needs intervention"
			for _, entry := range store.Retries() {
				if entry.Repo == repo && entry.Target == target {
					event.State = entry.State
					break
				}
			}
			if reason, ok := errors.AsType[interface {
				error
				ReasonCode() string
			}](result); ok {
				event.Reason = reason.ReasonCode()
				event.State = "blocked"
			}
		}
		const saveTimeout = 5 * time.Second
		saveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), saveTimeout)
		defer cancel()
		if saveErr := box.Record(saveCtx, event); saveErr != nil {
			logger.ErrorContext(ctx, "notification could not be queued")
		}
	}
	return box, outcome
}

func startNotifications(ctx context.Context, box *notify.Outbox, logger *slog.Logger) <-chan struct{} {
	done := make(chan struct{})
	if box == nil {
		close(done)
		return done
	}
	go func() {
		defer close(done)
		box.Run(ctx, func() { logger.WarnContext(ctx, "notification delivery or persistence failed") })
	}()
	return done
}
