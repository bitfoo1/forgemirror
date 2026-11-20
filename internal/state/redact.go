package state

import (
	"bytes"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
)

// Redactor sanitizes free-text plan/result fields, not JSON keys or lifecycle
// identifiers. Even a short credential must not corrupt timestamps or statuses.
func Redactor(secrets []string) func([]byte) ([]byte, error) {
	credentials := regexp.MustCompile(`(https?://)[^/\s"<>]*@`)
	scrub := func(text string) string {
		text = credentials.ReplaceAllString(text, "${1}[REDACTED]@")
		for _, secret := range secrets {
			if secret != "" {
				text = strings.ReplaceAll(text, secret, "[REDACTED]")
			}
		}
		return text
	}
	return func(data []byte) ([]byte, error) {
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.UseNumber()
		var value any
		if err := decoder.Decode(&value); err != nil {
			return nil, errors.New("invalid state payload for redaction")
		}
		cleaned := redactValue(value, "", scrub)
		encoded, err := json.Marshal(cleaned)
		if err != nil {
			return nil, errors.New("encoding redacted state")
		}
		return encoded, nil
	}
}

func redactValue(value any, key string, scrub func(string) string) any {
	switch typed := value.(type) {
	case map[string]any:
		for name, item := range typed {
			typed[name] = redactValue(item, name, scrub)
		}
	case []any:
		for i, item := range typed {
			typed[i] = redactValue(item, key, scrub)
		}
	case string:
		switch key {
		// These are structured identifiers, never credential/config fields.
		case "name",
			"status",
			"ref",
			"field",
			"tag",
			"at",
			"started",
			"finished",
			"last_started",
			"last_finished",
			"last_run",
			"created_at":
			return typed
		default:
			return scrub(typed)
		}
	}
	return value
}
