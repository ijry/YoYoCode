package handlers_test

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCustomImportFromAgent(t *testing.T) {
	home := setHome(t)
	srv := newTestServer(t)

	// seed a global claude_code skill: ~/.claude/skills/my-skill/SKILL.md
	skillDir := filepath.Join(home, ".claude", "skills", "my-skill")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	md := "---\nname: My Skill\ndescription: does things\n---\nbody\n"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(md), 0o644); err != nil {
		t.Fatal(err)
	}

	// import it + a bogus id
	_, arr := callArr(t, srv, "custom_import_from_agent", map[string]any{
		"agentType": "claude_code",
		"ids":       []any{"my-skill", "no-such-skill"},
	})
	if len(arr) != 2 {
		t.Fatalf("expected 2 results, got %v", arr)
	}
	r0 := arr[0].(map[string]any)
	if r0["id"] != "my-skill" || r0["ok"] != true || r0["skipped"] != false {
		t.Fatalf("import result wrong: %v", r0)
	}
	r1 := arr[1].(map[string]any)
	if r1["ok"] != false || r1["skipped"] != false || r1["error"] == nil {
		t.Fatalf("missing skill should error: %v", r1)
	}

	// it now exists in the central store
	central := filepath.Join(home, ".yoyocode", "skills", "my-skill", "SKILL.md")
	if _, err := os.Stat(central); err != nil {
		t.Fatalf("imported skill not in central store: %v", err)
	}

	// re-import → idempotent skip
	_, arr2 := callArr(t, srv, "custom_import_from_agent", map[string]any{
		"agentType": "claude_code",
		"ids":       []any{"my-skill"},
	})
	if arr2[0].(map[string]any)["skipped"] != true {
		t.Fatalf("re-import should skip, got %v", arr2[0])
	}

	// unsupported agent → error status
	if code, _ := call(t, srv, "custom_import_from_agent", map[string]any{"agentType": "not-an-agent", "ids": []any{"x"}}); code == 200 {
		t.Fatalf("unsupported agent should fail")
	}
}
