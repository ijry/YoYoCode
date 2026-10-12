package handlers_test

import (
	"os"
	"path/filepath"
	"testing"
)

func TestScanImportableSessions(t *testing.T) {
	home := setHome(t)
	claudeDir := filepath.Join(home, ".claude-test")
	t.Setenv("CLAUDE_CONFIG_DIR", claudeDir)
	srv := newTestServer(t)

	// seed a claude transcript under <claude>/projects/<slug>/<sess>.jsonl
	projDir := filepath.Join(claudeDir, "projects", "-home-u-proj")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}
	lines := `{"type":"user","sessionId":"sess-abc","cwd":"/home/u/proj","gitBranch":"main","timestamp":"2026-01-01T10:00:00Z","message":{"role":"user","content":"Hello world task"}}
{"type":"assistant","sessionId":"sess-abc","timestamp":"2026-01-01T10:01:00Z","message":{"role":"assistant","model":"claude-sonnet-4","content":[{"type":"text","text":"hi"}]}}
`
	if err := os.WriteFile(filepath.Join(projDir, "sess-abc.jsonl"), []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}

	code, out := call(t, srv, "scan_importable_sessions", map[string]any{})
	if code != 200 {
		t.Fatalf("scan status = %d (%v)", code, out)
	}
	if out["total_sessions"].(float64) != 1 || out["importable_count"].(float64) != 1 || out["no_folder_count"].(float64) != 0 {
		t.Fatalf("scan counts wrong: %v", out)
	}
	folders, _ := out["folders"].([]any)
	if len(folders) != 1 {
		t.Fatalf("expected 1 folder group, got %d", len(folders))
	}
	f0 := folders[0].(map[string]any)
	if f0["path"] != "/home/u/proj" || f0["name"] != "proj" || f0["exists_in_codeg"] != false {
		t.Fatalf("folder group wrong: %v", f0)
	}
	sessions, _ := f0["sessions"].([]any)
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}
	s0 := sessions[0].(map[string]any)
	if s0["external_id"] != "sess-abc" || s0["agent_type"] != "claude_code" || s0["status"] != "new" {
		t.Fatalf("session wrong: %v", s0)
	}
	if s0["model"] != "claude-sonnet-4" || s0["git_branch"] != "main" || s0["message_count"].(float64) != 2 {
		t.Fatalf("session fields wrong: %v", s0)
	}
	if s0["title"] != "Hello world task" {
		t.Fatalf("title wrong: %v", s0["title"])
	}

}
