package parsers

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadClaudeTurns(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", home)
	proj := filepath.Join(home, "projects", "-Users-test-proj")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	// A user record (string content), an assistant record (text + tool_use
	// blocks), a summary record (ignored), and a pure tool_result user echo
	// (no renderable text → skipped).
	lines := []string{
		`{"type":"user","uuid":"u1","timestamp":"2026-10-11T00:00:00Z","message":{"role":"user","content":"hello claude"}}`,
		`{"type":"assistant","uuid":"a1","timestamp":"2026-10-11T00:00:01Z","message":{"role":"assistant","content":[{"type":"text","text":"hi there"},{"type":"tool_use","id":"t1","name":"Read","input":{"path":"x.go"}}]}}`,
		`{"type":"summary","summary":"ignore me"}`,
		`{"type":"user","uuid":"u2","timestamp":"2026-10-11T00:00:02Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"ok"}]}}`,
	}
	sid := "sess-abc"
	if err := os.WriteFile(filepath.Join(proj, sid+".jsonl"), []byte(join(lines)), 0o644); err != nil {
		t.Fatal(err)
	}

	turns := ReadClaudeTurns(sid)
	if len(turns) != 2 {
		t.Fatalf("want 2 turns (user + assistant; summary & tool_result skipped), got %d: %+v", len(turns), turns)
	}
	if turns[0].Role != "user" || !turns[0].HasTS {
		t.Fatalf("turn0 = %+v", turns[0])
	}
	b0, _ := turns[0].Raw["blocks"].([]map[string]any)
	if len(b0) != 1 || b0[0]["type"] != "text" || b0[0]["text"] != "hello claude" {
		t.Fatalf("turn0 blocks = %v", turns[0].Raw["blocks"])
	}
	b1, _ := turns[1].Raw["blocks"].([]map[string]any)
	if len(b1) != 2 || b1[0]["text"] != "hi there" || b1[1]["type"] != "tool_use" || b1[1]["tool_name"] != "Read" {
		t.Fatalf("turn1 blocks = %v", turns[1].Raw["blocks"])
	}
}

func TestReadClaudeTurnsMissing(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	if turns := ReadClaudeTurns("nope"); turns != nil {
		t.Fatalf("missing session should be nil, got %v", turns)
	}
}

func TestIsSafeSessionID(t *testing.T) {
	for _, bad := range []string{"", "..", "a/b", "a\\b", "a..b", "c:evil", "x\x00y"} {
		if isSafeSessionID(bad) {
			t.Fatalf("%q should be unsafe", bad)
		}
	}
	if !isSafeSessionID("sess-abc-123") {
		t.Fatal("normal id should be safe")
	}
}

func join(lines []string) string {
	s := ""
	for _, l := range lines {
		s += l + "\n"
	}
	return s
}
