package config

import (
	"errors"
	"fmt"
	"net"
	"slices"

	"go.bitfoo.de/forgemirror/internal/forge"
)

// Validate checks the configuration and compiles derived values such as the
// target URL templates. It reports all problems at once, so a broken config
// can be fixed in one go instead of one error per restart.
func (c *Config) Validate() error {
	var errs []error
	add := func(key string, err error) {
		errs = append(errs, fmt.Errorf("%s: %w", key, err))
	}

	errs = append(errs, c.validateListeners()...)
	if c.Server.ReadHeaderTimeout <= 0 {
		add("server.read_header_timeout", errMustBePositive)
	}
	if c.Server.ShutdownTimeout <= 0 {
		add("server.shutdown_timeout", errMustBePositive)
	}

	if c.Log.Format != LogFormatJSON && c.Log.Format != LogFormatText {
		add("log.format", fmt.Errorf("must be %q or %q, got %q", LogFormatJSON, LogFormatText, c.Log.Format))
	}

	// Pushes and deletions both arrive as webhooks; without either, the
	// endpoint is not needed and a secret would only be ceremony.
	if c.Webhook.Secret == "" && (c.Sync.OnPush || c.anyDelete()) {
		add("webhook.secret", errRequired)
	}
	if c.Webhook.MaxBodyBytes <= 0 {
		add("webhook.max_body_bytes", errMustBePositive)
	}

	if err := c.Source.validate(); err != nil {
		add("source.base_url", err)
	}
	errs = append(errs, c.validateSync()...)

	if c.Mirror.Timeout <= 0 {
		add("mirror.timeout", errMustBePositive)
	}
	if c.Mirror.MaxConcurrent <= 0 {
		add("mirror.max_concurrent", errMustBePositive)
	}
	if c.Mirror.GitBinary == "" {
		add("mirror.git_binary", errRequired)
	}

	if err := c.Filter.Validate(); err != nil {
		errs = append(errs, err)
	}

	errs = append(errs, c.validateTargets()...)

	return errors.Join(errs...)
}

// validateListeners checks the listen addresses. They must differ, because
// each port has its own audience: sharing one would put the UI or the
// metrics behind the webhook's ingress.
func (c *Config) validateListeners() []error {
	listeners := []struct{ key, addr string }{
		{"server.listen_address", c.Server.ListenAddress},
		{"server.ops_listen_address", c.Server.OpsListenAddress},
	}
	if c.UI.Enabled {
		listeners = append(listeners, struct{ key, addr string }{"ui.listen_address", c.UI.ListenAddress})
	}

	var errs []error
	seen := make(map[string]string, len(listeners))
	for _, l := range listeners {
		host, port, err := net.SplitHostPort(l.addr)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", l.key, err))

			continue
		}
		// Port 0 picks a free port, so equal addresses do not collide.
		if port == "0" {
			continue
		}
		norm := net.JoinHostPort(host, port)
		if other, dup := seen[norm]; dup {
			errs = append(errs, fmt.Errorf("%s: %q is already used by %s", l.key, l.addr, other))

			continue
		}
		seen[norm] = l.key
	}

	if c.UI.LogEntries <= 0 {
		errs = append(errs, fmt.Errorf("ui.log_entries: %w", errMustBePositive))
	}

	return errs
}

var (
	errRequired       = errors.New("required")
	errMustBePositive = errors.New("must be greater than zero")
)

func (c *Config) validateSync() []error {
	var errs []error
	add := func(key string, err error) {
		errs = append(errs, fmt.Errorf("%s: %w", key, err))
	}

	if c.UsesSourceAPI() {
		const need = "required when a target has a type, sync.schedule is set or sync.auto is false"
		if c.Source.BaseURL == "" {
			add("source.base_url", errors.New(need))
		}
		if c.Source.Token == "" {
			add("source.token", errors.New(need))
		}
	}

	if c.Sync.Schedule < 0 {
		add("sync.schedule", errors.New("must not be negative"))
	}
	// In review mode the UI starts runs, so neither trigger is needed; without
	// the UI nothing could ever apply a plan.
	if c.Sync.Auto && !c.Sync.OnPush && c.Sync.Schedule <= 0 {
		add("sync", errors.New("on_push is false and no schedule is set"))
	}
	if !c.Sync.Auto && !c.UI.Enabled {
		add("sync.auto", errors.New("false requires ui.enabled, the only place to apply changes"))
	}

	return errs
}

func (c *Config) anyDelete() bool {
	return slices.ContainsFunc(c.Targets, func(t Target) bool { return t.Delete })
}

func (c *Config) validateTargets() []error {
	if len(c.Targets) == 0 {
		return []error{errors.New("targets: at least one target is required")}
	}

	var errs []error
	seen := make(map[string]int, len(c.Targets))
	for i := range c.Targets {
		t := &c.Targets[i]
		add := func(field string, err error) {
			errs = append(errs, fmt.Errorf("targets[%d].%s: %w", i, field, err))
		}

		switch first, dup := seen[t.Name]; {
		case t.Name == "":
			add("name", errRequired)
		case dup:
			add("name", fmt.Errorf("%q is already used by targets[%d]", t.Name, first))
		default:
			seen[t.Name] = i
		}

		if t.URL == "" {
			add("url", errRequired)
		} else if err := t.compile(); err != nil {
			errs = append(errs, fmt.Errorf("targets[%d].%w", i, err))
		}

		if t.Token == "" {
			add("token", errors.New("required (or token_file)"))
		}

		errs = append(errs, t.validateTyped(i)...)
	}

	return errs
}

func (t *Target) validateTyped(i int) []error {
	var errs []error
	add := func(field string, err error) {
		errs = append(errs, fmt.Errorf("targets[%d].%s: %w", i, field, err))
	}

	switch t.Type {
	case forge.TypeGitHub, forge.TypeGitLab, forge.TypeForgejo:
	case "":
		errNeedsType := errors.New("requires type")
		for _, f := range []struct {
			field string
			set   bool
		}{
			{"create", t.Create},
			{"delete", t.Delete},
			{"prune_labels", t.PruneLabels},
			{"api_url", t.APIURL != ""},
			{"settings", !t.Settings.IsZero()},
			{"overrides", len(t.Overrides) > 0},
		} {
			if f.set {
				add(f.field, errNeedsType)
			}
		}
	default:
		add("type", fmt.Errorf("must be one of %s, %s, %s", forge.TypeGitHub, forge.TypeGitLab, forge.TypeForgejo))
	}

	if err := t.Settings.Validate(); err != nil {
		add("settings", err)
	}
	for _, err := range t.compileOverrides() {
		errs = append(errs, fmt.Errorf("targets[%d].%w", i, err))
	}

	return errs
}
