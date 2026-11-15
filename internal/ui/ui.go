// Package ui serves the read-only web UI: overview, repository and target
// state, recent logs and the effective configuration.
//
// The UI has no authentication. Operators put a reverse proxy or SSO in
// front of it, and it runs on its own listener so that this decision does not
// depend on path rules. It never changes state: without a login of its own,
// every state-changing request would be open to cross-site request forgery.
package ui

import (
	"bytes"
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/knadh/koanf/parsers/yaml"

	"go.bitfoo.de/forgemirror/internal/config"
	"go.bitfoo.de/forgemirror/internal/logging"
	"go.bitfoo.de/forgemirror/internal/status"
)

const (
	// refresh is how often htmx reloads the changing sections.
	refresh = 5 * time.Second
	// subsecond is the display precision of durations between 1s and 1m.
	subsecond = 100 * time.Millisecond
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

//go:embed static/htmx.min.js static/style.css
var staticFS embed.FS

// Options configures [Handler].
type Options struct {
	Config *config.Config
	Store  *status.Store
	// Logs may be nil.
	Logs *logging.Ring
	// Queued returns the repositories with a run in progress, each mapped to
	// whether a follow-up run is queued; nil means none.
	Queued  func() map[string]bool
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
}

// Handler returns the UI's routing table. Only GET (and HEAD) are served.
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

	return withHeaders(onlyRead(mux)), nil
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

// onlyRead rejects every method but GET and HEAD before routing, so no path
// (including unknown ones) ever accepts a state-changing request.
func onlyRead(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)

			return
		}
		next.ServeHTTP(w, r)
	})
}

// level is an option of the log level filter.
type level struct {
	Value, Label string
	Selected     bool
}

type pageData struct {
	Version     string
	Refresh     time.Duration
	Snapshot    status.Snapshot
	Targets     []string
	OnPush      bool
	Running     int
	Queued      int
	QueuedRepos map[string]bool
	Config      string
	Logs        []logging.Entry
	Levels      []level
}

func (h *handler) render(name string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		data := h.data(r)

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
}

func (h *handler) data(r *http.Request) pageData {
	queued := h.opts.Queued()
	d := pageData{
		Version:     h.opts.Version,
		Refresh:     refresh,
		Snapshot:    h.opts.Store.Snapshot(),
		Targets:     h.targets,
		OnPush:      h.opts.Config.Sync.OnPush,
		QueuedRepos: queued,
		Config:      h.config,
	}
	for _, q := range queued {
		d.Running++
		if q {
			d.Queued++
		}
	}

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
