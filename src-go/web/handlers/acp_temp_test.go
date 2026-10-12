package handlers_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAcpScanAndReclaimLeakedTemp(t *testing.T) {
	// Point TMP/TEMP at a fresh dir so the scan is deterministic.
	tmp := t.TempDir()
	t.Setenv("TMP", tmp)
	t.Setenv("TEMP", tmp)
	t.Setenv("TMPDIR", tmp)
	srv := newTestServer(t)

	// A reclaimable resource file, aged past the 15-minute floor.
	leaked := filepath.Join(tmp, "cacert__py_binary_resource_agy_acp_server__abcd.pem")
	if err := os.WriteFile(leaked, []byte("xxxxx"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-30 * time.Minute)
	_ = os.Chtimes(leaked, old, old)
	// An unrelated file that must be ignored.
	if err := os.WriteFile(filepath.Join(tmp, "unrelated.txt"), []byte("y"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A too-recent resource file → skipped.
	recent := filepath.Join(tmp, "cacert__py_binary_resource_agy_acp_server__ef01.pem")
	if err := os.WriteFile(recent, []byte("zz"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, scan := call(t, srv, "acp_scan_leaked_temp", map[string]any{})
	entries, _ := scan["entries"].([]any)
	if len(entries) != 1 {
		t.Fatalf("expected exactly 1 reclaimable entry, got %d: %v", len(entries), entries)
	}
	e0 := entries[0].(map[string]any)
	if e0["path"] != leaked {
		t.Fatalf("scan found wrong path: %v", e0["path"])
	}
	if sk, _ := scan["skipped"].(float64); sk < 1 {
		t.Fatalf("the recent file should be counted as skipped, got %v", scan["skipped"])
	}

	// Reclaim it.
	_, rec := call(t, srv, "acp_reclaim_leaked_temp", map[string]any{"paths": []any{leaked}})
	if rm, _ := rec["removed"].(float64); rm != 1 {
		t.Fatalf("expected 1 removed, got %v", rec["removed"])
	}
	if _, err := os.Stat(leaked); err == nil {
		t.Fatalf("leaked file should be gone")
	}

	// clear_binary_cache is a no-op success.
	if code, _ := call(t, srv, "acp_clear_binary_cache", map[string]any{"agentType": "cursor"}); code != 200 {
		t.Fatalf("clear_binary_cache should succeed, got %d", code)
	}
}

func TestAcpReclaimRejectsOutsideTemp(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMP", tmp)
	t.Setenv("TEMP", tmp)
	t.Setenv("TMPDIR", tmp)
	srv := newTestServer(t)
	// A path outside the temp root must never be deleted.
	outside := filepath.Join(t.TempDir(), "keep.pem")
	if err := os.WriteFile(outside, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, rec := call(t, srv, "acp_reclaim_leaked_temp", map[string]any{"paths": []any{outside}})
	if rm, _ := rec["removed"].(float64); rm != 0 {
		t.Fatalf("must not remove outside temp, got %v", rec["removed"])
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatalf("outside file must survive: %v", err)
	}
}
