// Package ui serves the web UI: overview, repository state with the diff a
// sync would apply, recent logs and the effective configuration, plus the
// refresh and sync actions.
//
// The UI has no authentication. Operators put a reverse proxy or SSO in
// front of it, and it runs on its own listener so that this decision does not
// depend on path rules. Without a login of its own, its actions would be open
// to cross-site request forgery through the operator's browser; Go's
// cross-origin protection (Fetch Metadata, Origin) rejects those requests
// without needing tokens or sessions.
package ui

import (
	"bytes"
	"context"
	"embed"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/knadh/koanf/parsers/yaml"

	"go.bitfoo.de/forgemirror/internal/config"
	"go.bitfoo.de/forgemirror/internal/control"
	"go.bitfoo.de/forgemirror/internal/logging"
	"go.bitfoo.de/forgemirror/internal/status"
)

const (
	// refresh is how often htmx reloads the changing sections.
	refresh = 5 * time.Second
	// subsecond is the display precision of durations between 1s and 1m.
	subsecond = 100 * time.Millisecond
	// maxFormBytes bounds an action request body; it only carries a name.
	maxFormBytes = 4 << 10
	// shortID is the display length of git object ids, as git abbreviates.
	shortID = 7
)

// securityHeaders apply to every response. The CSP allows only same-origin
// resources and no inline script or style, so injected markup cannot run.
func securityHeaders() map[string]string {
	return map[string]string{
		"Content-Security-Policy": "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self'; " +
			"connect-src 'self'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'",
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "DENY",
		"Referrer-Policy":        "no-referrer",
		"Cache-Control":          "no-store",
	}
}

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static/htmx.min.js static/style.css static/app.js
var staticFS embed.FS

// Actions are the operator actions; [control.Controller] implements them.
type Actions interface {
	Refresh(ctx context.Context, fullName string) error
	Sync(ctx context.Context, fullName string) error
	RefreshAll(ctx context.Context) (int, error)
	SyncAll(ctx context.Context) int
}

// Options configures [Handler].
type Options struct {
	Config *config.Config
	Store  *status.Store
	// Logs may be nil.
	Logs *logging.Ring
	// Queued returns the repositories with a run in progress, each mapped to
	// whether a follow-up run is queued; nil means none.
	Queued func() map[string]bool
	// Actions may be nil: the UI then shows no action buttons and answers
	// action requests with 404.
	Actions Actions
	Version string
	Logger  *slog.Logger
	// Now returns the current time; nil means [time.Now].
	Now func() time.Time
}

type handler struct {
	opts    Options
	tmpl    *template.Template
	config  string
	targets []string
	types   []targetType
}

// Handler returns the UI's routing table: GET and HEAD for pages and
// fragments, POST for the actions.
func Handler(opts Options) (http.Handler, error) {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Queued == nil {
		opts.Queued = func() map[string]bool { return nil }
	}

	h := &handler{opts: opts}
	tmpl, err := template.New("ui").Funcs(h.funcs()).ParseFS(templateFS, "templates/*.html")
	if err != nil {
		return nil, fmt.Errorf("parsing UI templates: %w", err)
	}
	h.tmpl = tmpl

	// The configuration cannot change at runtime, so it is rendered once.
	cfgYAML, err := yaml.Parser().Marshal(opts.Config.Effective())
	if err != nil {
		return nil, fmt.Errorf("rendering configuration: %w", err)
	}
	h.config = string(cfgYAML)
	for _, t := range opts.Config.Targets {
		h.targets = append(h.targets, t.Name)
	}
	h.types = targetTypes(opts.Config.Targets)

	static, err := fs.Sub(staticFS, "static")
	if err != nil {
		return nil, fmt.Errorf("static files: %w", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", h.render("page"))
	mux.HandleFunc("GET /fragments/overview", h.render("overview"))
	mux.HandleFunc("GET /fragments/repos", h.render("repos"))
	mux.HandleFunc("GET /fragments/logs", h.render("logs"))
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(static)))
	if opts.Actions != nil {
		mux.HandleFunc("POST /actions/refresh", h.action(false))
		mux.HandleFunc("POST /actions/sync", h.action(true))
	}

	// Safe methods pass unchanged; a POST from another origin is rejected
	// before it reaches an action.
	return withHeaders(allowedMethods(http.NewCrossOriginProtection().Handler(mux))), nil
}

func withHeaders(next http.Handler) http.Handler {
	headers := securityHeaders()

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for k, v := range headers {
			w.Header().Set(k, v)
		}
		next.ServeHTTP(w, r)
	})
}

// allowedMethods rejects every method but GET, HEAD and POST before routing;
// the router answers POST on read-only paths with 405 itself.
func allowedMethods(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodPost:
			next.ServeHTTP(w, r)
		default:
			w.Header().Set("Allow", "GET, HEAD, POST")
			http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		}
	})
}

// action runs refresh or sync for the repository in the form field "repo",
// or for all repositories without it. htmx gets the updated repository
// fragment; a plain form post is redirected back to the page.
func (h *handler) action(sync bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)
		if err := r.ParseForm(); err != nil {
			http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)

			return
		}

		notice, err := h.runAction(r.Context(), sync, r.PostForm.Get("repo"))
		switch {
		case errors.Is(err, control.ErrUnknownRepository):
			http.Error(w, "unknown repository", http.StatusNotFound)

			return
		case err != nil:
			h.opts.Logger.ErrorContext(r.Context(), "UI action failed", slog.Any("error", err))
			notice = err.Error()
		}

		if r.Header.Get("Hx-Request") != "true" {
			// The action path has exactly one level, so "../" is the page
			// under any proxy path prefix. http.Redirect would turn it into an
			// absolute path and lose the prefix.
			w.Header().Set("Location", "../")
			w.WriteHeader(http.StatusSeeOther)

			return
		}
		h.renderWith(w, r, "repos", func(d *pageData) { d.Notice = notice })
	}
}

func (h *handler) runAction(ctx context.Context, sync bool, name string) (string, error) {
	actions := h.opts.Actions
	switch {
	case name != "" && sync:
		return "Sync started for " + name + ".", actions.Sync(ctx, name)
	case name != "":
		return "Refresh started for " + name + ".", actions.Refresh(ctx, name)
	case sync:
		return fmt.Sprintf("Sync started for %s.", repositories(actions.SyncAll(ctx))), nil
	default:
		n, err := actions.RefreshAll(ctx)

		return fmt.Sprintf("Refresh started for %s.", repositories(n)), err
	}
}

func repositories(n int) string {
	if n == 1 {
		return "1 repository"
	}

	return fmt.Sprintf("%d repositories", n)
}

// level is an option of the log level filter.
type level struct {
	Value, Label string
	Selected     bool
}

type pageData struct {
	Version  string
	Refresh  time.Duration
	Snapshot status.Snapshot
	Targets  []string
	Types    []targetType
	OnPush   bool
	Auto     bool
	Actions  bool
	Running  int
	Queued   int
	Repos    []repoView
	Counts   counts
	Notice   string
	Config   string
	Logs     []logging.Entry
	Levels   []level
}

// counts summarises the repository states for the section header.
type counts struct {
	OutOfSync, Failed, Pending int
}

func (h *handler) render(name string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		h.renderWith(w, r, name, nil)
	}
}

func (h *handler) renderWith(w http.ResponseWriter, r *http.Request, name string, adjust func(*pageData)) {
	data := h.data(r)
	if adjust != nil {
		adjust(&data)
	}

	// Rendering into a buffer first turns a template error into a clean
	// 500 instead of a half-written page.
	var buf bytes.Buffer
	if err := h.tmpl.ExecuteTemplate(&buf, name, data); err != nil {
		h.opts.Logger.ErrorContext(
			r.Context(),
			"rendering UI",
			slog.String("template", name),
			slog.Any("error", err),
		)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)

		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = buf.WriteTo(w)
}

func (h *handler) data(r *http.Request) pageData {
	queued := h.opts.Queued()
	d := pageData{
		Version:  h.opts.Version,
		Refresh:  refresh,
		Snapshot: h.opts.Store.Snapshot(),
		Targets:  h.targets,
		Types:    h.types,
		OnPush:   h.opts.Config.Sync.OnPush,
		Auto:     h.opts.Config.Sync.Auto,
		Actions:  h.opts.Actions != nil,
		Config:   h.config,
	}
	for _, q := range queued {
		d.Running++
		if q {
			d.Queued++
		}
	}
	d.Repos, d.Counts = repoViews(d.Snapshot.Repos, queued)

	minLevel := parseLevel(r.URL.Query().Get("level"))
	for _, l := range []slog.Level{slog.LevelDebug, slog.LevelInfo, slog.LevelWarn, slog.LevelError} {
		d.Levels = append(d.Levels, level{
			Value:    strings.ToLower(l.String()),
			Label:    l.String(),
			Selected: l == minLevel,
		})
	}
	if h.opts.Logs != nil {
		for _, e := range h.opts.Logs.Entries() {
			if e.Level >= minLevel {
				d.Logs = append(d.Logs, e)
			}
		}
	}

	return d
}

// parseLevel reads the log filter; anything unknown shows everything.
func parseLevel(s string) slog.Level {
	var l slog.Level
	if err := l.UnmarshalText([]byte(s)); err != nil {
		return slog.LevelDebug
	}

	return l
}

func (h *handler) funcs() template.FuncMap {
	return template.FuncMap{
		"iso": func(t time.Time) string { return t.UTC().Format(time.RFC3339) },
		"when": func(t time.Time) string {
			return t.UTC().Format("2006-01-02 15:04:05 UTC") + " (" + ago(h.opts.Now().Sub(t)) + ")"
		},
		"clock": func(t time.Time) string { return t.UTC().Format("2006-01-02 15:04:05") },
		"since": func(t time.Time) string { return round(h.opts.Now().Sub(t)).String() },
		"round": round,
		"nextSweep": func(s status.Schedule) time.Time {
			return s.LastRun.Add(s.Interval)
		},
		"short": func(id string) string {
			if len(id) > shortID {
				return id[:shortID]
			}

			return id
		},
		"plural": func(n int, one, many string) string {
			if n == 1 {
				return one
			}

			return many
		},
		"levelClass": func(l slog.Level) string {
			switch {
			case l >= slog.LevelError:
				return "fail"
			case l >= slog.LevelWarn:
				return "warn"
			default:
				return "muted"
			}
		},
	}
}

// round shortens durations for display, keeping about two significant
// units.
func round(d time.Duration) time.Duration {
	switch {
	case d >= time.Hour:
		return d.Round(time.Minute)
	case d >= time.Minute:
		return d.Round(time.Second)
	case d >= time.Second:
		return d.Round(subsecond)
	default:
		return d.Round(time.Millisecond)
	}
}

func ago(d time.Duration) string {
	if d < 0 {
		return "in " + round(-d).String()
	}

	return round(d).String() + " ago"
}
