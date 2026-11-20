package config

import (
	"errors"
	"fmt"
	"net/url"
	"time"
)

// Notifications configures the optional durable outbound webhook.
type Notifications struct {
	WebhookURL     Secret        `koanf:"webhook_url"`
	WebhookURLFile string        `koanf:"webhook_url_file"`
	Token          Secret        `koanf:"token"`
	TokenFile      string        `koanf:"token_file"`
	UIURL          string        `koanf:"ui_url"`
	Timeout        time.Duration `koanf:"timeout"`
}

func (c *Config) validateNotifications() []error {
	var errs []error
	for key, raw := range map[string]string{"webhook_url": c.Notifications.WebhookURL.Reveal(), "ui_url": c.Notifications.UIURL} {
		if raw == "" {
			continue
		}
		u, err := url.Parse(raw)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil ||
			u.Fragment != "" {
			errs = append(
				errs,
				fmt.Errorf("notifications.%s: must be an HTTP(S) URL without credentials or fragment", key),
			)
		}
	}
	if c.Notifications.Timeout <= 0 {
		errs = append(errs, errors.New("notifications.timeout: must be greater than zero"))
	}
	return errs
}
