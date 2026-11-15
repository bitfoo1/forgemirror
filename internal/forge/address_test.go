package forge_test

import (
	"testing"

	"go.bitfoo.de/forgemirror/internal/forge"
)

func TestParseAddress(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, typ, api, url string
		want                forge.Address
		wantErr             bool
	}{
		{
			"github.com", "github", "", "https://github.com/acme/app.git",
			forge.Address{APIURL: "https://api.github.com", Namespace: "acme", Name: "app"},
			false,
		},
		{
			"github enterprise", "github", "", "https://ghe.example/acme/app.git",
			forge.Address{APIURL: "https://ghe.example/api/v3", Namespace: "acme", Name: "app"},
			false,
		},
		{
			"gitlab subgroup", "gitlab", "", "https://gitlab.com/group/sub/app.git",
			forge.Address{APIURL: "https://gitlab.com/api/v4", Namespace: "group/sub", Name: "app"},
			false,
		},
		{
			"explicit api url", "forgejo", "https://f.example/api/v1/", "https://f.example/a/b.git",
			forge.Address{APIURL: "https://f.example/api/v1", Namespace: "a", Name: "b"},
			false,
		},
		{
			"forgejo http with port", "forgejo", "", "http://localhost:3000/a/b.git",
			forge.Address{APIURL: "http://localhost:3000/api/v1", Namespace: "a", Name: "b"},
			false,
		},
		{"no namespace", "github", "", "https://github.com/app.git", forge.Address{}, true},
		{"unknown type", "bitbucket", "", "https://bitbucket.org/a/b.git", forge.Address{}, true},
		{"invalid url", "github", "", "://bad", forge.Address{}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := forge.ParseAddress(tc.typ, tc.api, tc.url)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if got != tc.want {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}
