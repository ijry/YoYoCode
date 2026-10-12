package handlers_test

import "testing"

func TestVersionControlSettingsAndTokens(t *testing.T) {
	srv := newTestServer(t)

	// git_settings: default then round-trip.
	{
		_, def := call(t, srv, "get_git_settings", map[string]any{})
		if _, ok := def["custom_path"]; !ok {
			t.Fatalf("default git_settings missing custom_path: %v", def)
		}
		_, upd := call(t, srv, "update_git_settings", map[string]any{
			"settings": map[string]any{"custom_path": "/usr/bin/git"},
		})
		if upd["custom_path"] != "/usr/bin/git" {
			t.Fatalf("update_git_settings: %v", upd)
		}
		_, got := call(t, srv, "get_git_settings", map[string]any{})
		if got["custom_path"] != "/usr/bin/git" {
			t.Fatalf("persisted git_settings: %v", got)
		}
	}

	// github_accounts: default {accounts:[]} then round-trip.
	{
		_, def := call(t, srv, "get_github_accounts", map[string]any{})
		if _, ok := def["accounts"].([]any); !ok {
			t.Fatalf("default github_accounts: %v", def)
		}
		acct := map[string]any{
			"id": "a1", "server_url": "https://github.com", "username": "octocat",
			"scopes": []any{"repo"}, "avatar_url": nil, "is_default": true, "created_at": "2026-01-01",
		}
		_, upd := call(t, srv, "update_github_accounts", map[string]any{
			"settings": map[string]any{"accounts": []any{acct}},
		})
		accs := upd["accounts"].([]any)
		if len(accs) != 1 || accs[0].(map[string]any)["username"] != "octocat" {
			t.Fatalf("update_github_accounts: %v", upd)
		}
	}

	// account token save → get → delete.
	{
		if code, _ := call(t, srv, "save_account_token", map[string]any{"accountId": "a1", "token": "ghp_secret"}); code != 200 {
			t.Fatalf("save_account_token status %d", code)
		}
		body, _ := jsonBody(map[string]any{"accountId": "a1"})
		code, raw := rawPost(t, srv, "get_account_token", body)
		if code != 200 || raw != "\"ghp_secret\"\n" && raw != "\"ghp_secret\"" {
			t.Fatalf("get_account_token: %d %q", code, raw)
		}
		if code, _ := call(t, srv, "delete_account_token", map[string]any{"accountId": "a1"}); code != 200 {
			t.Fatalf("delete_account_token status %d", code)
		}
		body, _ = jsonBody(map[string]any{"accountId": "a1"})
		code, raw = rawPost(t, srv, "get_account_token", body)
		if code != 200 || (raw != "null" && raw != "null\n") {
			t.Fatalf("get_account_token after delete: %d %q", code, raw)
		}
	}

	// validate_github_token with empty token → invalid_input (no network).
	{
		code, out := call(t, srv, "validate_github_token", map[string]any{"serverUrl": "", "token": ""})
		if code != 400 || out["code"] != "invalid_input" {
			t.Fatalf("empty token: %d %v", code, out)
		}
	}
}
