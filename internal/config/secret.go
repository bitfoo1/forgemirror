package config

import "log/slog"

const redacted = "[REDACTED]"

// Secret is a string that refuses to be printed.
//
// Every formatting path (fmt verbs, slog, encoding/json and other text
// marshalers) yields a placeholder, so logging a whole Config cannot leak
// credentials. Code that really needs the value calls [Secret.Reveal].
type Secret string

// Reveal returns the secret value.
func (s Secret) Reveal() string { return string(s) }

// String implements [fmt.Stringer].
func (s Secret) String() string {
	if s == "" {
		return ""
	}

	return redacted
}

// GoString implements [fmt.GoStringer] so %#v does not leak either.
func (s Secret) GoString() string { return s.String() }

// LogValue implements [slog.LogValuer].
func (s Secret) LogValue() slog.Value { return slog.StringValue(s.String()) }

// MarshalText implements [encoding.TextMarshaler].
func (s Secret) MarshalText() ([]byte, error) { return []byte(s.String()), nil }
