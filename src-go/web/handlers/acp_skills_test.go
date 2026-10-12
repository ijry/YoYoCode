package handlers_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

// Project-scoped round trip for claude_code (SkillDirectoryOnly): save creates
// <ws>/.claude/skills/<id>/SKILL.md, list surfaces it, read returns content,
// delete removes it.
func TestAgentSkillProjectRoundTrip(t *testing.T) {
	srv := newTestServer(t)
	ws := t.TempDir()

	// save
	_, saved := call(t, srv, "acp_save_agent_skill", map[string]any{
		"agentType":     "claude_code",
		"scope":         "project",
		"skillId":       "my-skill",
		"workspacePath": ws,
		"content":       "---\nname: My Skill\ndescription: does a thing\n---\nbody\n",
	})
	if saved["layout"] != "skill_directory" {
		t.Fatalf("expected skill_directory layout, got %v", saved["layout"])
	}
	if saved["description"] != "does a thing" {
		t.Fatalf("expected parsed description, got %v", saved["description"])
	}
	wantPath := filepath.Join(ws, ".claude", "skills", "my-skill")
	if saved["path"] != wantPath {
		t.Fatalf("path = %v, want %v", saved["path"], wantPath)
	}

	// list
	_, listed := call(t, srv, "acp_list_agent_skills", map[string]any{
		"agentType":     "claude_code",
		"workspacePath": ws,
	})
	if listed["supported"] != true {
		t.Fatalf("claude_code should be supported: %v", listed)
	}
	skills, _ := listed["skills"].([]any)
	found := false
	for _, s := range skills {
		m := s.(map[string]any)
		if m["id"] == "my-skill" && m["scope"] == "project" {
			found = true
		}
	}
	if !found {
		t.Fatalf("saved skill not in list: %v", skills)
	}

	// read
	_, read := call(t, srv, "acp_read_agent_skill", map[string]any{
		"agentType":     "claude_code",
		"scope":         "project",
		"skillId":       "my-skill",
		"workspacePath": ws,
	})
	content, _ := read["content"].(string)
	if !strings.Contains(content, "name: My Skill") {
		t.Fatalf("read content wrong: %q", content)
	}

	// delete
	code, _ := call(t, srv, "acp_delete_agent_skill", map[string]any{
		"agentType":     "claude_code",
		"scope":         "project",
		"skillId":       "my-skill",
		"workspacePath": ws,
	})
	if code != 200 {
		t.Fatalf("delete status = %d", code)
	}
	// gone from list
	_, after := call(t, srv, "acp_list_agent_skills", map[string]any{
		"agentType":     "claude_code",
		"workspacePath": ws,
	})
	for _, s := range after["skills"].([]any) {
		if s.(map[string]any)["id"] == "my-skill" {
			t.Fatalf("skill should be deleted: %v", after["skills"])
		}
	}
}

func TestAgentSkillUnsupportedAgent(t *testing.T) {
	srv := newTestServer(t)
	_, out := call(t, srv, "acp_list_agent_skills", map[string]any{"agentType": "not_a_real_agent"})
	if out["supported"] != false {
		t.Fatalf("unknown agent should be unsupported: %v", out)
	}
}

func TestAgentSkillInvalidID(t *testing.T) {
	srv := newTestServer(t)
	ws := t.TempDir()
	for _, bad := range []string{"../escape", "..", "a/b", ".hidden", ""} {
		code, out := call(t, srv, "acp_read_agent_skill", map[string]any{
			"agentType":     "claude_code",
			"scope":         "project",
			"skillId":       bad,
			"workspacePath": ws,
		})
		if code == 200 {
			t.Fatalf("id %q should be rejected, got %v", bad, out)
		}
	}
}

func TestAcpCurrentPlatform(t *testing.T) {
	srv := newTestServer(t)
	body := callPlatformString(t, srv)
	if !strings.Contains(body, "-") {
		t.Fatalf("platform token should be <os>-<arch>, got %q", body)
	}
	for _, osn := range []string{"darwin", "linux", "windows"} {
		if strings.HasPrefix(body, osn+"-") {
			return
		}
	}
	t.Fatalf("unexpected platform os: %q", body)
}

func callPlatformString(t *testing.T, srv *httptest.Server) string {
	t.Helper()
	payload, _ := json.Marshal(map[string]any{})
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/acp_current_platform", bytes.NewReader(payload))
	req.Header.Set("content-type", "application/json")
	req.Header.Set("authorization", "Bearer tok")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("acp_current_platform: %v", err)
	}
	defer resp.Body.Close()
	var out string
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return out
}
