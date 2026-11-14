package version_test

import (
	"runtime/debug"
	"strings"
	"testing"

	"go.bitfoo.de/forgemirror/internal/version"
)

func TestFromBuildInfo(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		bi   *debug.BuildInfo
		want version.Info
	}{
		{
			name: "tagged clean build",
			bi: &debug.BuildInfo{
				GoVersion: "go1.27.4",
				Main:      debug.Module{Version: "v1.2.3"},
				Settings: []debug.BuildSetting{
					{Key: "vcs.revision", Value: "abc123"},
					{Key: "vcs.modified", Value: "false"},
				},
			},
			want: version.Info{Version: "v1.2.3", Commit: "abc123", GoVersion: "go1.27.4"},
		},
		{
			name: "dirty build",
			bi: &debug.BuildInfo{
				Main:     debug.Module{Version: "v1.2.4-0.20261114-abc123+dirty"},
				Settings: []debug.BuildSetting{{Key: "vcs.modified", Value: "true"}},
			},
			want: version.Info{Version: "v1.2.4-0.20261114-abc123+dirty", Modified: true},
		},
		{
			name: "no version from toolchain",
			bi:   &debug.BuildInfo{},
			want: version.Info{Version: "(devel)"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := version.FromBuildInfo(tc.bi); got != tc.want {
				t.Errorf("FromBuildInfo() = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestInfoString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		info version.Info
		want string
	}{
		{
			name: "full",
			info: version.Info{Version: "v1.0.0", Commit: "abc", GoVersion: "go1.27.4"},
			want: "v1.0.0 (commit abc, go1.27.4)",
		},
		{
			name: "dirty without commit",
			info: version.Info{Version: "(devel)", Modified: true, GoVersion: "go1.27.4"},
			want: "(devel) (commit unknown-dirty, go1.27.4)",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := tc.info.String(); got != tc.want {
				t.Errorf("String() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestGet(t *testing.T) {
	t.Parallel()

	// Test binaries always carry build info; the exact version depends on how
	// the tests are run, so only the invariants are asserted.
	info := version.Get()
	if info.Version == "" {
		t.Error("Get().Version is empty")
	}
	if !strings.HasPrefix(info.GoVersion, "go") {
		t.Errorf("Get().GoVersion = %q, want go toolchain version", info.GoVersion)
	}
}
