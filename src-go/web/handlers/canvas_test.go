package handlers_test

// Canvas boards + node graph: board CRUD, node create/update/move, grouping
// conversations into a custom region, delete, and the revision/snapshot wiring.

import "testing"

func TestCanvasBoardsAndNodes(t *testing.T) {
	srv := newTestServer(t)
	proj := t.TempDir()
	_, det := call(t, srv, "open_folder", map[string]any{"path": proj})
	fid := det["id"]
	_, cid := callRaw(t, srv, "create_conversation", map[string]any{"folderId": fid, "agentType": "codex", "title": "C"})

	// empty board list.
	_, boards := callArr(t, srv, "canvas_list_boards", map[string]any{})
	if len(boards) != 0 {
		t.Fatalf("boards should be empty, got %d", len(boards))
	}

	// create a board.
	_, board := call(t, srv, "canvas_create_board", map[string]any{"input": map[string]any{"name": "Plan", "color": "blue"}})
	if board["name"] != "Plan" || board["color"] != "blue" {
		t.Fatalf("create_board = %v", board)
	}
	bid := board["id"]

	// bad color rejected.
	if code, out := call(t, srv, "canvas_create_board", map[string]any{"input": map[string]any{"color": "chartreuse"}}); code != 400 || out["code"] != "invalid_input" {
		t.Fatalf("bad color: %d %v", code, out)
	}

	// list_boards summary shape.
	_, boards = callArr(t, srv, "canvas_list_boards", map[string]any{})
	if len(boards) != 1 {
		t.Fatalf("boards len %d", len(boards))
	}
	sum := boards[0].(map[string]any)
	for _, k := range []string{"board", "node_count", "terminal_count", "preview"} {
		if _, ok := sum[k]; !ok {
			t.Fatalf("BoardSummary missing %q: %v", k, sum)
		}
	}

	// list_nodes: empty, revision present.
	_, snap := call(t, srv, "canvas_list_nodes", map[string]any{"boardId": bid})
	if snap["board_id"] == nil || snap["revision"] == nil {
		t.Fatalf("snapshot shape = %v", snap)
	}
	rev0 := snap["revision"].(float64)

	// create a note node.
	_, mut := call(t, srv, "canvas_create_node", map[string]any{"input": map[string]any{
		"board_id": bid, "kind": "note", "content": "hello", "x": 10.0, "y": 20.0, "width": 200.0, "height": 100.0,
	}})
	node, _ := mut["value"].(map[string]any)
	if node == nil || node["kind"] != "note" || node["content"] != "hello" {
		t.Fatalf("create note = %v", mut)
	}
	if mut["revision"].(float64) <= rev0 {
		t.Fatalf("revision did not advance: %v <= %v", mut["revision"], rev0)
	}
	noteID := node["id"]

	// note with content on a non-note kind is rejected (folder needs folder_id anyway).
	if code, out := call(t, srv, "canvas_create_node", map[string]any{"input": map[string]any{
		"board_id": bid, "kind": "note", "x": 0.0, "y": 0.0, "width": 50.0, "height": 50.0, "path": "/x",
	}}); code != 400 || out["code"] != "invalid_input" {
		t.Fatalf("path on note: %d %v", code, out)
	}

	// create a conversation pin.
	_, mut2 := call(t, srv, "canvas_create_node", map[string]any{"input": map[string]any{
		"board_id": bid, "kind": "conversation", "conversation_id": cid, "x": 300.0, "y": 40.0, "width": 224.0, "height": 132.0,
	}})
	pin, _ := mut2["value"].(map[string]any)
	pinID := pin["id"]

	// update note title + move.
	_, u := call(t, srv, "canvas_update_node", map[string]any{"nodeId": noteID, "patch": map[string]any{"title": "Note A", "x": 15.0}})
	un, _ := u["value"].(map[string]any)
	if un["title"] != "Note A" || un["x"].(float64) != 15.0 {
		t.Fatalf("update node = %v", u)
	}

	// move_nodes batch.
	_, mv := call(t, srv, "canvas_move_nodes", map[string]any{"moves": []any{map[string]any{"id": noteID, "x": 99.0, "y": 88.0}}})
	mvv, _ := mv["value"].([]any)
	if len(mvv) != 1 {
		t.Fatalf("move value = %v", mv)
	}

	// group the conversation into a NEW custom region, consuming the pin.
	_, grp := call(t, srv, "canvas_group_into_region", map[string]any{"input": map[string]any{
		"board_id": bid, "member_ids": []any{cid}, "consume_node_ids": []any{pinID},
		"x": 400.0, "y": 400.0, "width": 300.0, "height": 300.0,
	}})
	gv, _ := grp["value"].(map[string]any)
	region, _ := gv["node"].(map[string]any)
	if region["kind"] != "custom" {
		t.Fatalf("group region kind = %v", region["kind"])
	}
	members, _ := region["member_ids"].([]any)
	if len(members) != 1 {
		t.Fatalf("region members = %v", region["member_ids"])
	}
	deleted, _ := gv["deleted_ids"].([]any)
	if len(deleted) != 1 {
		t.Fatalf("group deleted_ids = %v", gv["deleted_ids"])
	}

	// the pin is gone; note + region remain.
	_, snap2 := call(t, srv, "canvas_list_nodes", map[string]any{"boardId": bid})
	nodes2, _ := snap2["nodes"].([]any)
	if len(nodes2) != 2 {
		t.Fatalf("nodes after group = %d, want 2", len(nodes2))
	}

	// delete the note.
	if code, _ := call(t, srv, "canvas_delete_node", map[string]any{"nodeId": noteID}); code != 200 {
		t.Fatalf("delete_node status %d", code)
	}
	_, snap3 := call(t, srv, "canvas_list_nodes", map[string]any{"boardId": bid})
	nodes3, _ := snap3["nodes"].([]any)
	if len(nodes3) != 1 {
		t.Fatalf("nodes after delete = %d, want 1", len(nodes3))
	}

	// delete the board → returns deleted node ids, list empties.
	_, db := call(t, srv, "canvas_delete_board", map[string]any{"boardId": bid})
	if _, ok := db["value"]; !ok {
		t.Fatalf("delete_board mutation shape = %v", db)
	}
	_, boards = callArr(t, srv, "canvas_list_boards", map[string]any{})
	if len(boards) != 0 {
		t.Fatalf("boards after delete = %d", len(boards))
	}
}
