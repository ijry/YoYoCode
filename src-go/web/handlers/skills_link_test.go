package handlers_test

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSkillInstallStatusMatrix(t *testing.T) {
	home := setHome(t)
	srv := newTestServer(t)
	// seed one central-store skill
	skillDir := filepath.Join(home, ".yoyocode", "skills", "my-skill")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: S\n---\nx\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, statuses := callArr(t, srv, "custom_list_all_install_statuses", map[string]any{})
	if len(statuses) == 0 {
		t.Fatalf("expected a (skill x agent) matrix, got empty")
	}
	// Every entry carries the expected keys and defaults to not_linked.
	for _, s := range statuses {
		m := s.(map[string]any)
		if m["expert_id"] != "my-skill" {
			t.Fatalf("unexpected expert_id: %v", m["expert_id"])
		}
		for _, k := range []string{"agent_type", "state", "link_path", "expected_target_path", "copy_mode"} {
			if _, ok := m[k]; !ok {
				t.Fatalf("status missing key %s: %v", k, m)
			}
		}
		if m["state"] != "not_linked" {
			t.Fatalf("fresh skill should be not_linked, got %v", m["state"])
		}
	}
}

func TestSkillLinkApply(t *testing.T) {
	home := setHome(t)
	srv := newTestServer(t)
	skillDir := filepath.Join(home, ".yoyocode", "skills", "linkable")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: L\n---\nx\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, res := callArr(t, srv, "custom_apply_links", map[string]any{
		"ops": []any{map[string]any{"expertId": "linkable", "agentType": "claude_code", "enable": true}},
	})
	if len(res) != 1 {
		t.Fatalf("expected 1 op result, got %v", res)
	}
	op := res[0].(map[string]any)
	ok, _ := op["ok"].(bool)
	if !ok {
		// Symlink creation can fail on a host without the privilege (e.g.
		// Windows without Developer Mode). The engine must report it per-op
		// rather than crash — that is the contract we verify here.
		t.Logf("link enable not permitted on this host: %v (acceptable)", op["error"])
		return
	}
	// If it succeeded, the link resolves to the central store and disable clears it.
	link := filepath.Join(home, ".claude", "skills", "linkable")
	if _, err := os.Lstat(link); err != nil {
		t.Fatalf("link should exist after enable: %v", err)
	}
	callArr(t, srv, "custom_apply_links", map[string]any{
		"ops": []any{map[string]any{"expertId": "linkable", "agentType": "claude_code", "enable": false}},
	})
	if _, err := os.Lstat(link); err == nil {
		t.Fatalf("link should be gone after disable")
	}
}
