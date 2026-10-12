package handlers

import (
	"os"
	"path/filepath"
	"testing"

	toml "github.com/pelletier/go-toml/v2"
)

func readTomlObj(t *testing.T, path string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var v any
	if err := toml.Unmarshal(b, &v); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	m, _ := v.(map[string]any)
	return m
}

func tomlTestHome(t *testing.T) string {
	t.Helper()
	home := mcpTestHome(t)
	t.Setenv("GROK_HOME", "")
	return home
}

func TestCodexStdioRoundTrip(t *testing.T) {
	home := tomlTestHome(t)
	err := upsertServerForApp("codex", "foo", map[string]any{
		"type": "stdio", "command": "node", "args": []any{"x"}, "env": map[string]any{"K": "v"},
	})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	root := readTomlObj(t, filepath.Join(home, ".codex", "config.toml"))
	entry := root["mcp_servers"].(map[string]any)["foo"].(map[string]any)
	if entry["command"] != "node" {
		t.Errorf("command = %v", entry["command"])
	}
	if _, had := entry["type"]; had {
		t.Errorf("codex must not write a type key: %v", entry)
	}
	// Scan reads it back as canonical stdio.
	servers, err := readCodexServersImpl()
	if err != nil {
		t.Fatal(err)
	}
	if servers["foo"]["type"] != "stdio" || servers["foo"]["command"] != "node" {
		t.Errorf("canonical = %v", servers["foo"])
	}
}

func TestCodexHttpAndSse(t *testing.T) {
	home := tomlTestHome(t)
	if err := upsertServerForApp("codex", "web", map[string]any{"type": "http", "url": "https://api", "headers": map[string]any{"A": "b"}}); err != nil {
		t.Fatalf("http upsert: %v", err)
	}
	entry := readTomlObj(t, filepath.Join(home, ".codex", "config.toml"))["mcp_servers"].(map[string]any)["web"].(map[string]any)
	if entry["url"] != "https://api" {
		t.Errorf("url = %v", entry["url"])
	}
	if _, had := entry["http_headers"]; !had {
		t.Errorf("codex remote should write http_headers: %v", entry)
	}
	// SSE cannot be represented in Codex config — the writer rejects it.
	if err := upsertServerForApp("codex", "s", map[string]any{"type": "sse", "url": "https://sse"}); err == nil {
		t.Error("expected codex SSE rejection")
	}
}

func TestCodexLegacyServersRead(t *testing.T) {
	home := tomlTestHome(t)
	cfg := "[mcp.servers.old]\ncommand = \"legacy\"\n"
	path := filepath.Join(home, ".codex", "config.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	servers, err := readCodexServersImpl()
	if err != nil {
		t.Fatal(err)
	}
	if servers["old"]["command"] != "legacy" {
		t.Errorf("legacy mcp.servers not read: %v", servers)
	}
}

func TestGrokSseHttpInference(t *testing.T) {
	home := tomlTestHome(t)
	if err := upsertServerForApp("grok", "e", map[string]any{"type": "sse", "url": "https://e"}); err != nil {
		t.Fatalf("sse upsert: %v", err)
	}
	if err := upsertServerForApp("grok", "h", map[string]any{"type": "http", "url": "https://h"}); err != nil {
		t.Fatalf("http upsert: %v", err)
	}
	root := readTomlObj(t, filepath.Join(home, ".grok", "config.toml"))["mcp_servers"].(map[string]any)
	// SSE carries an explicit type; HTTP omits it.
	if root["e"].(map[string]any)["type"] != "sse" {
		t.Errorf("sse entry = %v", root["e"])
	}
	if _, had := root["h"].(map[string]any)["type"]; had {
		t.Errorf("http entry should omit type: %v", root["h"])
	}
	servers, _ := readGrokServersImpl()
	if servers["e"]["type"] != "sse" || servers["h"]["type"] != "http" {
		t.Errorf("grok canonical types = %v / %v", servers["e"]["type"], servers["h"]["type"])
	}
}

func TestGrokPassthrough(t *testing.T) {
	home := tomlTestHome(t)
	if err := upsertServerForApp("grok", "p", map[string]any{"type": "stdio", "command": "node", "enabled": true}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	entry := readTomlObj(t, filepath.Join(home, ".grok", "config.toml"))["mcp_servers"].(map[string]any)["p"].(map[string]any)
	if entry["enabled"] != true {
		t.Errorf("grok passthrough enabled = %v", entry["enabled"])
	}
}
