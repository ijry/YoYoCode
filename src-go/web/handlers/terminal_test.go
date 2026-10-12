package handlers_test

// Terminal PTY coverage (B6): spawn an interactive shell, write a command and
// poll the snapshot for its output, then kill. PTY availability varies by
// environment (ConPTY on Windows, unix98 pty elsewhere); if spawn is not
// supported here the test skips rather than failing the suite.

import (
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestTerminalLifecycle(t *testing.T) {
	srv := newTestServer(t)

	// empty list to start.
	_, list := callArr(t, srv, "terminal_list", map[string]any{})
	if len(list) != 0 {
		t.Fatalf("terminal_list should start empty, got %d", len(list))
	}

	dir := t.TempDir()
	code, raw := rawPostJSON(t, srv, "terminal_spawn", map[string]any{"workingDir": dir})
	if code != 200 {
		t.Skipf("terminal_spawn unsupported in this environment: %d %s", code, raw)
	}
	id := strings.Trim(strings.TrimSpace(raw), "\"")
	if id == "" {
		t.Fatalf("spawn returned empty id: %q", raw)
	}

	// it shows up in the list.
	_, list = callArr(t, srv, "terminal_list", map[string]any{})
	if len(list) != 1 {
		t.Fatalf("terminal_list after spawn = %d", len(list))
	}

	// resize is accepted.
	if code, _ := call(t, srv, "terminal_resize", map[string]any{"terminalId": id, "cols": 100, "rows": 30}); code != 200 {
		t.Fatalf("terminal_resize status %d", code)
	}

	// write a command whose output we can recognize.
	marker := "YOYO_TERM_MARK_42"
	nl := "\n"
	if runtime.GOOS == "windows" {
		nl = "\r\n"
	}
	if code, _ := call(t, srv, "terminal_write", map[string]any{"terminalId": id, "data": "echo " + marker + nl}); code != 200 {
		t.Fatalf("terminal_write status %d", code)
	}

	// poll the snapshot until the marker shows (PTY output is async).
	found := false
	for i := 0; i < 50; i++ {
		_, snap := call(t, srv, "terminal_snapshot", map[string]any{"terminalId": id})
		if data, _ := snap["data"].(string); strings.Contains(data, marker) {
			found = true
			if alive, _ := snap["alive"].(bool); !alive {
				t.Fatalf("snapshot alive=false while terminal running")
			}
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !found {
		t.Fatalf("marker %q never appeared in terminal output", marker)
	}

	// kill it.
	if code, _ := call(t, srv, "terminal_kill", map[string]any{"terminalId": id}); code != 200 {
		t.Fatalf("terminal_kill status %d", code)
	}

	// after the child is reaped the snapshot reports not-alive.
	gone := false
	for i := 0; i < 50; i++ {
		_, snap := call(t, srv, "terminal_snapshot", map[string]any{"terminalId": id})
		if alive, _ := snap["alive"].(bool); !alive {
			gone = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !gone {
		t.Fatalf("terminal still alive after kill")
	}
}
