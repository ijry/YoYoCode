package handlers_test

import (
	"testing"
)

// TestSidebarDataAggregation verifies the upstream-shaped wire for
// get_stats (AgentStats), list_folders (FolderInfo[]) and get_sidebar_data
// (SidebarData) against a seeded DB.
func TestSidebarDataAggregation(t *testing.T) {
	srv := newTestServer(t)

	// Seed a folder, then three conversations: 2 claude_code + 1 codex.
	status, folder := call(t, srv, "add_folder_to_history", map[string]any{"path": t.TempDir()})
	if status != 200 {
		t.Fatalf("add_folder status %d: %v", status, folder)
	}
	folderID := folder["id"]
	folderName, _ := folder["name"].(string)
	folderPath, _ := folder["path"].(string)
	if folderPath == "" {
		t.Fatalf("folder path missing: %v", folder)
	}

	for _, at := range []string{"claude_code", "claude_code", "codex"} {
		status, _ = call(t, srv, "create_conversation", map[string]any{"folderId": folderID, "agentType": at})
		if status != 200 {
			t.Fatalf("create_conversation(%s) status %d", at, status)
		}
	}

	// get_stats -> AgentStats {total_conversations, total_messages, by_agent}
	status, stats := call(t, srv, "get_stats", map[string]any{})
	if status != 200 {
		t.Fatalf("get_stats status %d: %v", status, stats)
	}
	if tc, _ := stats["total_conversations"].(float64); tc != 3 {
		t.Fatalf("total_conversations = %v, want 3 (%v)", stats["total_conversations"], stats)
	}
	if _, ok := stats["total_messages"]; !ok {
		t.Fatalf("missing total_messages: %v", stats)
	}
	byAgent, ok := stats["by_agent"].([]any)
	if !ok || len(byAgent) != 2 {
		t.Fatalf("by_agent = %v, want 2 entries", stats["by_agent"])
	}
	first, _ := byAgent[0].(map[string]any)
	if first["agent_type"] != "claude_code" || first["conversation_count"].(float64) != 2 {
		t.Fatalf("by_agent[0] = %v, want claude_code x2 (desc)", first)
	}

	// list_folders -> FolderInfo[] {path, name, agent_types, conversation_count}
	status, folders := callArr(t, srv, "list_folders", map[string]any{})
	if status != 200 || len(folders) != 1 {
		t.Fatalf("list_folders status %d len %d: %v", status, len(folders), folders)
	}
	fi, _ := folders[0].(map[string]any)
	if fi["path"] != folderPath || fi["name"] != folderName {
		t.Fatalf("folder info path/name mismatch: %v (want %s / %s)", fi, folderPath, folderName)
	}
	if cc, _ := fi["conversation_count"].(float64); cc != 3 {
		t.Fatalf("conversation_count = %v, want 3", fi["conversation_count"])
	}
	ats, ok := fi["agent_types"].([]any)
	if !ok || len(ats) != 2 {
		t.Fatalf("agent_types = %v, want 2 distinct", fi["agent_types"])
	}

	// get_sidebar_data -> {folders, stats}
	status, sidebar := call(t, srv, "get_sidebar_data", map[string]any{})
	if status != 200 {
		t.Fatalf("get_sidebar_data status %d: %v", status, sidebar)
	}
	sf, ok := sidebar["folders"].([]any)
	if !ok || len(sf) != 1 {
		t.Fatalf("sidebar folders = %v", sidebar["folders"])
	}
	ss, ok := sidebar["stats"].(map[string]any)
	if !ok {
		t.Fatalf("sidebar stats missing: %v", sidebar)
	}
	if tc, _ := ss["total_conversations"].(float64); tc != 3 {
		t.Fatalf("sidebar stats total_conversations = %v, want 3", ss["total_conversations"])
	}
}
