package config

import (
	"errors"
	"fmt"
	"net"
)

// Validate checks the configuration and compiles derived values such as the
// target URL templates. It reports all problems at once, so a broken config
// can be fixed in one go instead of one error per restart.
func (c *Config) Validate() error {
	var errs []error
	add := func(key string, err error) {
		errs = append(errs, fmt.Errorf("%s: %w", key, err))
	}

	if _, _, err := net.SplitHostPort(c.Server.ListenAddress); err != nil {
		add("server.listen_address", err)
	}
	if c.Server.ReadHeaderTimeout <= 0 {
		add("server.read_header_timeout", errMustBePositive)
	}
	if c.Server.ShutdownTimeout <= 0 {
		add("server.shutdown_timeout", errMustBePositive)
	}

	if c.Log.Format != LogFormatJSON && c.Log.Format != LogFormatText {
		add("log.format", fmt.Errorf("must be %q or %q, got %q", LogFormatJSON, LogFormatText, c.Log.Format))
	}

	if c.Webhook.Secret == "" {
		add("webhook.secret", errRequired)
	}
	if c.Webhook.MaxBodyBytes <= 0 {
		add("webhook.max_body_bytes", errMustBePositive)
	}

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

var (
	errRequired       = errors.New("required")
	errMustBePositive = errors.New("must be greater than zero")
)

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
	}

	return errs
}
