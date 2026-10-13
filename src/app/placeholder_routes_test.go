package app

// placeholder_routes_test.go covers the honest placeholders for the two
// full-screen upstream routes whose backend is not yet ported (project-boot,
// import-sessions): selecting the route renders its tracked title + hint.

import (
	"encoding/json"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestPlaceholderRoutesRender(t *testing.T) {
	for _, tc := range []struct{ route, want string }{
		{"import-sessions", "import_sessions"}, // unique hint, not the nav label
		{"project-boot", "project_boot_*"},
	} {
		s := New(newBackendForTest(t))
		s.stream = newStreamForTest()
		s.workbench = tc.route
		s.cache["appearance-mode"] = json.RawMessage(`{"mode":"light"}`)
		s.cache["folders"] = json.RawMessage(`[]`)
		tt := ui.NewTester(s.View, 1240, 820)
		if !tt.HasText(tc.want) {
			t.Errorf("route %q missing %q; texts = %v", tc.route, tc.want, tt.Texts())
		}
	}
}
