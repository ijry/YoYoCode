package handlers_test

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBackupActiveAgents(t *testing.T) {
	srv := newTestServer(t)
	_, out := callArr(t, srv, "backup_active_agents", map[string]any{})
	if len(out) != 0 {
		t.Fatalf("headless build should report no active agents, got %v", out)
	}
}

func TestBackupListSafetySnapshots(t *testing.T) {
	srv, st := newTestServerState(t)
	// empty when no safety dir
	_, empty := callArr(t, srv, "backup_list_safety_snapshots", map[string]any{})
	if len(empty) != 0 {
		t.Fatalf("expected no snapshots, got %v", empty)
	}
	// create two snapshot dirs; one with a valid manifest (rollback_supported).
	root := filepath.Join(st.DataDir, ".yoyocode-restore-backup")
	a := filepath.Join(root, "snap-a")
	b := filepath.Join(root, "snap-b")
	_ = os.MkdirAll(a, 0o755)
	_ = os.MkdirAll(b, 0o755)
	if err := os.WriteFile(filepath.Join(a, "data.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(b, ".yoyocode-snapshot.json"), []byte(`{"v":1,"layout":"staging","created_at":"2026-10-12T00:00:00Z"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	_, list := callArr(t, srv, "backup_list_safety_snapshots", map[string]any{})
	if len(list) != 2 {
		t.Fatalf("expected 2 snapshots, got %d: %v", len(list), list)
	}
	byID := map[string]map[string]any{}
	for _, s := range list {
		m := s.(map[string]any)
		byID[m["id"].(string)] = m
	}
	if byID["snap-b"]["rollback_supported"] != true || byID["snap-b"]["created_at"] != "2026-10-12T00:00:00Z" {
		t.Fatalf("snap-b manifest not read: %v", byID["snap-b"])
	}
	if byID["snap-a"]["rollback_supported"] != false {
		t.Fatalf("snap-a without manifest should not support rollback: %v", byID["snap-a"])
	}
	if szb, _ := byID["snap-a"]["size_bytes"].(float64); szb < 5 {
		t.Fatalf("snap-a size should count data.txt, got %v", byID["snap-a"]["size_bytes"])
	}
}
