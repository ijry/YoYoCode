package app

// pet_panel_test.go covers the active-session panel: the empty state, the
// waiting > error > running ordering, the row labels, and the click-to-jump
// behaviour that selects a session's conversation in the workspace.

import (
	"encoding/json"
	"testing"

	"github.com/egoist/mygo/ui"
)

// petShellForTest parks the Shell on the Pet section with the sessions payload
// pre-seeded, so the panel renders without touching the network.
func petShellForTest(t *testing.T, payload string) *Shell {
	t.Helper()
	s := New(newBackendForTest(t))
	s.stream = newStreamForTest()
	s.section = "pet"
	s.petTab = petTabSessions
	s.cache["appearance-mode"] = json.RawMessage(`{"mode":"light"}`)
	s.cache["pet-sessions"] = json.RawMessage(payload)
	return s
}

func TestPetPanelEmptyState(t *testing.T) {
	s := petShellForTest(t, `{"running_count":0,"waiting_count":0,"error_count":0,"sessions":[]}`)
	tt := ui.NewTester(s.View, 900, 600)
	for _, want := range []string{"Pet", "Active sessions", "No active sessions"} {
		if !tt.HasText(want) {
			t.Errorf("empty panel missing %q; texts = %v", want, tt.Texts())
		}
	}
}

// TestPetPanelRowsAndJump seeds a running session and a permission-blocked one,
// asserts both rows (with their status pills and the inline permission card)
// render, and that clicking a row jumps to its conversation in the workspace.
func TestPetPanelRowsAndJump(t *testing.T) {
	s := petShellForTest(t, `{
		"running_count":1,"waiting_count":1,"error_count":0,
		"sessions":[
			{"connectionId":"c-run","conversationId":2,"folderId":1,"agentType":"claude_code","title":"Running task","status":"running"},
			{"connectionId":"c-wait","conversationId":3,"folderId":1,"agentType":"codex","title":"Needs review","status":"running",
			 "pending":{"requestId":"r1","toolCall":{"label":"Run rm -rf"},"options":[{"optionId":"allow","name":"Allow"},{"optionId":"deny","name":"Deny"}]}}
		]}`)
	tt := ui.NewTester(s.View, 900, 600)
	for _, want := range []string{
		"Running task", "Needs review", // titles
		"waiting", "running", // status pills
		"Permission requested", "Allow", "Deny", // inline permission card
	} {
		if !tt.HasText(want) {
			t.Errorf("panel missing %q; texts = %v", want, tt.Texts())
		}
	}
	if err := tt.Click("Needs review"); err != nil {
		t.Fatalf("click session row: %v", err)
	}
	tt.Frame()
	if s.section != "workspace" || s.selectedFolder != "1" || s.selectedConv != "3" {
		t.Errorf("jump: section=%q folder=%q conv=%q, want workspace/1/3", s.section, s.selectedFolder, s.selectedConv)
	}
}

// TestPetSessionOrdering locks the waiting > error > running precedence used by
// the panel sort (and mirrored by the sprite badge upstream).
func TestPetSessionOrdering(t *testing.T) {
	sessions := []map[string]any{
		{"status": "running"},
		{"status": "error"},
		{"pending": map[string]any{"requestId": "r"}},
		{"status": "running"},
	}
	sortPetSessions(sessions)
	got := []string{
		petStatusKind(sessions[0]),
		petStatusKind(sessions[1]),
		petStatusKind(sessions[2]),
		petStatusKind(sessions[3]),
	}
	want := []string{"waiting", "error", "running", "running"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order = %v, want %v", got, want)
		}
	}
}
