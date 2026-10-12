package handlers_test

// get_folder_conversation_turns page-shape coverage. A DB conversation with no
// persisted transcript returns an empty, well-formed page (matching upstream);
// the prefix fingerprint of an empty prefix is the FNV-1a offset basis.

import "testing"

func TestConversationTurnsPage(t *testing.T) {
	srv := newTestServer(t)
	proj := t.TempDir()
	_, det := call(t, srv, "open_folder", map[string]any{"path": proj})
	fid := det["id"]

	// a real conversation to query.
	code, conv := call(t, srv, "create_conversation", map[string]any{
		"folderId": fid, "agentType": "codex", "title": "T",
	})
	if code != 200 {
		t.Fatalf("create_conversation %d: %v", code, conv)
	}
	cid := conv["id"]

	// missing id → invalid_input.
	if code, out := call(t, srv, "get_folder_conversation_turns", map[string]any{"beforeIndex": 0, "limit": 50}); code != 400 || out["code"] != "invalid_input" {
		t.Fatalf("missing conversationId: %d %v", code, out)
	}

	code, page := call(t, srv, "get_folder_conversation_turns", map[string]any{
		"conversationId": cid, "beforeIndex": 0, "limit": 50,
	})
	if code != 200 {
		t.Fatalf("get_folder_conversation_turns %d: %v", code, page)
	}
	for _, k := range []string{"turns", "turns_offset", "turns_total",
		"assistant_turns_before_offset", "prefix_hash", "prefix_hash_before_index"} {
		if _, ok := page[k]; !ok {
			t.Fatalf("page missing %q: %v", k, page)
		}
	}
	if turns, _ := page["turns"].([]any); len(turns) != 0 {
		t.Fatalf("expected empty turns, got %d", len(turns))
	}
	if page["turns_total"].(float64) != 0 {
		t.Fatalf("turns_total = %v", page["turns_total"])
	}
	// FNV-1a offset basis, the fingerprint of an empty prefix.
	if page["prefix_hash"] != "cbf29ce484222325" {
		t.Fatalf("empty prefix_hash = %v", page["prefix_hash"])
	}
	if page["prefix_hash_before_index"] != "cbf29ce484222325" {
		t.Fatalf("seam prefix_hash = %v", page["prefix_hash_before_index"])
	}
	if page["uncovered_prefix_max_ts"] != nil {
		t.Fatalf("uncovered_prefix_max_ts should be null for offset 0: %v", page["uncovered_prefix_max_ts"])
	}
}
