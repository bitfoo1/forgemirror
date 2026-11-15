// Package config loads, resolves and validates the forgemirror configuration.
//
// Sources are merged in this order, later ones win: built-in defaults, the
// YAML file, FORGEMIRROR_* environment variables, CLI flag overrides.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/go-viper/mapstructure/v2"
	"github.com/knadh/koanf/parsers/yaml"
	"github.com/knadh/koanf/providers/confmap"
	"github.com/knadh/koanf/providers/env/v2"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/v2"

	"go.bitfoo.de/forgemirror/internal/filter"
)

const (
	// EnvPrefix prefixes every environment variable that maps to a config key.
	// Nesting uses a double underscore, e.g. FORGEMIRROR_SERVER__LISTEN_ADDRESS
	// for server.listen_address. A single underscore cannot be the separator
	// because keys themselves contain underscores.
	EnvPrefix = "FORGEMIRROR_"
	// EnvConfigFile names the config file; it is read by the CLI, not mapped
	// to a config key.
	EnvConfigFile = EnvPrefix + "CONFIG"
	// FileName is the config file name looked up in the default locations.
	FileName = "forgemirror.yaml"

	envNestingSeparator = "__"
	keyDelimiter        = "."
	defaultUsername     = "oauth2"

	defaultReadHeaderTimeout = 10 * time.Second
	// Kubernetes waits 30s (terminationGracePeriodSeconds) by default; the
	// deployment raises that, so running mirrors get a chance to finish.
	defaultShutdownTimeout = 60 * time.Second
	// Forgejo push payloads list commits with their changed files; 5 MiB
	// leaves ample headroom while still bounding memory per request.
	defaultMaxBodyBytes  = 5 << 20
	defaultMirrorTimeout = 10 * time.Minute
	defaultConcurrency   = 4
	// Enough to see the last few runs of a handful of repositories while
	// bounding memory (a few hundred KiB).
	defaultUILogEntries = 1000
)

// Config is the complete runtime configuration.
type Config struct {
	Server  Server       `koanf:"server"`
	UI      UI           `koanf:"ui"`
	Log     Log          `koanf:"log"`
	Webhook Webhook      `koanf:"webhook"`
	Source  Source       `koanf:"source"`
	Sync    Sync         `koanf:"sync"`
	Mirror  Mirror       `koanf:"mirror"`
	Filter  filter.Rules `koanf:"filter"`
	Targets []Target     `koanf:"targets"`
}

// Server configures the webhook and ops HTTP listeners.
type Server struct {
	// ListenAddress is the host:port of the webhook listener.
	ListenAddress string `koanf:"listen_address"`
	// OpsListenAddress is the host:port of the health and metrics listener.
	// It is separate from the webhook so that exposing the webhook (e.g.
	// through an ingress) never exposes metrics or probes.
	OpsListenAddress string `koanf:"ops_listen_address"`
	// ReadHeaderTimeout bounds how long a client may take to send headers.
	ReadHeaderTimeout time.Duration `koanf:"read_header_timeout"`
	// ShutdownTimeout bounds the graceful shutdown, including running mirrors.
	ShutdownTimeout time.Duration `koanf:"shutdown_timeout"`
}

// UI configures the read-only web UI. It has no authentication of its own;
// operators put a proxy or SSO in front of it, which is why it gets its own
// listener and is off by default.
type UI struct {
	Enabled bool `koanf:"enabled"`
	// ListenAddress is the host:port of the web listener.
	ListenAddress string `koanf:"listen_address"`
	// LogEntries is the number of recent log records kept for the UI.
	LogEntries int `koanf:"log_entries"`
}

// Log configures logging.
type Log struct {
	Level  slog.Level `koanf:"level"`
	Format string     `koanf:"format"`
}

// Log formats.
const (
	LogFormatJSON = "json"
	LogFormatText = "text"
)

// Webhook configures the webhook endpoint.
type Webhook struct {
	// Secret is the HMAC secret configured on the Forgejo webhook.
	Secret Secret `koanf:"secret"`
	// SecretFile is read into Secret; for Kubernetes Secret mounts.
	SecretFile string `koanf:"secret_file"`
	// MaxBodyBytes caps the accepted request body size.
	MaxBodyBytes int64 `koanf:"max_body_bytes"`
}

// Source configures access to the Forgejo instance repositories are cloned from.
type Source struct {
	// BaseURL is the Forgejo URL clone URLs must start with; empty accepts
	// any clone URL. See [Source.CheckCloneURL].
	BaseURL   string `koanf:"base_url"`
	Username  string `koanf:"username"`
	Token     Secret `koanf:"token"`
	TokenFile string `koanf:"token_file"`
}

// Sync selects what starts a mirror run.
type Sync struct {
	// OnPush lets Forgejo push webhooks start runs.
	OnPush bool `koanf:"on_push"`
	// Schedule is the interval of scheduled runs; 0 disables them.
	Schedule time.Duration `koanf:"schedule"`
}

// PrivatePolicy returns how the filter admits private repositories. Typed
// targets and schedules make private repositories opt-in through
// filter.allow_private; push-only setups with untyped targets keep relying on
// filter.exclude_private, so existing deployments do not change silently.
func (c *Config) PrivatePolicy() filter.PrivatePolicy {
	if c.Typed() || c.Sync.Schedule > 0 {
		return filter.PrivateOptIn
	}

	return filter.PrivateUnlessExcluded
}

// Typed reports whether any target talks to a forge API.
func (c *Config) Typed() bool {
	return slices.ContainsFunc(c.Targets, func(t Target) bool { return t.Type != "" })
}

// Mirror configures how git runs.
type Mirror struct {
	// Timeout bounds one complete mirror operation (clone + push) per target.
	Timeout time.Duration `koanf:"timeout"`
	// MaxConcurrent caps the number of git operations running at once.
	MaxConcurrent int `koanf:"max_concurrent"`
	// GitBinary is the git executable (name in PATH or absolute path).
	GitBinary string `koanf:"git_binary"`
	// WorkDir is where temporary clones are created; empty means the OS default.
	WorkDir string `koanf:"work_dir"`
}

// Options controls where [Load] reads configuration from.
type Options struct {
	// File is the YAML file to read. Empty means: no file.
	File string
	// Environ returns the environment as "KEY=value" pairs; nil means [os.Environ].
	Environ func() []string
	// Overrides are applied last, keyed by dotted config path (CLI flags).
	Overrides map[string]any
}

// Load merges all configuration sources, resolves secret files and validates
// the result.
func Load(opts Options) (*Config, error) {
	k := koanf.New(keyDelimiter)

	if err := k.Load(confmap.Provider(defaults(), keyDelimiter), nil); err != nil {
		return nil, fmt.Errorf("loading defaults: %w", err)
	}

	if opts.File != "" {
		if err := k.Load(file.Provider(opts.File), yaml.Parser()); err != nil {
			return nil, fmt.Errorf("loading config file %s: %w", opts.File, err)
		}
	}

	environ := opts.Environ
	if environ == nil {
		environ = os.Environ
	}
	envProvider := env.Provider(keyDelimiter, env.Opt{
		Prefix:        EnvPrefix,
		TransformFunc: envKey,
		EnvironFunc:   environ,
	})
	if err := k.Load(envProvider, nil); err != nil {
		return nil, fmt.Errorf("loading environment: %w", err)
	}

	if len(opts.Overrides) > 0 {
		if err := k.Load(confmap.Provider(opts.Overrides, keyDelimiter), nil); err != nil {
			return nil, fmt.Errorf("loading overrides: %w", err)
		}
	}

	var cfg Config
	if err := k.UnmarshalWithConf("", &cfg, unmarshalConf()); err != nil {
		return nil, fmt.Errorf("decoding config: %w", err)
	}

	if err := cfg.resolve(); err != nil {
		return nil, err
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config:\n%w", err)
	}

	return &cfg, nil
}

// DefaultFiles lists the locations searched when no config file is given,
// in order of precedence.
func DefaultFiles() []string {
	files := []string{FileName}
	if dir, err := os.UserConfigDir(); err == nil {
		files = append(files, filepath.Join(dir, "forgemirror", FileName))
	}

	return files
}

// FindFile returns the first existing file of candidates, or "" if none exists.
func FindFile(candidates []string) (string, error) {
	for _, path := range candidates {
		_, err := os.Stat(path)
		if err == nil {
			return path, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("checking config file %s: %w", path, err)
		}
	}

	return "", nil
}

func defaults() map[string]any {
	return map[string]any{
		"server.listen_address":      ":8081",
		"server.ops_listen_address":  ":9090",
		"server.read_header_timeout": defaultReadHeaderTimeout,
		"server.shutdown_timeout":    defaultShutdownTimeout,
		"ui.enabled":                 false,
		"ui.listen_address":          ":8080",
		"ui.log_entries":             defaultUILogEntries,
		"log.level":                  "info",
		"log.format":                 LogFormatJSON,
		"webhook.max_body_bytes":     defaultMaxBodyBytes,
		"source.username":            defaultUsername,
		"sync.on_push":               true,
		"mirror.timeout":             defaultMirrorTimeout,
		"mirror.max_concurrent":      defaultConcurrency,
		"mirror.git_binary":          "git",
	}
}

// envKey maps FORGEMIRROR_SERVER__LISTEN_ADDRESS to server.listen_address.
func envKey(key, value string) (string, any) {
	if key == EnvConfigFile {
		return "", nil // consumed by the CLI; not a config key
	}
	key = strings.ToLower(strings.TrimPrefix(key, EnvPrefix))

	return strings.ReplaceAll(key, envNestingSeparator, keyDelimiter), value
}

// unmarshalConf exists only to turn on ErrorUnused: unknown keys must be
// errors, because a typo such as "listen_adress" would otherwise silently fall
// back to the default. koanf's own decoder config cannot be extended, so
// passing ours replaces it, and its duration and TextUnmarshaler hooks have to
// be restated here. koanf fills in Result and TagName itself.
func unmarshalConf() koanf.UnmarshalConf {
	return koanf.UnmarshalConf{
		Tag: "koanf",
		DecoderConfig: &mapstructure.DecoderConfig{
			DecodeHook: mapstructure.ComposeDecodeHookFunc(
				mapstructure.StringToTimeDurationHookFunc(),
				mapstructure.TextUnmarshallerHookFunc(),
			),
			ErrorUnused:      true,
			WeaklyTypedInput: true,
		},
	}
}

// resolve fills secrets from their *_file counterparts and applies per-target
// defaults that cannot be expressed in the defaults map (list elements).
func (c *Config) resolve() error {
	var errs []error

	if err := readSecretFile(&c.Webhook.Secret, c.Webhook.SecretFile, "webhook.secret"); err != nil {
		errs = append(errs, err)
	}
	if err := readSecretFile(&c.Source.Token, c.Source.TokenFile, "source.token"); err != nil {
		errs = append(errs, err)
	}

	for i := range c.Targets {
		t := &c.Targets[i]
		if err := readSecretFile(&t.Token, t.TokenFile, fmt.Sprintf("targets[%d].token", i)); err != nil {
			errs = append(errs, err)
		}
		if t.Username == "" {
			t.Username = defaultUsername
		}
	}

	return errors.Join(errs...)
}

func readSecretFile(dst *Secret, path, key string) error {
	if path == "" {
		return nil
	}
	if *dst != "" {
		return fmt.Errorf("%s: set either the value or %s_file, not both", key, key)
	}

	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return fmt.Errorf("%s_file: %w", key, err)
	}
	// Secret files commonly end with a newline that is not part of the secret.
	*dst = Secret(strings.TrimSpace(string(data)))

	return nil
}
