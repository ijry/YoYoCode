package handlers_test

// Forge-triggered work task coverage (B10 tail): the issue path needs no
// network — create_from_forge verifies the folder origin, resolves the stored
// account, composes the prompt server-side and dedups on the source key. We
// also assert the folder_mismatch and duplicate discriminated outcomes, and the
// reverse lookup by source key.

import (
	"strings"
	"testing"
)

func TestWorkTaskCreateFromForgeIssue(t *testing.T) {
	srv := newTestServer(t)
	repo := t.TempDir()
	runGitTest(t, repo, "init")
	runGitTest(t, repo, "remote", "add", "origin", "git@github.com:octocat/hello.git")

	// open the folder.
	code, det := call(t, srv, "open_folder", map[string]any{"path": repo})
	if code != 200 {
		t.Fatalf("open_folder %d: %v", code, det)
	}
	fid := det["id"]

	// store a default github account + token so auth resolves.
	acct := map[string]any{
		"id": "a1", "server_url": "https://github.com", "username": "octocat",
		"scopes": []any{"repo"}, "avatar_url": nil, "is_default": true, "created_at": "2026-01-01",
	}
	if code, _ := call(t, srv, "update_github_accounts", map[string]any{
		"settings": map[string]any{"accounts": []any{acct}},
	}); code != 200 {
		t.Fatalf("update_github_accounts %d", code)
	}
	if code, _ := call(t, srv, "save_account_token", map[string]any{"accountId": "a1", "token": "ghp_x"}); code != 200 {
		t.Fatalf("save_account_token %d", code)
	}

	draft := map[string]any{
		"folderId": fid,
		"source": map[string]any{
			"kind": "issue", "provider": "github", "server_host": "github.com",
			"owner_repo": "octocat/hello", "number": 5,
		},
		"snapshot":    map[string]any{"title": "Login broken", "body": "steps", "labels": []any{"bug"}, "author": "mallory"},
		"scenario":    "fix",
		"instruction": "please be careful",
		"writeback":   true,
	}

	// create → outcome created.
	code, res := call(t, srv, "work_task_create_from_forge", map[string]any{"draft": draft})
	if code != 200 {
		t.Fatalf("create_from_forge %d: %v", code, res)
	}
	if res["outcome"] != "created" {
		t.Fatalf("outcome = %v, want created: %v", res["outcome"], res)
	}
	task, _ := res["task"].(map[string]any)
	if task == nil {
		t.Fatalf("created result missing task: %v", res)
	}
	if tt, _ := task["title"].(string); !strings.HasPrefix(tt, "#5 · Login broken") {
		t.Fatalf("task title = %v", task["title"])
	}
	if task["source_kind"] != "forge_issue" {
		t.Fatalf("source_kind = %v", task["source_kind"])
	}
	// config carries two prompt blocks (instruction + untrusted envelope).
	cfg, _ := task["config"].(map[string]any)
	blocks, _ := cfg["prompt_blocks"].([]any)
	if len(blocks) != 2 {
		t.Fatalf("prompt_blocks len = %d: %v", len(blocks), cfg)
	}
	b0, _ := blocks[0].(map[string]any)
	if txt, _ := b0["text"].(string); !strings.Contains(txt, "issue #5") || !strings.Contains(txt, "please be careful") {
		t.Fatalf("instruction block missing issue ref or user note: %q", b0["text"])
	}
	b1, _ := blocks[1].(map[string]any)
	if txt, _ := b1["text"].(string); !strings.Contains(txt, "----[forge-data-begin]----") || !strings.Contains(txt, "Login broken") {
		t.Fatalf("envelope block malformed: %q", b1["text"])
	}

	// reverse lookup by the canonical source key.
	key := "github:github.com:octocat/hello:issue:5"
	_, links := callArr(t, srv, "work_task_lookup_by_source", map[string]any{"sourceKeys": []any{key}})
	if len(links) != 1 {
		t.Fatalf("lookup len %d: %v", len(links), links)
	}
	link, _ := links[0].(map[string]any)
	if link["source_key"] != key || link["status"] != "todo" {
		t.Fatalf("lookup row = %v", link)
	}

	// create again without force → duplicate.
	_, dup := call(t, srv, "work_task_create_from_forge", map[string]any{"draft": draft})
	if dup["outcome"] != "duplicate" {
		t.Fatalf("second create outcome = %v, want duplicate", dup["outcome"])
	}

	// a mismatched repo → folder_mismatch (origin is octocat/hello).
	bad := map[string]any{
		"folderId": fid,
		"source": map[string]any{
			"kind": "issue", "provider": "github", "server_host": "github.com",
			"owner_repo": "someone/else", "number": 9,
		},
		"snapshot": map[string]any{"title": "x"},
	}
	_, mm := call(t, srv, "work_task_create_from_forge", map[string]any{"draft": bad})
	if mm["outcome"] != "folder_mismatch" {
		t.Fatalf("mismatch outcome = %v, want folder_mismatch: %v", mm["outcome"], mm)
	}
}
