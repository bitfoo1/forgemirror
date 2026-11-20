package ui_test

import (
	"net/http"
	"strings"
	"testing"
)

func TestTargetsOnlyRenderForgeTiles(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	body := f.do(t, http.MethodGet, "/fragments/overview").Body.String()
	for _, name := range []string{"github", "backup"} {
		if strings.Contains(body, "<code>"+name+"</code>") {
			t.Errorf("target %q duplicated beneath forge tiles: %s", name, body)
		}
		if !strings.Contains(body, `class="kind kind-on" title="`+name+`"`) {
			t.Errorf("configured target %q missing from tile tooltip: %s", name, body)
		}
	}
}
