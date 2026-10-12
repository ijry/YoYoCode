package handlers

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"yoyocode/src-go/web"
)

func mcpTestHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	old := mcpHomeOverride
	mcpHomeOverride = home
	t.Cleanup(func() { mcpHomeOverride = old })
	for _, k := range []string{"DSH_HOME", "QODER_CONFIG_DIR", "QODER_CLI_HOME", "GEMINI_CLI_HOME", "QODER_CONFIG_DIR_NAME", "GEMINI_HOME", "CODEX_HOME"} {
		t.Setenv(k, "")
	}
	return home
}

func readJSONObj(t *testing.T, path string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var v map[string]any
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return v
}

func TestAppCanHostSpec(t *testing.T) {
	sse := map[string]any{"type": "sse", "url": "https://x"}
	stdio := map[string]any{"type": "stdio", "command": "node"}
	if appCanHostSpec("codex", sse) || appCanHostSpec("deepseek", sse) {
		t.Error("codex/deepseek must not host sse")
	}
	if !appCanHostSpec("codex", stdio) || !appCanHostSpec("gemini", sse) {
		t.Error("stdio on codex and sse on gemini must be allowed")
	}
}

func TestClaudeUpsertRemoveWithPluginGate(t *testing.T) {
	home := mcpTestHome(t)
	spec := map[string]any{"type": "stdio", "command": "node", "args": []any{"srv.js"}}
	if err := upsertServerForApp("claude_code", "foo", spec); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	// ~/.claude.json carries the canonical entry.
	cfg := readJSONObj(t, filepath.Join(home, ".claude.json"))
	servers := cfg["mcpServers"].(map[string]any)
	if _, ok := servers["foo"]; !ok {
		t.Fatalf("foo not written: %v", cfg)
	}
	// ~/.claude/settings.json gates it on with foo@local: true.
	settings := readJSONObj(t, filepath.Join(home, ".claude", "settings.json"))
	plugins := settings["enabledPlugins"].(map[string]any)
	if plugins["foo@local"] != true {
		t.Fatalf("plugin gate not set: %v", plugins)
	}
	// Scan surfaces it assigned to claude_code.
	scan := mcpScanLocal()
	srv := scan["servers"].([]map[string]any)
	if len(srv) != 1 || srv[0]["id"] != "foo" {
		t.Fatalf("scan = %v", srv)
	}
	// Remove clears both the entry and the gate.
	removed, err := removeServerForApp("claude_code", "foo")
	if err != nil || !removed {
		t.Fatalf("remove = %v, %v", removed, err)
	}
	settings = readJSONObj(t, filepath.Join(home, ".claude", "settings.json"))
	if _, had := settings["enabledPlugins"].(map[string]any)["foo@local"]; had {
		t.Error("plugin gate not cleared")
	}
}

func TestClineHttpRemap(t *testing.T) {
	home := mcpTestHome(t)
	if err := upsertServerForApp("cline", "web", map[string]any{"type": "http", "url": "https://api"}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	cfg := readJSONObj(t, filepath.Join(home, ".cline", "data", "settings", "cline_mcp_settings.json"))
	entry := cfg["mcpServers"].(map[string]any)["web"].(map[string]any)
	if entry["type"] != "streamableHttp" {
		t.Fatalf("cline type = %v, want streamableHttp", entry["type"])
	}
	// Reader collapses it back to canonical http.
	scan := mcpScanLocal()
	spec := scan["servers"].([]map[string]any)[0]["spec"].(map[string]any)
	if spec["type"] != "http" {
		t.Fatalf("scan spec type = %v, want http", spec["type"])
	}
}

func TestCursorEntryStripsType(t *testing.T) {
	home := mcpTestHome(t)
	if err := upsertServerForApp("cursor", "c", map[string]any{"type": "stdio", "command": "node"}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	entry := readJSONObj(t, filepath.Join(home, ".cursor", "mcp.json"))["mcpServers"].(map[string]any)["c"].(map[string]any)
	if _, had := entry["type"]; had {
		t.Errorf("cursor entry should not carry type: %v", entry)
	}
	if entry["command"] != "node" {
		t.Errorf("cursor command = %v", entry["command"])
	}
}

func TestUpsertStagedAgentErrors(t *testing.T) {
	mcpTestHome(t)
	// An unknown app (e.g. a not-yet-mapped custom agent) is refused on upsert
	// and is a silent no-op on removal, so it never aborts a multi-agent save.
	if err := upsertServerForApp("custom_unknown", "x", map[string]any{"type": "stdio", "command": "node"}); err == nil {
		t.Error("expected unsupported-agent upsert error")
	}
	if removed, err := removeServerForApp("custom_unknown", "x"); err != nil || removed {
		t.Errorf("unknown remove = %v, %v; want false, nil", removed, err)
	}
}

func TestMcpUpsertAndSetAppsHandlers(t *testing.T) {
	home := mcpTestHome(t)

	ctx := func(body map[string]any) *web.Context { return &web.Context{Body: body} }

	// Upsert foo to claude_code + gemini.
	res, err := mcpUpsertLocalServer(ctx(map[string]any{
		"serverId": "foo",
		"spec":     map[string]any{"type": "stdio", "command": "node"},
		"apps":     []any{"gemini", "claude_code"},
	}))
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	out := res.(map[string]any)
	apps := out["apps"].([]McpAppType)
	if len(apps) != 2 || apps[0] != "claude_code" || apps[1] != "gemini" {
		t.Fatalf("apps = %v, want [claude_code gemini]", apps)
	}
	if !fileExists(filepath.Join(home, ".gemini", "settings.json")) {
		t.Error("gemini config not written")
	}

	// Narrow to gemini only; claude's entry must be stripped.
	res, err = mcpSetServerApps(ctx(map[string]any{"serverId": "foo", "apps": []any{"gemini"}}))
	if err != nil {
		t.Fatalf("set_apps: %v", err)
	}
	claude := readJSONObj(t, filepath.Join(home, ".claude.json"))
	if servers, ok := claude["mcpServers"].(map[string]any); ok {
		if _, had := servers["foo"]; had {
			t.Error("foo should have been removed from claude")
		}
	}
	apps = res.(map[string]any)["apps"].([]McpAppType)
	if len(apps) != 1 || apps[0] != "gemini" {
		t.Fatalf("post set_apps apps = %v", apps)
	}

	// Remove entirely.
	rres, err := mcpRemoveServer(ctx(map[string]any{"serverId": "foo"}))
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	if rres != true {
		t.Fatalf("remove returned %v, want true", rres)
	}
	if findLocalServer("foo") != nil {
		t.Error("foo still present after remove")
	}
}
