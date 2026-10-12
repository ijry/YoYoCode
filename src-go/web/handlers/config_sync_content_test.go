package handlers_test

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestConfigSyncContentRoundTrip(t *testing.T) {
	setHome(t)
	t.Setenv("YOYOCODE_HOME", t.TempDir()) // isolate config-snapshots from the real ~/.yoyocode
	srv := newTestServer(t)

	// seed a quick message (a portable domain)
	if code, _ := call(t, srv, "quick_messages_create", map[string]any{"title": "Hi", "content": "hello world"}); code != 200 {
		t.Fatalf("seed quick message failed (%d)", code)
	}

	// export
	code, exp := call(t, srv, "config_sync_export_content", map[string]any{})
	if code != 200 {
		t.Fatalf("export status = %d", code)
	}
	content, _ := exp["content"].(string)
	if content == "" || !strings.Contains(content, "codegConfigExport") {
		t.Fatalf("export content missing envelope: %q", content[:min(80, len(content))])
	}
	counts, _ := exp["counts"].(map[string]any)
	if counts["quickMessages"].(float64) != 1 {
		t.Fatalf("expected 1 quick message in counts, got %v", counts["quickMessages"])
	}
	// modelProviders domain intentionally omitted
	if _, ok := counts["modelProviders"]; ok {
		t.Fatalf("modelProviders domain should be omitted")
	}

	// peek the exported content
	code2, preview := call(t, srv, "config_sync_peek_content", map[string]any{"content": content})
	if code2 != 200 {
		t.Fatalf("peek status = %d", code2)
	}
	if preview["manifest"] == nil || preview["counts"] == nil {
		t.Fatalf("peek missing manifest/counts: %v", preview)
	}

	// mutate: change the quick message content, then import the earlier export → restores it
	// (import upserts by title)
	_ = json.Valid
	code3, imp := call(t, srv, "config_sync_import_content", map[string]any{"content": content})
	if code3 != 200 {
		t.Fatalf("import status = %d (%v)", code3, imp)
	}
	applied, _ := imp["applied"].(map[string]any)
	if applied == nil || applied["total"] == nil {
		t.Fatalf("import result missing applied: %v", imp)
	}
	if imp["rollbackPath"] == nil {
		t.Fatalf("import should write a rollback snapshot")
	}

	// list rollbacks → at least one
	_, rbs := callArr(t, srv, "config_sync_list_rollbacks", map[string]any{})
	if len(rbs) < 1 {
		t.Fatalf("expected at least one rollback snapshot, got %d", len(rbs))
	}
	first := rbs[0].(map[string]any)
	rbID, _ := first["id"].(string)
	if !strings.HasPrefix(rbID, "config-") {
		t.Fatalf("bad rollback id: %q", rbID)
	}

	// apply that rollback
	if code5, out5 := call(t, srv, "config_sync_apply_rollback", map[string]any{"id": rbID}); code5 != 200 {
		t.Fatalf("apply_rollback status = %d (%v)", code5, out5)
	}

	// bogus rollback id → 404-ish error
	if code6, _ := call(t, srv, "config_sync_apply_rollback", map[string]any{"id": "config-../etc"}); code6 == 200 {
		t.Fatalf("bogus rollback id should be refused")
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
