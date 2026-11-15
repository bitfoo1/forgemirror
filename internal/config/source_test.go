package config_test

import (
	"testing"

	"go.bitfoo.de/forgemirror/internal/config"
)

func TestSourceCheckCloneURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		baseURL string
		url     string
		wantErr bool
	}{
		{name: "no base URL accepts anything", url: "https://evil.example/x.git"},
		{name: "same host", baseURL: "https://git.example", url: "https://git.example/acme/app.git"},
		{name: "host case differs", baseURL: "https://git.example/", url: "https://GIT.example/acme/app.git"},
		{
			name:    "below base path",
			baseURL: "https://git.example/forgejo/",
			url:     "https://git.example/forgejo/acme/app.git",
		},
		{
			name:    "base path without slash",
			baseURL: "https://git.example/forgejo",
			url:     "https://git.example/forgejo/a/b.git",
		},
		{name: "other host", baseURL: "https://git.example", url: "https://evil.example/acme/app.git", wantErr: true},
		{name: "host suffix", baseURL: "https://git.example", url: "https://git.example.evil/a/b.git", wantErr: true},
		{name: "other port", baseURL: "https://git.example", url: "https://git.example:8443/a/b.git", wantErr: true},
		{name: "scheme downgrade", baseURL: "https://git.example", url: "http://git.example/a/b.git", wantErr: true},
		{name: "credentials", baseURL: "https://git.example", url: "https://u@git.example/a/b.git", wantErr: true},
		{
			name:    "sibling path",
			baseURL: "https://git.example/forgejo",
			url:     "https://git.example/forgejo-x/a.git",
			wantErr: true,
		},
		{
			name:    "path traversal",
			baseURL: "https://git.example/forgejo/",
			url:     "https://git.example/forgejo/../x/a.git",
			wantErr: true,
		},
		{name: "unparsable", baseURL: "https://git.example", url: "https://git.example/%zz", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := config.Source{BaseURL: tc.baseURL}.CheckCloneURL(tc.url)
			if gotErr := err != nil; gotErr != tc.wantErr {
				t.Errorf("CheckCloneURL(%q) error = %v, want error %t", tc.url, err, tc.wantErr)
			}
		})
	}
}
