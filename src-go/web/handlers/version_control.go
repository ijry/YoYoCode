package handlers

// Mirror upstream web/handlers/version_control.rs + commands/version_control.rs.
// Git detection, git settings, GitHub/GitLab/Gitea account storage + token
// validation. Settings/accounts persist in app_metadata (JSON blobs, same keys
// as upstream). Account tokens persist under an app_metadata namespace in lieu
// of the desktop OS keyring — the get/save/delete wire contract round-trips
// identically.

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"yoyocode/src-go/db"
	"yoyocode/src-go/web"
)

const (
	gitSettingsKey      = "git_settings"
	githubAccountsKey   = "github_accounts"
	accountTokenPrefix  = "account_token:"
	tokenValidationHTTP = 15 * time.Second
)

// RegisterVersionControl wires git detection, settings, accounts and tokens.
func RegisterVersionControl(reg *Registry) {
	reg.Register("detect_git", func(c *web.Context) (any, error) {
		d := stateDB(c)
		custom := gitSettingsCustomPath(d)
		if custom != "" {
			return runGitVersion(custom), nil
		}
		if p := detectGitPath(); p != "" {
			return runGitVersion(p), nil
		}
		res := runGitVersion("git")
		if res["installed"] == true {
			return res, nil
		}
		return map[string]any{"installed": false, "version": nil, "path": nil}, nil
	})

	reg.Register("test_git_path", func(c *web.Context) (any, error) {
		path := strings.TrimSpace(GetString(c, "path"))
		if path == "" {
			return nil, web.ErrInvalidInput("Git path cannot be empty")
		}
		return runGitVersion(path), nil
	})

	reg.Register("get_git_settings", func(c *web.Context) (any, error) {
		return loadGitSettings(stateDB(c)), nil
	})
	reg.Register("update_git_settings", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		raw := GetRaw(c, "settings")
		if raw == nil {
			return nil, web.ErrInvalidInput("settings required")
		}
		upsertValue(d, gitSettingsKey, string(raw), nowRFC3339())
		return loadGitSettings(d), nil
	})

	reg.Register("get_github_accounts", func(c *web.Context) (any, error) {
		return loadGithubAccounts(stateDB(c)), nil
	})
	reg.Register("update_github_accounts", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		raw := GetRaw(c, "settings")
		if raw == nil {
			return nil, web.ErrInvalidInput("settings required")
		}
		upsertValue(d, githubAccountsKey, string(raw), nowRFC3339())
		return loadGithubAccounts(d), nil
	})

	reg.Register("validate_github_token", func(c *web.Context) (any, error) {
		return validateGithubToken(GetString(c, "serverUrl", "server_url"), GetString(c, "token"))
	})
	reg.Register("validate_gitlab_token", func(c *web.Context) (any, error) {
		return validateGitlabToken(GetString(c, "serverUrl", "server_url"), GetString(c, "token"))
	})
	reg.Register("validate_gitea_token", func(c *web.Context) (any, error) {
		return validateGiteaToken(GetString(c, "serverUrl", "server_url"), GetString(c, "token"))
	})

	reg.Register("save_account_token", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		id := GetString(c, "accountId", "account_id")
		if id == "" {
			return nil, web.ErrInvalidInput("accountId required")
		}
		upsertValue(d, accountTokenPrefix+id, GetString(c, "token"), nowRFC3339())
		return EmptyResult(), nil
	})
	reg.Register("get_account_token", func(c *web.Context) (any, error) {
		d := stateDB(c)
		id := GetString(c, "accountId", "account_id")
		if id == "" {
			return nil, web.ErrInvalidInput("accountId required")
		}
		v := accountTokenValue(d, accountTokenPrefix+id)
		if v == "" {
			return nil, nil
		}
		return v, nil
	})
	reg.Register("delete_account_token", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		id := GetString(c, "accountId", "account_id")
		if id == "" {
			return nil, web.ErrInvalidInput("accountId required")
		}
		_, _ = d.Conn.Exec("DELETE FROM app_metadata WHERE key = ?", accountTokenPrefix+id)
		return EmptyResult(), nil
	})
}

// ─── git detection ──────────────────────────────────────────────────────────

func runGitVersion(gitPath string) map[string]any {
	out, err := exec.Command(gitPath, "--version").Output()
	if err != nil {
		// Distinguish "ran but failed" from "could not execute": upstream
		// returns installed:false with the path either way for a run failure.
		if _, ok := err.(*exec.ExitError); ok {
			return map[string]any{"installed": false, "version": nil, "path": gitPath}
		}
		return map[string]any{"installed": false, "version": nil, "path": gitPath}
	}
	s := strings.TrimSpace(string(out))
	version := strings.TrimPrefix(s, "git version ")
	return map[string]any{"installed": true, "version": version, "path": gitPath}
}

func detectGitPath() string {
	which := "which"
	if runtime.GOOS == "windows" {
		which = "where"
	}
	out, err := exec.Command(which, "git").Output()
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		if t := strings.TrimSpace(line); t != "" {
			return t
		}
	}
	return ""
}

func gitSettingsCustomPath(d *db.Database) string {
	s := loadGitSettings(d)
	if v, ok := s["custom_path"].(string); ok {
		return strings.TrimSpace(v)
	}
	return ""
}

func loadGitSettings(d *db.Database) map[string]any {
	raw := valueOrDefault(d, gitSettingsKey)
	if raw == "" {
		return map[string]any{"custom_path": nil}
	}
	var m map[string]any
	if json.Unmarshal([]byte(raw), &m) != nil {
		return map[string]any{"custom_path": nil}
	}
	if _, ok := m["custom_path"]; !ok {
		m["custom_path"] = nil
	}
	return m
}

func loadGithubAccounts(d *db.Database) map[string]any {
	raw := valueOrDefault(d, githubAccountsKey)
	if raw == "" {
		return map[string]any{"accounts": []any{}}
	}
	var m map[string]any
	if json.Unmarshal([]byte(raw), &m) != nil {
		return map[string]any{"accounts": []any{}}
	}
	if _, ok := m["accounts"]; !ok {
		m["accounts"] = []any{}
	}
	return m
}

func accountTokenValue(d *db.Database, key string) string {
	if d == nil || d.Conn == nil {
		return ""
	}
	var v string
	_ = d.Conn.QueryRow("SELECT value FROM app_metadata WHERE key = ? AND deleted_at IS NULL", key).Scan(&v)
	return v
}

// ─── token validation ───────────────────────────────────────────────────────

func tokenFailure(status int, body string) map[string]any {
	msg := fmt.Sprintf("API returned status %d: %s", status, body)
	if status == 401 {
		msg = "Invalid or expired token"
	}
	return map[string]any{
		"success": false, "username": nil, "scopes": []string{},
		"avatar_url": nil, "message": msg,
	}
}

func validateGithubToken(serverURL, token string) (any, error) {
	trimmedURL := strings.TrimRight(strings.TrimSpace(serverURL), "/")
	t := strings.TrimSpace(token)
	if t == "" {
		return nil, web.ErrInvalidInput("Token cannot be empty")
	}
	apiURL := "https://api.github.com/user"
	if trimmedURL != "" && trimmedURL != "https://github.com" && trimmedURL != "http://github.com" {
		apiURL = trimmedURL + "/api/v3/user"
	}
	req, _ := http.NewRequest(http.MethodGet, apiURL, nil)
	req.Header.Set("Authorization", "Bearer "+t)
	req.Header.Set("User-Agent", "yoyocode")
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := httpClient().Do(req)
	if err != nil {
		return nil, web.ErrNetwork("Failed to connect to GitHub API").WithDetail(err.Error())
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return tokenFailure(resp.StatusCode, string(body)), nil
	}
	scopes := []string{}
	if h := resp.Header.Get("x-oauth-scopes"); h != "" {
		for _, s := range strings.Split(h, ",") {
			if v := strings.TrimSpace(s); v != "" {
				scopes = append(scopes, v)
			}
		}
	}
	var user struct {
		Login     string  `json:"login"`
		AvatarURL *string `json:"avatar_url"`
	}
	if json.Unmarshal(body, &user) != nil {
		return nil, web.ErrNetwork("Failed to parse GitHub API response")
	}
	return map[string]any{
		"success": true, "username": user.Login, "scopes": scopes,
		"avatar_url": optStr(user.AvatarURL), "message": nil,
	}, nil
}

func validateGitlabToken(serverURL, token string) (any, error) {
	t := strings.TrimSpace(token)
	if t == "" {
		return nil, web.ErrInvalidInput("Token cannot be empty")
	}
	origin := originFromBase(strings.TrimSpace(serverURL), "https://gitlab.com")
	req, _ := http.NewRequest(http.MethodGet, origin+"/api/v4/user", nil)
	req.Header.Set("PRIVATE-TOKEN", t)
	req.Header.Set("User-Agent", "yoyocode")
	req.Header.Set("Accept", "application/json")
	resp, err := httpClient().Do(req)
	if err != nil {
		return nil, web.ErrNetwork("Failed to connect to GitLab API").WithDetail(err.Error())
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return tokenFailure(resp.StatusCode, string(body)), nil
	}
	var user struct {
		Username  string  `json:"username"`
		AvatarURL *string `json:"avatar_url"`
	}
	if json.Unmarshal(body, &user) != nil {
		return nil, web.ErrNetwork("Failed to parse GitLab API response")
	}
	scopes := []string{}
	sreq, _ := http.NewRequest(http.MethodGet, origin+"/api/v4/personal_access_tokens/self", nil)
	sreq.Header.Set("PRIVATE-TOKEN", t)
	sreq.Header.Set("User-Agent", "yoyocode")
	sreq.Header.Set("Accept", "application/json")
	if sresp, serr := httpClient().Do(sreq); serr == nil {
		defer sresp.Body.Close()
		if sresp.StatusCode >= 200 && sresp.StatusCode < 300 {
			var ts struct {
				Scopes []string `json:"scopes"`
			}
			sbody, _ := io.ReadAll(sresp.Body)
			if json.Unmarshal(sbody, &ts) == nil {
				scopes = ts.Scopes
			}
		}
	}
	return map[string]any{
		"success": true, "username": user.Username, "scopes": scopes,
		"avatar_url": optStr(user.AvatarURL), "message": nil,
	}, nil
}

func validateGiteaToken(serverURL, token string) (any, error) {
	t := strings.TrimSpace(token)
	if t == "" {
		return nil, web.ErrInvalidInput("Token cannot be empty")
	}
	base := strings.TrimRight(strings.TrimSpace(serverURL), "/")
	if base == "" {
		return nil, web.ErrInvalidInput("A Gitea server URL is required")
	}
	origin := base
	if !strings.Contains(base, "://") {
		origin = "https://" + base
	}
	req, _ := http.NewRequest(http.MethodGet, origin+"/api/v1/user", nil)
	req.Header.Set("Authorization", "token "+t)
	req.Header.Set("User-Agent", "yoyocode")
	req.Header.Set("Accept", "application/json")
	resp, err := httpClient().Do(req)
	if err != nil {
		return nil, web.ErrNetwork("Failed to connect to Gitea API").WithDetail(err.Error())
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return tokenFailure(resp.StatusCode, string(body)), nil
	}
	var user struct {
		Login     string  `json:"login"`
		AvatarURL *string `json:"avatar_url"`
	}
	if json.Unmarshal(body, &user) != nil {
		return nil, web.ErrNetwork("Failed to parse Gitea API response")
	}
	return map[string]any{
		"success": true, "username": user.Login, "scopes": []string{},
		"avatar_url": optStr(user.AvatarURL), "message": nil,
	}, nil
}

func originFromBase(base, fallback string) string {
	if base == "" {
		return fallback
	}
	if strings.Contains(base, "://") {
		return strings.TrimRight(base, "/")
	}
	return "https://" + strings.TrimRight(base, "/")
}

func optStr(p *string) any {
	if p == nil {
		return nil
	}
	return *p
}

func httpClient() *http.Client { return &http.Client{Timeout: tokenValidationHTTP} }
