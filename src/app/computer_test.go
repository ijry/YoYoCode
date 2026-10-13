package app

// computer_test.go covers the computer-use indicator strip: it renders while
// computer use is available and something is shared, and stays hidden when the
// backend refuses computer use (the port's default).

import (
	"encoding/json"
	"testing"

	"github.com/egoist/mygo/ui"
)

// computerShellForTest seeds a Shell whose backend advertises computer use and,
// when shared is non-empty, a shared state payload.
func computerShellForTest(t *testing.T, available bool, shared string) *Shell {
	t.Helper()
	s := New(newBackendForTest(t))
	s.stream = newStreamForTest()
	s.cache["appearance-mode"] = json.RawMessage(`{"mode":"light"}`)
	if available {
		s.cache["computer-available"] = json.RawMessage(`{"available":true,"platform":"windows"}`)
	} else {
		s.cache["computer-available"] = json.RawMessage(`{"available":false,"platform":"windows"}`)
	}
	if shared != "" {
		s.cache["computer-shared"] = json.RawMessage(shared)
	}
	return s
}

func TestComputerIndicatorHiddenWhenUnavailable(t *testing.T) {
	s := computerShellForTest(t, false, `{"shared":[{"level":"control","appName":"X"}]}`)
	tt := ui.NewTester(s.View, 1240, 820)
	if tt.HasText("Stop") {
		t.Errorf("strip should be hidden when computer use is unavailable; texts = %v", tt.Texts())
	}
}

func TestComputerIndicatorShowsWhileShared(t *testing.T) {
	s := computerShellForTest(t, true, `{"shared":[{"level":"control","appName":"X"},{"level":"read","appName":"Y"}]}`)
	tt := ui.NewTester(s.View, 1240, 820)
	for _, want := range []string{"Agents can control 2 window(s)", "Stop"} {
		if !tt.HasText(want) {
			t.Errorf("strip missing %q; texts = %v", want, tt.Texts())
		}
	}
}

func TestComputerIndicatorHiddenWhenNothingShared(t *testing.T) {
	s := computerShellForTest(t, true, `{"shared":[]}`)
	tt := ui.NewTester(s.View, 1240, 820)
	if tt.HasText("Stop") {
		t.Errorf("strip should be hidden with nothing shared; texts = %v", tt.Texts())
	}
}
