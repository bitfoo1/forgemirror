// Package webhook receives and authenticates Forgejo webhook events.
package webhook

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"

	"go.bitfoo.de/forgemirror/internal/filter"
	"go.bitfoo.de/forgemirror/internal/repo"
)

// Forgejo sends both header families; X-Gitea-* is accepted as a fallback so
// the service also works with Gitea, which uses the same payload format.
const (
	headerForgejoSignature = "X-Forgejo-Signature"
	headerGiteaSignature   = "X-Gitea-Signature"
	headerForgejoEvent     = "X-Forgejo-Event"
	headerGiteaEvent       = "X-Gitea-Event"
	headerForgejoDelivery  = "X-Forgejo-Delivery"
	headerGiteaDelivery    = "X-Gitea-Delivery"

	pushEventName       = "push"
	repositoryEventName = "repository"
	releaseEventName    = "release"
	packageEventName    = "package"

	actionDeleted = "deleted"
)

// Event is an authenticated push that passed the filter.
type Event struct {
	Repository repo.Repository
	// Target limits an operator-requested run; empty means all destinations.
	Target string
	// Retry preserves the existing retry budget instead of starting a new one.
	Retry    bool
	ParentID uint64
	// Operator retains explicit write intent when events coalesce.
	Operator bool
	// Ref is the pushed ref, e.g. "refs/heads/main".
	Ref string
	// Delivery is Forgejo's delivery ID, for correlating logs.
	Delivery string
	// Kind selects what the run does; the zero value is a push.
	Kind EventKind
}

// EventKind is what started a run.
type EventKind int

// Event kinds.
const (
	// EventPush mirrors after a push. Push, scheduled and delete events are
	// applied only when sync.auto is true; otherwise they only plan.
	EventPush EventKind = iota
	// EventScheduled mirrors periodically.
	EventScheduled
	// EventDelete removes the repository from targets with delete enabled.
	EventDelete
	// EventRefresh only computes the plan, in every sync mode (UI refresh).
	EventRefresh
	// EventSync computes and applies the plan, in every sync mode (UI sync).
	EventSync
	// EventRepairLFS reconciles LFS objects without publishing Git refs.
	EventRepairLFS
)

// Dispatcher runs the mirror work for an event. Dispatch must not block on
// the mirror itself: Forgejo times out webhook deliveries after a few
// seconds, while a mirror can take minutes.
type Dispatcher interface {
	Dispatch(ctx context.Context, e Event)
}

// Filter decides whether a push is mirrored.
type Filter interface {
	Decide(r repo.Repository, ref string) filter.Decision
}

// Observer is notified about every request outcome (metrics).
type Observer interface {
	ObserveWebhook(ctx context.Context, outcome Outcome)
}

// Outcome classifies a webhook request.
type Outcome string

// Webhook outcomes.
const (
	OutcomeAccepted       Outcome = "accepted"
	OutcomeFiltered       Outcome = "filtered"
	OutcomeIgnored        Outcome = "ignored"
	OutcomeUnauthorized   Outcome = "unauthorized"
	OutcomeBadRequest     Outcome = "bad_request"
	OutcomeTooLarge       Outcome = "too_large"
	OutcomeUnsupportedMed Outcome = "unsupported_media_type"
)

// Source vets the clone URL of a payload. The repository is cloned with the
// source token, so the URL decides which host receives that token.
type Source interface {
	CheckCloneURL(raw string) error
}

// Options configures a [Handler].
type Options struct {
	Secret       []byte
	MaxBodyBytes int64
	Filter       Filter
	// Source is optional; without it every clone URL is accepted.
	Source     Source
	Dispatcher Dispatcher
	Logger     *slog.Logger
	// Observer is optional.
	Observer Observer
	// IgnorePush acknowledges push events without mirroring (sync.on_push is
	// false); the endpoint stays up for repository events.
	IgnorePush bool
}

// Handler serves the webhook endpoint.
type Handler struct {
	opts Options
}

// NewHandler returns a webhook [Handler].
func NewHandler(opts Options) *Handler {
	return &Handler{opts: opts}
}

// ServeHTTP implements [http.Handler].
//
// Push, release and package events queue a run; repository "deleted" events
// queue a deletion.
//
// Status codes: 202 queued for mirroring; 200 authenticated but not mirrored
// (other event type, filtered, or a package without repository); 400 malformed payload or a clone URL the
// [Source] rejects; 401 missing or wrong signature; 413 body too large; 415
// not JSON.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	delivery := firstHeader(r.Header, headerForgejoDelivery, headerGiteaDelivery)
	logger := h.opts.Logger.With(slog.String("delivery", delivery))

	if mediaType, _, err := mime.ParseMediaType(
		r.Header.Get("Content-Type"),
	); err != nil ||
		mediaType != "application/json" {
		h.respond(ctx, w, logger, http.StatusUnsupportedMediaType, OutcomeUnsupportedMed,
			"content type must be application/json; set the webhook's POST content type accordingly")

		return
	}

	// The body is read in full before anything else: the signature covers the
	// exact raw bytes, so it must be verified before the JSON is trusted.
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, h.opts.MaxBodyBytes))
	if err != nil {
		if maxErr := (*http.MaxBytesError)(nil); errors.As(err, &maxErr) {
			h.respond(ctx, w, logger, http.StatusRequestEntityTooLarge, OutcomeTooLarge, "request body too large")

			return
		}
		h.respond(ctx, w, logger, http.StatusBadRequest, OutcomeBadRequest, "reading request body failed")

		return
	}

	if !h.validSignature(r.Header, body) {
		h.respond(ctx, w, logger, http.StatusUnauthorized, OutcomeUnauthorized, "missing or invalid signature")

		return
	}

	switch event := firstHeader(r.Header, headerForgejoEvent, headerGiteaEvent); event {
	case pushEventName:
		if h.opts.IgnorePush {
			h.respond(ctx, w, logger, http.StatusOK, OutcomeIgnored, "push events disabled (sync.on_push is false)")

			return
		}
	case repositoryEventName:
		h.handleRepository(ctx, w, logger, body, delivery)

		return
	case releaseEventName, packageEventName:
		h.handleArtifact(ctx, w, logger, event, body, delivery)

		return
	default:
		h.respond(ctx, w, logger, http.StatusOK, OutcomeIgnored, "ignored event "+event)

		return
	}

	var payload pushEvent
	if jsonErr := json.Unmarshal(body, &payload); jsonErr != nil {
		h.respond(ctx, w, logger, http.StatusBadRequest, OutcomeBadRequest, "malformed JSON payload")

		return
	}
	if invalidErr := payload.validate(); invalidErr != nil {
		h.respond(ctx, w, logger, http.StatusBadRequest, OutcomeBadRequest, "invalid payload: "+invalidErr.Error())

		return
	}
	if h.opts.Source != nil {
		if urlErr := h.opts.Source.CheckCloneURL(payload.Repository.CloneURL); urlErr != nil {
			h.respond(ctx, w, logger, http.StatusBadRequest, OutcomeBadRequest, "invalid payload: "+urlErr.Error())

			return
		}
	}

	event := Event{Repository: payload.repository(), Ref: payload.Ref, Delivery: delivery}
	logger = logger.With(slog.String("repository", event.Repository.FullName), slog.String("ref", event.Ref))

	if decision := h.opts.Filter.Decide(event.Repository, event.Ref); !decision.Mirror {
		h.respond(ctx, w, logger, http.StatusOK, OutcomeFiltered, "not mirrored: "+decision.Reason)

		return
	}

	// The request context ends with the response; the dispatcher must not
	// inherit its cancellation, only its values (trace IDs etc.).
	h.opts.Dispatcher.Dispatch(context.WithoutCancel(ctx), event)
	h.respond(ctx, w, logger, http.StatusAccepted, OutcomeAccepted, "mirror queued")
}

// handleRepository queues a deletion for a repository "deleted" event.
//
// The filter still applies: when a target URL template lacks the owner, an
// excluded bob/app would otherwise delete the mirror of acme/app. Applying it
// can only prevent deletions, never cause one.
func (h *Handler) handleRepository(
	ctx context.Context,
	w http.ResponseWriter,
	logger *slog.Logger,
	body []byte,
	delivery string,
) {
	var payload repositoryEvent
	if err := json.Unmarshal(body, &payload); err != nil {
		h.respond(ctx, w, logger, http.StatusBadRequest, OutcomeBadRequest, "malformed JSON payload")

		return
	}
	if payload.Action != actionDeleted {
		h.respond(ctx, w, logger, http.StatusOK, OutcomeIgnored, "ignored repository action "+payload.Action)

		return
	}
	if err := payload.validate(); err != nil {
		h.respond(ctx, w, logger, http.StatusBadRequest, OutcomeBadRequest, "invalid payload: "+err.Error())

		return
	}

	event := Event{Repository: payload.repository(), Delivery: delivery, Kind: EventDelete}
	logger = logger.With(slog.String("repository", event.Repository.FullName))

	if decision := h.opts.Filter.Decide(event.Repository, ""); !decision.Mirror {
		h.respond(ctx, w, logger, http.StatusOK, OutcomeFiltered, "not mirrored: "+decision.Reason)

		return
	}

	h.opts.Dispatcher.Dispatch(context.WithoutCancel(ctx), event)
	h.respond(ctx, w, logger, http.StatusAccepted, OutcomeAccepted, "delete queued")
}

// handleArtifact queues an ordinary run for a release or package event: the
// run compares everything, so the payload only names the repository. That
// keeps coalescing and review mode exactly as for pushes.
func (h *Handler) handleArtifact(
	ctx context.Context,
	w http.ResponseWriter,
	logger *slog.Logger,
	event string,
	body []byte,
	delivery string,
) {
	if h.opts.IgnorePush {
		h.respond(ctx, w, logger, http.StatusOK, OutcomeIgnored, event+" events disabled (sync.on_push is false)")

		return
	}

	var payload artifactEvent
	if err := json.Unmarshal(body, &payload); err != nil {
		h.respond(ctx, w, logger, http.StatusBadRequest, OutcomeBadRequest, "malformed JSON payload")

		return
	}
	if payload.Repository == nil {
		h.respond(ctx, w, logger, http.StatusOK, OutcomeIgnored, "not linked to a repository")

		return
	}
	if err := payload.validate(); err != nil {
		h.respond(ctx, w, logger, http.StatusBadRequest, OutcomeBadRequest, "invalid payload: "+err.Error())

		return
	}
	if h.opts.Source != nil {
		if err := h.opts.Source.CheckCloneURL(payload.Repository.CloneURL); err != nil {
			h.respond(ctx, w, logger, http.StatusBadRequest, OutcomeBadRequest, "invalid payload: "+err.Error())

			return
		}
	}

	run := Event{
		Repository: (&pushEvent{Repository: *payload.Repository}).repository(),
		Ref:        payload.ref(),
		Delivery:   delivery,
	}
	logger = logger.With(slog.String("repository", run.Repository.FullName), slog.String("ref", run.Ref))

	// The branch rules do not apply: no branch was pushed.
	if decision := h.opts.Filter.Decide(run.Repository, ""); !decision.Mirror {
		h.respond(ctx, w, logger, http.StatusOK, OutcomeFiltered, "not mirrored: "+decision.Reason)

		return
	}

	h.opts.Dispatcher.Dispatch(context.WithoutCancel(ctx), run)
	h.respond(ctx, w, logger, http.StatusAccepted, OutcomeAccepted, "mirror queued")
}

// validSignature checks the HMAC-SHA256 of body against the signature header.
//
// [hmac.Equal] compares in constant time. A plain == or [bytes.Equal] returns at
// the first differing byte; by timing many requests an attacker could learn
// a valid signature byte by byte without knowing the secret.
func (h *Handler) validSignature(header http.Header, body []byte) bool {
	got, err := hex.DecodeString(firstHeader(header, headerForgejoSignature, headerGiteaSignature))
	if err != nil || len(got) == 0 {
		return false
	}

	mac := hmac.New(sha256.New, h.opts.Secret)
	mac.Write(body)

	return hmac.Equal(got, mac.Sum(nil))
}

func (h *Handler) respond(
	ctx context.Context,
	w http.ResponseWriter,
	logger *slog.Logger,
	status int,
	outcome Outcome,
	message string,
) {
	level := slog.LevelInfo
	if status >= http.StatusBadRequest {
		level = slog.LevelWarn
	}
	logger.Log(ctx, level, "webhook "+string(outcome), slog.Int("status", status), slog.String("detail", message))

	if h.opts.Observer != nil {
		h.opts.Observer.ObserveWebhook(ctx, outcome)
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	// Plain text with nosniff cannot be rendered as HTML; the only
	// request-derived part (the event name) is echoed after authentication.
	_, _ = io.WriteString(w, message+"\n") //nolint:gosec // see above
}

func firstHeader(h http.Header, names ...string) string {
	for _, name := range names {
		if v := h.Get(name); v != "" {
			return v
		}
	}

	return ""
}
