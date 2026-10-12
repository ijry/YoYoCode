package handlers_test

import (
	"os"
	"testing"
)

// TestCreateChatDirAndConversation covers the no-folder chat-mode path:
// create_chat_dir mints a real scratch dir with no DB rows, and
// create_chat_conversation mints a hidden "Chat" folder + chat conversation,
// reusing an existing scratch dir when asked.
func TestCreateChatDirAndConversation(t *testing.T) {
	srv := newTestServer(t)

	// create_chat_dir -> {path}; the dir exists and no conversation rows yet.
	status, dirRes := call(t, srv, "create_chat_dir", map[string]any{})
	if status != 200 {
		t.Fatalf("create_chat_dir status %d: %v", status, dirRes)
	}
	path, _ := dirRes["path"].(string)
	if path == "" {
		t.Fatalf("create_chat_dir returned empty path: %v", dirRes)
	}
	if info, err := os.Stat(path); err != nil || !info.IsDir() {
		t.Fatalf("scratch dir not created: %v (%v)", path, err)
	}

	// create_chat_conversation reusing the scratch dir -> folder.path == path.
	status, res := call(t, srv, "create_chat_conversation", map[string]any{
		"agentType":   "codex",
		"title":       "Hello chat",
		"existingDir": path,
	})
	if status != 200 {
		t.Fatalf("create_chat_conversation status %d: %v", status, res)
	}
	convID, _ := res["conversationId"].(float64)
	folderID, _ := res["folderId"].(float64)
	if convID <= 0 || folderID <= 0 {
		t.Fatalf("bad ids: conv=%v folder=%v (%v)", res["conversationId"], res["folderId"], res)
	}
	folder, ok := res["folder"].(map[string]any)
	if !ok {
		t.Fatalf("missing folder detail: %v", res)
	}
	if folder["name"] != "Chat" || folder["kind"] != "chat" {
		t.Fatalf("chat folder shape: %v", folder)
	}
	if folder["path"] != path {
		t.Fatalf("folder path = %v, want reused scratch dir %v", folder["path"], path)
	}
	// FolderDetail must carry the full snake_case field set.
	for _, k := range []string{"id", "name", "path", "git_branch", "default_agent_type", "last_opened_at", "sort_order", "color", "parent_id", "kind", "alias", "group_id"} {
		if _, present := folder[k]; !present {
			t.Fatalf("folder detail missing %q: %v", k, folder)
		}
	}

	// The conversation row is readable back.
	status, conv := call(t, srv, "get_conversation", map[string]any{"conversationId": convID})
	if status != 200 || conv["id"].(float64) != convID {
		t.Fatalf("get_conversation status %d: %v", status, conv)
	}
	if conv["title"] != "Hello chat" || conv["agent_type"] != "codex" {
		t.Fatalf("conversation fields: %v", conv)
	}

	// Hidden chat folders must NOT appear in list_folders aggregation... they
	// do appear once a conversation references them (upstream groups all
	// non-deleted conversations). Just assert the chat conversation is counted
	// in get_stats.
	status, stats := call(t, srv, "get_stats", map[string]any{})
	if status != 200 {
		t.Fatalf("get_stats status %d", status)
	}
	if tc, _ := stats["total_conversations"].(float64); tc != 1 {
		t.Fatalf("total_conversations = %v, want 1", stats["total_conversations"])
	}

	// A fresh create (no existingDir) mints its own dir + rows.
	status, res2 := call(t, srv, "create_chat_conversation", map[string]any{"agentType": "claude_code"})
	if status != 200 {
		t.Fatalf("create_chat_conversation (fresh) status %d: %v", status, res2)
	}
	f2, _ := res2["folder"].(map[string]any)
	if f2["path"] == path {
		t.Fatalf("fresh create should mint a new dir, got reused %v", f2["path"])
	}
}
