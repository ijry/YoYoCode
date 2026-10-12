package handlers_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func seedClaudeSession(t *testing.T, claudeDir, slug, sess, cwd string) {
	t.Helper()
	projDir := filepath.Join(claudeDir, "projects", slug)
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}
	l1, _ := json.Marshal(map[string]any{
		"type": "user", "sessionId": sess, "cwd": cwd, "gitBranch": "main",
		"timestamp": "2026-02-02T10:00:00Z",
		"message":   map[string]any{"role": "user", "content": "Do the thing"},
	})
	l2, _ := json.Marshal(map[string]any{
		"type": "assistant", "sessionId": sess, "timestamp": "2026-02-02T10:05:00Z",
		"message": map[string]any{"role": "assistant", "model": "claude-sonnet-4", "content": []any{map[string]any{"type": "text", "text": "ok"}}},
	})
	body := string(l1) + "\n" + string(l2) + "\n"
	if err := os.WriteFile(filepath.Join(projDir, sess+".jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestImportSelectedAndLocal(t *testing.T) {
	home := setHome(t)
	claudeDir := filepath.Join(home, ".claude-test2")
	t.Setenv("CLAUDE_CONFIG_DIR", claudeDir)
	srv := newTestServer(t)

	// use a real OS-absolute cwd so folder-path reconcile matches after import
	cwd := t.TempDir()
	seedClaudeSession(t, claudeDir, "proj-slug", "sess-xyz", cwd)

	code, res := call(t, srv, "import_selected_sessions", map[string]any{
		"selections": []any{map[string]any{"agentType": "claude_code", "externalId": "sess-xyz"}},
	})
	if code != 200 {
		t.Fatalf("import_selected status = %d (%v)", code, res)
	}
	if res["imported"].(float64) != 1 || res["created_folders"].(float64) != 1 {
		t.Fatalf("import result wrong: %v", res)
	}

	_, scan := call(t, srv, "scan_importable_sessions", map[string]any{})
	folders, _ := scan["folders"].([]any)
	if len(folders) != 1 {
		t.Fatalf("expected 1 folder, got %d", len(folders))
	}
	f0 := folders[0].(map[string]any)
	if f0["exists_in_codeg"] != true || f0["folder_id"] == nil {
		t.Fatalf("folder should now exist in YoYoCode: %v", f0)
	}
	s0 := f0["sessions"].([]any)[0].(map[string]any)
	if s0["status"] != "imported" {
		t.Fatalf("session should be imported now, got %v", s0["status"])
	}

	_, res2 := call(t, srv, "import_selected_sessions", map[string]any{
		"selections": []any{map[string]any{"agentType": "claude_code", "externalId": "sess-xyz"}},
	})
	if res2["imported"].(float64) != 0 || res2["skipped"].(float64) != 1 {
		t.Fatalf("re-import should skip: %v", res2)
	}

	fid := int(f0["folder_id"].(float64))
	code3, res3 := call(t, srv, "import_local_conversations", map[string]any{"folderId": fid})
	if code3 != 200 {
		t.Fatalf("import_local status = %d (%v)", code3, res3)
	}
	if res3["imported"].(float64) != 0 {
		t.Fatalf("import_local into existing folder should not re-insert: %v", res3)
	}

	_, res4 := call(t, srv, "import_selected_sessions", map[string]any{
		"selections": []any{map[string]any{"agentType": "claude_code", "externalId": "nope"}},
	})
	if res4["not_found"].(float64) != 1 {
		t.Fatalf("bogus selection should be not_found: %v", res4)
	}
}
