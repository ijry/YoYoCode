package handlers_test

import (
	"os/exec"
	"testing"
)

// The forge auth resolution is the compatibility-critical part: mcode branches
// recovery on the i18n_key, so a missing account / unsupported host must carry
// the exact keys at HTTP 422. (Live GitHub calls aren't exercised offline.)
func TestForgeAuthErrors(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	srv := newTestServer(t)

	// A github.com remote with NO stored account → noAccount (422).
	repo := t.TempDir()
	runGitTest(t, repo, "init")
	runGitTest(t, repo, "remote", "add", "origin", "git@github.com:foo/bar.git")
	_, det := call(t, srv, "open_folder", map[string]any{"path": repo})
	fid := det["id"]
	{
		code, out := call(t, srv, "forge_identity", map[string]any{"folderId": fid})
		if code != 422 || out["code"] != "configuration_missing" {
			t.Fatalf("expected 422 configuration_missing, got %d %v", code, out)
		}
		if out["i18n_key"] != "Forge.errors.noAccount" {
			t.Fatalf("expected noAccount i18n_key, got %v", out["i18n_key"])
		}
		params, _ := out["i18n_params"].(map[string]any)
		if params == nil || params["host"] != "github.com" || params["provider"] != "GitHub" {
			t.Fatalf("i18n_params wrong: %v", out["i18n_params"])
		}
	}

	// An unrecognized host → unsupportedHost (422) with the host param.
	repo2 := t.TempDir()
	runGitTest(t, repo2, "init")
	runGitTest(t, repo2, "remote", "add", "origin", "https://git.corp.internal/team/app.git")
	_, det2 := call(t, srv, "open_folder", map[string]any{"path": repo2})
	fid2 := det2["id"]
	{
		code, out := call(t, srv, "forge_list_labels", map[string]any{"folderId": fid2})
		if code != 422 || out["i18n_key"] != "Forge.errors.unsupportedHost" {
			t.Fatalf("expected unsupportedHost 422, got %d %v", code, out)
		}
		params, _ := out["i18n_params"].(map[string]any)
		if params == nil || params["host"] != "git.corp.internal" {
			t.Fatalf("unsupportedHost param: %v", out["i18n_params"])
		}
	}

	// After adding a github account WITHOUT a token, auth surfaces as
	// authentication_failed (not noAccount): adding another account won't fix it.
	call(t, srv, "update_github_accounts", map[string]any{
		"settings": map[string]any{"accounts": []any{map[string]any{
			"id": "acc1", "server_url": "https://github.com", "username": "octocat",
			"scopes": []any{}, "is_default": true, "created_at": "2026-01-01", "provider": "github",
		}}},
	})
	{
		code, out := call(t, srv, "forge_identity", map[string]any{"folderId": fid})
		if code != 422 || out["code"] != "authentication_failed" {
			t.Fatalf("expected 422 authentication_failed (no token), got %d %v", code, out)
		}
	}
}
