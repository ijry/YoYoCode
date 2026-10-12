package handlers_test

// Integration coverage for the pure-DB P0 handlers added for mcode-app
// compatibility. These run against a real on-disk SQLite DB (temp dir) through
// the actual Router, so a column/shape mismatch fails here rather than silently
// at runtime against the client.

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"yoyocode/src-go/app_state"
	"yoyocode/src-go/db"
	"yoyocode/src-go/web"
	"yoyocode/src-go/web/handlers"
)

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	database, err := db.Open(t.TempDir())
	if err != nil {
		t.Fatalf("db open: %v", err)
	}
	t.Cleanup(func() { database.Conn.Close() })
	st := appstate.Init(database, t.TempDir())
	r := web.NewRouter(st)
	r.SetToken("tok")
	handlers.RegisterAll(handlers.NewRegistry(r))
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv
}

// newTestServerState is like newTestServer but also returns the AppState so a
// test can reach the ACP manager (e.g. inject a fake agent spawner).
func newTestServerState(t *testing.T) (*httptest.Server, *appstate.AppState) {
	t.Helper()
	database, err := db.Open(t.TempDir())
	if err != nil {
		t.Fatalf("db open: %v", err)
	}
	t.Cleanup(func() { database.Conn.Close() })
	st := appstate.Init(database, t.TempDir())
	r := web.NewRouter(st)
	r.SetToken("tok")
	handlers.RegisterAll(handlers.NewRegistry(r))
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv, st
}

func call(t *testing.T, srv *httptest.Server, cmd string, payload map[string]any) (int, map[string]any) {
	t.Helper()
	body, _ := json.Marshal(payload)
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/"+cmd, bytes.NewReader(body))
	req.Header.Set("content-type", "application/json")
	req.Header.Set("authorization", "Bearer tok")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s: %v", cmd, err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func callArr(t *testing.T, srv *httptest.Server, cmd string, payload map[string]any) (int, []any) {
	t.Helper()
	body, _ := json.Marshal(payload)
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/"+cmd, bytes.NewReader(body))
	req.Header.Set("content-type", "application/json")
	req.Header.Set("authorization", "Bearer tok")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s: %v", cmd, err)
	}
	defer resp.Body.Close()
	var out []any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func callBool(t *testing.T, srv *httptest.Server, cmd string, payload map[string]any) (int, bool) {
	t.Helper()
	body, _ := json.Marshal(payload)
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/"+cmd, bytes.NewReader(body))
	req.Header.Set("content-type", "application/json")
	req.Header.Set("authorization", "Bearer tok")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s: %v", cmd, err)
	}
	defer resp.Body.Close()
	var out bool
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func jsonBody(v any) ([]byte, error) { return json.Marshal(v) }

// rawPost posts a raw body and returns the status and the raw (undecoded) body,
// for endpoints whose result is a bare JSON string (git_diff, get_git_branch).
func rawPost(t *testing.T, srv *httptest.Server, cmd string, body []byte) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/"+cmd, bytes.NewReader(body))
	req.Header.Set("content-type", "application/json")
	req.Header.Set("authorization", "Bearer tok")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s: %v", cmd, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// rawPostJSON marshals a payload and returns status + raw (undecoded) body.
func rawPostJSON(t *testing.T, srv *httptest.Server, cmd string, payload map[string]any) (int, string) {
	t.Helper()
	body, _ := json.Marshal(payload)
	return rawPost(t, srv, cmd, body)
}

func TestQuickMessagesLifecycle(t *testing.T) {
	srv := newTestServer(t)

	status, created := call(t, srv, "quick_messages_create", map[string]any{"title": "Hi", "content": "hello world"})
	if status != 200 {
		t.Fatalf("create status %d: %v", status, created)
	}
	if created["title"] != "Hi" || created["content"] != "hello world" {
		t.Fatalf("unexpected create body: %v", created)
	}
	id := created["id"]
	if _, ok := created["sort_order"]; !ok {
		t.Fatalf("missing sort_order: %v", created)
	}

	status, list := callArr(t, srv, "quick_messages_list", map[string]any{})
	if status != 200 || len(list) != 1 {
		t.Fatalf("list status %d len %d", status, len(list))
	}

	status, updated := call(t, srv, "quick_messages_update", map[string]any{"id": id, "content": "changed"})
	if status != 200 || updated["content"] != "changed" || updated["title"] != "Hi" {
		t.Fatalf("update body: %v", updated)
	}

	status, _ = call(t, srv, "quick_messages_delete", map[string]any{"id": id})
	if status != 200 {
		t.Fatalf("delete status %d", status)
	}
	status, list = callArr(t, srv, "quick_messages_list", map[string]any{})
	if status != 200 || len(list) != 0 {
		t.Fatalf("post-delete list len %d", len(list))
	}
}

func TestOpenedTabsCAS(t *testing.T) {
	srv := newTestServer(t)

	status, snap := call(t, srv, "list_opened_tabs", map[string]any{})
	if status != 200 {
		t.Fatalf("list status %d", status)
	}
	if snap["version"].(float64) != 0 {
		t.Fatalf("initial version %v", snap["version"])
	}
	if _, ok := snap["items"]; !ok {
		t.Fatalf("snapshot missing items key: %v", snap)
	}

	// Accepted save at version 0.
	status, out := call(t, srv, "save_opened_tabs", map[string]any{
		"expectedVersion": 0,
		"origin":          "test",
		"items": []any{
			map[string]any{"id": 0, "folder_id": 1, "conversation_id": 7, "agent_type": "claude_code", "position": 0, "is_active": true, "is_pinned": false},
			map[string]any{"id": 0, "folder_id": 1, "conversation_id": 0, "agent_type": "claude_code", "position": 1, "is_active": false, "is_pinned": false},
		},
	})
	if status != 200 || out["accepted"] != true {
		t.Fatalf("save not accepted: %v", out)
	}
	if out["version"].(float64) != 1 {
		t.Fatalf("version after save %v", out["version"])
	}
	tabs := out["tabs"].([]any)
	if len(tabs) != 1 { // draft (conversation_id 0/null) dropped
		t.Fatalf("expected 1 persisted tab, got %d: %v", len(tabs), tabs)
	}
	first := tabs[0].(map[string]any)
	if first["conversation_id"].(float64) != 7 || first["is_active"] != true {
		t.Fatalf("persisted tab wrong: %v", first)
	}

	// Stale save at version 0 is rejected and hands back the truth.
	status, stale := call(t, srv, "save_opened_tabs", map[string]any{
		"expectedVersion": 0, "origin": "test", "items": []any{},
	})
	if status != 200 || stale["accepted"] != false || stale["version"].(float64) != 1 {
		t.Fatalf("stale save not rejected cleanly: %v", stale)
	}
}

func TestModelProviderCRUDAndMask(t *testing.T) {
	srv := newTestServer(t)

	status, created := call(t, srv, "create_model_provider", map[string]any{
		"name": "p1", "apiUrl": "https://x", "apiKey": "sk-test-1234567890", "agentType": "claude_code",
	})
	if status != 200 {
		t.Fatalf("create status %d: %v", status, created)
	}
	if created["api_key"] != "sk-test-1234567890" {
		t.Fatalf("api_key not stored raw: %v", created["api_key"])
	}
	if created["api_key_masked"] != "sk-t\u2022\u2022\u2022\u2022\u2022\u2022\u2022\u2022\u2022\u20227890" {
		t.Fatalf("mask wrong: %q", created["api_key_masked"])
	}
	id := created["id"]

	status, list := callArr(t, srv, "list_model_providers", map[string]any{})
	if status != 200 || len(list) != 1 {
		t.Fatalf("list len %d", len(list))
	}

	status, upd := call(t, srv, "update_model_provider", map[string]any{"id": id, "name": "p2"})
	if status != 200 || upd["name"] != "p2" {
		t.Fatalf("update body: %v", upd)
	}

	status, _ = call(t, srv, "delete_model_provider", map[string]any{"id": id})
	if status != 200 {
		t.Fatalf("delete status %d", status)
	}
}

func TestConversationMutations(t *testing.T) {
	srv := newTestServer(t)

	// Seed a folder + conversation.
	status, _ := call(t, srv, "add_folder_to_history", map[string]any{"path": t.TempDir()})
	if status != 200 {
		t.Fatalf("add folder %d", status)
	}
	status, conv := call(t, srv, "create_conversation", map[string]any{"folderId": 1, "agentType": "claude_code", "title": "orig"})
	if status != 200 {
		t.Fatalf("create conv %d: %v", status, conv)
	}
	cid := conv["id"]

	status, _ = call(t, srv, "update_conversation_title", map[string]any{"conversationId": cid, "title": "renamed"})
	if status != 200 {
		t.Fatalf("title status %d", status)
	}
	status, _ = call(t, srv, "update_conversation_status", map[string]any{"conversationId": cid, "status": "completed"})
	if status != 200 {
		t.Fatalf("status status %d", status)
	}
	status, _ = call(t, srv, "update_conversation_pinned", map[string]any{"conversationId": cid, "pinned": true})
	if status != 200 {
		t.Fatalf("pinned status %d", status)
	}

	status, got := call(t, srv, "get_conversation", map[string]any{"conversationId": cid})
	if status != 200 {
		t.Fatalf("get conv %d", status)
	}
	if got["title"] != "renamed" || got["status"] != "completed" {
		t.Fatalf("mutations not applied: %v", got)
	}
}

func TestConversationTags(t *testing.T) {
	srv := newTestServer(t)

	status, tag := call(t, srv, "create_conversation_tag", map[string]any{"name": "Bug", "color": "#FF0000"})
	if status != 200 {
		t.Fatalf("create tag %d: %v", status, tag)
	}
	if tag["color"] != "#ff0000" {
		t.Fatalf("color not normalized: %v", tag["color"])
	}
	if tag["folder_id"] != nil {
		t.Fatalf("global tag should have null folder_id: %v", tag["folder_id"])
	}

	// Duplicate name (case-insensitive) rejected with i18n key.
	status, dup := call(t, srv, "create_conversation_tag", map[string]any{"name": "bug", "color": "#00ff00"})
	if status != 409 || dup["i18n_key"] != "errors.duplicateName" {
		t.Fatalf("expected duplicate 409 with key, got %d %v", status, dup)
	}

	// Branch tag default then update.
	status, bt := call(t, srv, "get_conversation_branch_tag", map[string]any{})
	if status != 200 || bt["enabled"] != false || bt["color"] != "#6e7781" {
		t.Fatalf("branch tag default: %v", bt)
	}
	status, bt = call(t, srv, "update_conversation_branch_tag", map[string]any{"enabled": true, "color": "#123456"})
	if status != 200 || bt["enabled"] != true || bt["color"] != "#123456" {
		t.Fatalf("branch tag update: %v", bt)
	}
}
