package handlers_test

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExpertsEmptyCatalog(t *testing.T) {
	srv := newTestServer(t)
	for _, cmd := range []string{"experts_list", "experts_list_all_install_statuses"} {
		_, arr := callArr(t, srv, cmd, map[string]any{})
		if len(arr) != 0 {
			t.Fatalf("%s should be empty (no bundled packs), got %v", cmd, arr)
		}
	}
	_, st := callArr(t, srv, "experts_get_install_status", map[string]any{"expertId": "anything"})
	if len(st) != 0 {
		t.Fatalf("install status should be empty, got %v", st)
	}
}

func TestExpertsReadAndCentralDir(t *testing.T) {
	home := setHome(t)
	srv := newTestServer(t)

	// open_central_dir creates and returns ~/.yoyocode/skills
	code, dir := rawPostJSON(t, srv, "experts_open_central_dir", map[string]any{})
	if code != 200 {
		t.Fatalf("open_central_dir failed: %d", code)
	}
	want := filepath.Join(home, ".yoyocode", "skills")
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("central dir not created: %v", err)
	}
	_ = dir

	// read a central skill that we place on disk
	skillDir := filepath.Join(want, "my-expert")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: E\n---\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rc, content := rawPostJSON(t, srv, "experts_read_content", map[string]any{"expertId": "my-expert"})
	if rc != 200 {
		t.Fatalf("read_content failed: %d", rc)
	}
	if content == "" || content == "null" {
		t.Fatalf("read_content empty: %q", content)
	}

	// missing expert → error
	if c2, _ := call(t, srv, "experts_read_content", map[string]any{"expertId": "nope"}); c2 == 200 {
		t.Fatalf("reading a missing expert should fail")
	}
}
