package app

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	appstate "yoyocode/src-go/app_state"
	"yoyocode/src-go/db"
	"yoyocode/src-go/web"
	"yoyocode/src-go/web/handlers"
)

// newBackendForTest boots the real upstream stack behind an httptest server and
// returns a Backend pointed at it — the same loopback contract the native app
// uses, so the UI data layer is exercised end-to-end.
func newBackendForTest(t *testing.T) *Backend {
	t.Helper()
	database, err := db.Open(t.TempDir())
	if err != nil {
		t.Fatalf("db: %v", err)
	}
	t.Cleanup(func() { database.Conn.Close() })
	st := appstate.Init(database, t.TempDir())
	r := web.NewRouter(st)
	r.SetToken("tok")
	handlers.RegisterAll(handlers.NewRegistry(r))
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return &Backend{URL: srv.URL, Token: "tok"}
}

func TestBackendConversationLifecycle(t *testing.T) {
	b := newBackendForTest(t)

	// Open a folder.
	raw, err := b.raw("add_folder_to_history", map[string]any{"path": t.TempDir()})
	if err != nil {
		t.Fatalf("add_folder: %v", err)
	}
	var folder map[string]any
	_ = json.Unmarshal(raw, &folder)
	folderID := folder["id"]

	// Create a conversation. create_conversation mirrors upstream and returns the
	// new id as a bare number (Json<i32>), not an object.
	raw, err = b.raw("create_conversation", map[string]any{"folderId": folderID, "agentType": "claude_code"})
	if err != nil {
		t.Fatalf("create_conversation: %v", err)
	}
	var convID float64
	if err := json.Unmarshal(raw, &convID); err != nil {
		t.Fatalf("create_conversation not a bare id: %v", err)
	}
	if convID == 0 {
		t.Fatalf("no conversation id: %v", raw)
	}

	// Rename it.
	if _, err := b.raw("update_conversation_title", map[string]any{"conversationId": convID, "title": "Renamed"}); err != nil {
		t.Fatalf("rename: %v", err)
	}
	raw, _ = b.raw("list_conversations", map[string]any{"folderId": folderID})
	convs := asList(raw)
	if len(convs) != 1 || str(convs[0], "title") != "Renamed" {
		t.Fatalf("rename not reflected: %v", convs)
	}

	// Pin / unpin (no error = accepted).
	if _, err := b.raw("update_conversation_pinned", map[string]any{"conversationId": convID, "pinned": true}); err != nil {
		t.Fatalf("pin: %v", err)
	}

	// Delete it → the folder has no conversations left.
	if _, err := b.raw("delete_conversation", map[string]any{"conversationId": convID}); err != nil {
		t.Fatalf("delete: %v", err)
	}
	raw, _ = b.raw("list_conversations", map[string]any{"folderId": folderID})
	if got := asList(raw); len(got) != 0 {
		t.Fatalf("expected empty after delete, got %v", got)
	}
}
