package handlers_test

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOfficeToolsDetectAndSkills(t *testing.T) {
	home := setHome(t)
	srv := newTestServer(t)

	_, info := call(t, srv, "officecli_detect", map[string]any{})
	if info["installed"] != false {
		t.Fatalf("officecli should be not-installed in this build: %v", info)
	}
	for _, k := range []string{"installed", "version", "path", "runtime_error"} {
		if _, ok := info[k]; !ok {
			t.Fatalf("detect missing key %s: %v", k, info)
		}
	}

	for _, cmd := range []string{"officecli_list_skills", "officecli_skill_list_all_install_statuses"} {
		_, arr := callArr(t, srv, cmd, map[string]any{})
		if len(arr) != 0 {
			t.Fatalf("%s should be empty, got %v", cmd, arr)
		}
	}

	_, sync := call(t, srv, "officecli_sync_skills", map[string]any{})
	if sync["synced"].(float64) != 0 {
		t.Fatalf("sync should report 0 synced: %v", sync)
	}

	// read_content against a central skill
	skillDir := filepath.Join(home, ".yoyocode", "skills", "office-skill")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: O\n---\nx\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, content := rawPostJSON(t, srv, "officecli_skill_read_content", map[string]any{"skillId": "office-skill"})
	if code != 200 || content == "" || content == "null" {
		t.Fatalf("read_content failed: %d %q", code, content)
	}
}
