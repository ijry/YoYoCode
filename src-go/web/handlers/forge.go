package handlers

// Forge workbench foundation (B9a): panel settings storage, the folder→forge
// remote resolver (git remote parse + provider detection), and the shared
// route/provider helpers the per-forge REST clients build on. Mirrors
// commands/forge.rs + forge/{mod,auth,settings}.rs.

import (
	"encoding/json"
	"strconv"
	"strings"

	"yoyocode/src-go/db"
	"yoyocode/src-go/web"
)

const forgeSettingsKey = "forge_workbench_settings"

// RegisterForge wires the forge settings + remote-resolution endpoints.
func RegisterForge(reg *Registry) {
	reg.Register("forge_settings_get", func(c *web.Context) (any, error) {
		return loadForgeSettings(stateDB(c)), nil
	})
	reg.Register("forge_settings_set", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		store := loadForgeSettings(d)
		global, _ := store["global"].(map[string]any)
		folders, _ := store["folders"].(map[string]any)
		if global == nil {
			global = defaultForgePanel()
		}
		if folders == nil {
			folders = map[string]any{}
		}
		settings := settingsSubObject(c)
		_, hasFolder := c.Body["folderId"]
		if !hasFolder {
			_, hasFolder = c.Body["folder_id"]
		}
		folderID := int64Num(c.Body["folderId"])
		if folderID == 0 {
			folderID = int64Num(c.Body["folder_id"])
		}
		if !hasFolder || folderID == 0 {
			// Global scope.
			if settings != nil {
				global = normalizeForgePanel(settings)
			} else {
				global = defaultForgePanel()
			}
		} else {
			key := int64ToStr(folderID)
			if settings != nil {
				folders[key] = normalizeForgePanel(settings)
			} else {
				delete(folders, key)
			}
		}
		store = map[string]any{"global": global, "folders": folders}
		raw, _ := json.Marshal(store)
		upsertValue(d, forgeSettingsKey, string(raw), nowRFC3339())
		Emit(c, "forge-settings://changed", map[string]any{})
		return store, nil
	})

	reg.Register("folder_forge_remote", func(c *web.Context) (any, error) {
		d := stateDB(c)
		folderID := int64Num(c.Body["folderId"])
		if folderID == 0 {
			folderID = int64Num(c.Body["folder_id"])
		}
		path := folderPath(d, folderID)
		if path == "" {
			return nil, nil
		}
		stdout, _, ok := gitRun(path, "remote", "get-url", "origin")
		if !ok {
			return nil, nil
		}
		url := strings.TrimSpace(stdout)
		host, ownerRepo, ok := parseRemoteURL(url)
		if !ok {
			return nil, nil
		}
		provider, recognized := resolveForgeProvider(d, host)
		return map[string]any{
			"server_host": host,
			"owner_repo":  ownerRepo,
			"remote_url":  redactUserinfo(url),
			"provider":    provider,
			"supported":   recognized,
		}, nil
	})
}

// ─── forge settings ──────────────────────────────────────────────────────────

func defaultForgePanel() map[string]any {
	return map[string]any{
		"default_issue_scenario": nil,
		"default_pr_scenario":    nil,
		"writeback_default":      true,
		"scenario_prompts":       map[string]any{},
	}
}

func normalizeForgePanel(in map[string]any) map[string]any {
	out := defaultForgePanel()
	if v, ok := in["default_issue_scenario"]; ok {
		out["default_issue_scenario"] = trimOptionAny(v)
	}
	if v, ok := in["default_pr_scenario"]; ok {
		out["default_pr_scenario"] = trimOptionAny(v)
	}
	if v, ok := in["writeback_default"].(bool); ok {
		out["writeback_default"] = v
	}
	if v, ok := in["scenario_prompts"].(map[string]any); ok {
		out["scenario_prompts"] = v
	}
	return out
}

func trimOptionAny(v any) any {
	if s, ok := v.(string); ok {
		if t := strings.TrimSpace(s); t != "" {
			return t
		}
	}
	return nil
}

func loadForgeSettings(d *db.Database) map[string]any {
	raw := valueOrDefault(d, forgeSettingsKey)
	store := map[string]any{"global": defaultForgePanel(), "folders": map[string]any{}}
	if raw == "" {
		return store
	}
	var parsed map[string]any
	if json.Unmarshal([]byte(raw), &parsed) != nil {
		return store
	}
	if g, ok := parsed["global"].(map[string]any); ok {
		store["global"] = normalizeForgePanel(g)
	}
	if f, ok := parsed["folders"].(map[string]any); ok {
		norm := map[string]any{}
		for k, v := range f {
			if m, ok := v.(map[string]any); ok {
				norm[k] = normalizeForgePanel(m)
			}
		}
		store["folders"] = norm
	}
	return store
}

func settingsSubObject(c *web.Context) map[string]any {
	if raw, ok := c.Body["settings"].(map[string]any); ok {
		return raw
	}
	return nil
}

// ─── remote parsing + provider detection ─────────────────────────────────────

// parseRemoteURL mirrors forge::parse_remote_url → (host, owner/repo).
func parseRemoteURL(url string) (host, ownerRepo string, ok bool) {
	t := strings.TrimSpace(url)
	if t == "" {
		return "", "", false
	}
	if !strings.Contains(t, "://") {
		// scp-like: git@host:owner/repo(.git)
		idx := strings.Index(t, ":")
		if idx < 0 {
			return "", "", false
		}
		userHost := t[:idx]
		path := t[idx+1:]
		h := userHost
		if at := strings.LastIndex(userHost, "@"); at >= 0 {
			h = userHost[at+1:]
		}
		h = strings.ToLower(strings.TrimSpace(h))
		if h == "" || strings.Contains(h, "/") {
			return "", "", false
		}
		repo, rok := normalizeRepo(path)
		if !rok {
			return "", "", false
		}
		return h, repo, true
	}
	rest := t[strings.Index(t, "://")+3:]
	slash := strings.Index(rest, "/")
	if slash < 0 {
		return "", "", false
	}
	hostPort := rest[:slash]
	path := rest[slash+1:]
	h := hostPort
	if at := strings.LastIndex(hostPort, "@"); at >= 0 {
		h = hostPort[at+1:]
	}
	if colon := strings.Index(h, ":"); colon >= 0 {
		h = h[:colon]
	}
	h = strings.ToLower(strings.TrimSpace(h))
	if h == "" {
		return "", "", false
	}
	repo, rok := normalizeRepo(path)
	if !rok {
		return "", "", false
	}
	return h, repo, true
}

func normalizeRepo(input string) (string, bool) {
	t := strings.ToLower(strings.TrimSuffix(strings.Trim(strings.TrimSpace(input), "/"), ".git"))
	if t == "" || !strings.Contains(t, "/") {
		return "", false
	}
	for _, seg := range strings.Split(t, "/") {
		if seg == "" {
			return "", false
		}
		for _, ch := range seg {
			ok := (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') || ch == '-' || ch == '_' || ch == '.'
			if !ok {
				return "", false
			}
		}
	}
	return t, true
}

// providerFromHostName mirrors forge::auth::provider_from_host_name.
func providerFromHostName(host string) (string, bool) {
	h := strings.ToLower(strings.TrimSpace(host))
	if h == "" {
		return "", false
	}
	labels := strings.Split(h, ".")
	has := func(w string) bool {
		for _, l := range labels {
			if l == w {
				return true
			}
		}
		return false
	}
	switch {
	case has("gitlab"):
		return "gitlab", true
	case has("github"):
		return "github", true
	case has("gitea"), has("forgejo"):
		return "gitea", true
	}
	return "", false
}

// resolveForgeProvider returns (provider, recognized). An account registered
// for the host also counts as recognition even when the name gives nothing away.
func resolveForgeProvider(d *db.Database, host string) (string, bool) {
	if p, ok := providerFromHostName(host); ok {
		return p, true
	}
	if p, ok := accountProviderForHost(d, host); ok {
		return p, true
	}
	return "github", false
}

// accountProviderForHost scans stored github_accounts for one whose server_url
// host matches, returning its declared provider.
func accountProviderForHost(d *db.Database, host string) (string, bool) {
	accounts := loadGithubAccounts(d)
	arr, _ := accounts["accounts"].([]any)
	for _, a := range arr {
		m, ok := a.(map[string]any)
		if !ok {
			continue
		}
		su, _ := m["server_url"].(string)
		if su == "" {
			continue
		}
		ah, _, ok := parseAccountHost(su)
		if ok && ah == strings.ToLower(host) {
			if p, ok := m["provider"].(string); ok && p != "" {
				return p, true
			}
			return "github", true
		}
	}
	return "", false
}

func parseAccountHost(serverURL string) (string, string, bool) {
	s := strings.TrimSpace(serverURL)
	if s == "" {
		return "github.com", "", true
	}
	if strings.Contains(s, "://") {
		s = s[strings.Index(s, "://")+3:]
	}
	if slash := strings.Index(s, "/"); slash >= 0 {
		s = s[:slash]
	}
	if colon := strings.Index(s, ":"); colon >= 0 {
		s = s[:colon]
	}
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return "", "", false
	}
	return s, "", true
}

// redactUserinfo strips user:pass@ from a URL authority before it leaves us.
func redactUserinfo(url string) string {
	i := strings.Index(url, "://")
	if i < 0 {
		return url
	}
	scheme := url[:i]
	rest := url[i+3:]
	at := strings.Index(rest, "@")
	if at < 0 {
		return url
	}
	userinfo := rest[:at]
	if strings.Contains(userinfo, "/") {
		return url
	}
	return scheme + "://" + rest[at+1:]
}

func int64ToStr(n int64) string { return strconv.FormatInt(n, 10) }
