package handlers

// System proxy + terminal settings (B11), mirroring the rest of
// web/handlers/system_settings.rs: proxy get/update (normalized, exported to
// child-process env), terminal get/update, and the default-shell picker
// (get_available_terminal_shells). Settings persist in app_metadata.

import (
	"encoding/json"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"yoyocode/src-go/db"
	"yoyocode/src-go/web"
)

const (
	systemProxyKey        = "system_proxy_settings"
	systemTerminalKey     = "system_terminal_settings"
	terminalShellSystemID = "system"
	terminalShellCustomID = "custom"
)

func metadataValue(d *db.Database, key string) string {
	if d == nil || d.Conn == nil {
		return ""
	}
	var v string
	_ = d.Conn.QueryRow(`SELECT value FROM app_metadata WHERE key = ? AND deleted_at IS NULL`, key).Scan(&v)
	return v
}

// ── proxy ──

func loadProxySettings(d *db.Database) map[string]any {
	raw := metadataValue(d, systemProxyKey)
	out := map[string]any{"enabled": false, "proxy_url": nil, "no_proxy": nil}
	if raw == "" {
		return out
	}
	var m map[string]any
	if json.Unmarshal([]byte(raw), &m) != nil {
		return out
	}
	// Normalize on read too, so a bare host:port saved by an older build heals.
	norm, err := normalizeProxySettings(m)
	if err != nil {
		return out
	}
	return norm
}

func normalizeProxySettings(in map[string]any) (map[string]any, error) {
	enabled, _ := in["enabled"].(bool)
	proxyURL := strings.TrimSpace(asString(pick(in, "proxy_url", "proxyUrl")))
	noProxy := canonicalNoProxy(asString(pick(in, "no_proxy", "noProxy")))
	if !enabled {
		out := map[string]any{"enabled": false, "proxy_url": nil, "no_proxy": nil}
		if proxyURL != "" {
			out["proxy_url"] = proxyURL
		}
		if noProxy != "" {
			out["no_proxy"] = noProxy
		}
		return out, nil
	}
	if proxyURL == "" {
		return nil, web.ErrConfigMissing("Proxy URL is required when proxy is enabled")
	}
	normURL, err := normalizeProxyURL(proxyURL)
	if err != nil {
		return nil, err
	}
	out := map[string]any{"enabled": true, "proxy_url": normURL, "no_proxy": nil}
	if noProxy != "" {
		out["no_proxy"] = noProxy
	}
	return out, nil
}

func normalizeProxyURL(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	normalized := trimmed
	if !strings.Contains(trimmed, "://") {
		normalized = "http://" + trimmed
	}
	u, err := url.Parse(normalized)
	if err != nil || u.Hostname() == "" {
		return "", web.ErrInvalidInput("Invalid proxy URL").WithDetail("a proxy address must include a host, e.g. http://127.0.0.1:7890")
	}
	return normalized, nil
}

// canonicalNoProxy trims entries, drops empties, and rejoins with commas.
func canonicalNoProxy(raw string) string {
	parts := strings.Split(raw, ",")
	out := []string{}
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return strings.Join(out, ",")
}

func applyProxyEnv(settings map[string]any) {
	enabled, _ := settings["enabled"].(bool)
	keys := []string{"HTTP_PROXY", "http_proxy", "HTTPS_PROXY", "https_proxy", "ALL_PROXY", "all_proxy"}
	if !enabled {
		for _, k := range keys {
			_ = os.Unsetenv(k)
		}
		_ = os.Unsetenv("NO_PROXY")
		_ = os.Unsetenv("no_proxy")
		return
	}
	urlStr := asString(settings["proxy_url"])
	for _, k := range keys {
		_ = os.Setenv(k, urlStr)
	}
	noProxy := "localhost,127.0.0.1,::1"
	if np := asString(settings["no_proxy"]); np != "" {
		noProxy = noProxy + "," + np
	}
	_ = os.Setenv("NO_PROXY", noProxy)
	_ = os.Setenv("no_proxy", noProxy)
}

// ── terminal ──

func loadTerminalSettings(d *db.Database) map[string]any {
	raw := metadataValue(d, systemTerminalKey)
	out := map[string]any{"default_shell": nil, "colorize_command_output": false}
	if raw == "" {
		return out
	}
	var m map[string]any
	if json.Unmarshal([]byte(raw), &m) != nil {
		return out
	}
	if ds := strings.TrimSpace(asString(pick(m, "default_shell", "defaultShell"))); ds != "" {
		out["default_shell"] = ds
	}
	if b, ok := pick(m, "colorize_command_output", "colorizeCommandOutput").(bool); ok {
		out["colorize_command_output"] = b
	}
	return out
}

// ── shell resolution ──

func resolveShellDefault() string {
	if runtime.GOOS == "windows" {
		if s := strings.TrimSpace(os.Getenv("SHELL")); s != "" {
			return s
		}
		if s := strings.TrimSpace(os.Getenv("COMSPEC")); s != "" {
			return s
		}
		return "cmd.exe"
	}
	if s := strings.TrimSpace(os.Getenv("SHELL")); s != "" {
		return s
	}
	for _, c := range []string{"/bin/zsh", "/bin/bash", "/bin/sh"} {
		if st, err := os.Stat(c); err == nil && !st.IsDir() {
			return c
		}
	}
	return "/bin/sh"
}

func resolveShellPath(value string) (string, bool) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", false
	}
	looksLikePath := filepath.IsAbs(trimmed) || strings.ContainsAny(trimmed, "/\\")
	if looksLikePath {
		if st, err := os.Stat(trimmed); err == nil && !st.IsDir() {
			return trimmed, true
		}
		if runtime.GOOS == "windows" && filepath.Ext(trimmed) == "" {
			withExe := trimmed + ".exe"
			if st, err := os.Stat(withExe); err == nil && !st.IsDir() {
				return withExe, true
			}
		}
		return "", false
	}
	if resolved, err := exec.LookPath(trimmed); err == nil {
		return resolved, true
	}
	return "", false
}

func shellExists(value string) bool {
	_, ok := resolveShellPath(value)
	return ok
}

func resolveEffectiveShell(defaultShell string) string {
	sel := strings.TrimSpace(defaultShell)
	if sel == "" {
		return resolveShellDefault()
	}
	if resolved, ok := resolveShellPath(sel); ok {
		return resolved
	}
	return sel
}

func buildAvailableTerminalShells(defaultShell string) map[string]any {
	options := []map[string]any{
		{"id": terminalShellSystemID, "label_key": "terminalSystemDefault", "value": nil, "exists": true, "accepts_custom_path": false},
	}
	if runtime.GOOS == "windows" {
		for _, sh := range [][2]string{{"pwsh.exe", "terminalPowerShell7"}, {"powershell.exe", "terminalWindowsPowerShell"}, {"cmd.exe", "terminalCmd"}} {
			options = append(options, map[string]any{
				"id": sh[0], "label_key": sh[1], "value": sh[0], "exists": shellExists(sh[0]), "accepts_custom_path": false,
			})
		}
	}
	options = append(options, map[string]any{
		"id": terminalShellCustomID, "label_key": "terminalShellCustom", "value": nil, "exists": true, "accepts_custom_path": true,
	})
	return map[string]any{"options": options, "resolved_shell": resolveEffectiveShell(defaultShell)}
}

func RegisterSystemSettingsMore(reg *Registry) {
	reg.Register("get_system_proxy_settings", func(c *web.Context) (any, error) {
		return loadProxySettings(stateDB(c)), nil
	})
	reg.Register("update_system_proxy_settings", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		in, _ := c.Body["settings"].(map[string]any)
		if in == nil {
			in = map[string]any{}
		}
		norm, e := normalizeProxySettings(in)
		if e != nil {
			return nil, e
		}
		raw, _ := json.Marshal(norm)
		upsertValue(d, systemProxyKey, string(raw), nowRFC3339())
		applyProxyEnv(norm)
		return norm, nil
	})

	reg.Register("get_system_terminal_settings", func(c *web.Context) (any, error) {
		return loadTerminalSettings(stateDB(c)), nil
	})
	reg.Register("update_system_terminal_settings", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		in, _ := c.Body["settings"].(map[string]any)
		if in == nil {
			in = map[string]any{}
		}
		out := map[string]any{"default_shell": nil, "colorize_command_output": false}
		if ds := strings.TrimSpace(asString(pick(in, "default_shell", "defaultShell"))); ds != "" {
			out["default_shell"] = ds
		}
		if b, ok := pick(in, "colorize_command_output", "colorizeCommandOutput").(bool); ok {
			out["colorize_command_output"] = b
		}
		raw, _ := json.Marshal(out)
		upsertValue(d, systemTerminalKey, string(raw), nowRFC3339())
		return out, nil
	})

	reg.Register("get_available_terminal_shells", func(c *web.Context) (any, error) {
		settings := loadTerminalSettings(stateDB(c))
		return buildAvailableTerminalShells(asString(settings["default_shell"])), nil
	})
}
