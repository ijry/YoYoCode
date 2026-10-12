package handlers_test

import (
	"os"
	"path/filepath"
	"testing"
)

func TestScienceEmptyAndRead(t *testing.T) {
	home := setHome(t)
	srv := newTestServer(t)
	for _, cmd := range []string{"science_list", "science_list_all_install_statuses"} {
		_, arr := callArr(t, srv, cmd, map[string]any{})
		if len(arr) != 0 {
			t.Fatalf("%s should be empty, got %v", cmd, arr)
		}
	}
	// open_central_dir creates ~/.yoyocode/skills (shared store with experts/custom)
	if code, _ := call(t, srv, "science_open_central_dir", map[string]any{}); code != 200 {
		t.Fatalf("science_open_central_dir failed: %d", code)
	}
	// read a central skill
	skillDir := filepath.Join(home, ".yoyocode", "skills", "sci-skill")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: Sci\n---\nx\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, content := rawPostJSON(t, srv, "science_read_content", map[string]any{"scienceId": "sci-skill"})
	if code != 200 || content == "" || content == "null" {
		t.Fatalf("science_read_content failed: %d %q", code, content)
	}
	if c2, _ := call(t, srv, "science_read_content", map[string]any{"scienceId": "nope"}); c2 == 200 {
		t.Fatalf("missing science skill should fail")
	}
}
