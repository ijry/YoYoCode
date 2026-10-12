package handlers

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

func readYamlObj(t *testing.T, path string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var v any
	if err := yaml.Unmarshal(b, &v); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	m, _ := yamlNormalize(v).(map[string]any)
	return m
}

func hermesTestHome(t *testing.T) string {
	t.Helper()
	home := mcpTestHome(t)
	t.Setenv("HERMES_HOME", "")
	return home
}

func TestHermesStdioRoundTrip(t *testing.T) {
	home := hermesTestHome(t)
	err := upsertServerForApp("hermes", "foo", map[string]any{
		"type": "stdio", "command": "node", "args": []any{"x"}, "env": map[string]any{"K": "v"},
	})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	root := readYamlObj(t, filepath.Join(home, ".hermes", "config.yaml"))
	entry := root["mcp_servers"].(map[string]any)["foo"].(map[string]any)
	if entry["command"] != "node" {
		t.Errorf("command = %v", entry["command"])
	}
	if _, had := entry["type"]; had {
		t.Errorf("hermes stdio must not carry type: %v", entry)
	}
	servers, err := readHermesServersImpl()
	if err != nil {
		t.Fatal(err)
	}
	if servers["foo"]["type"] != "stdio" || servers["foo"]["command"] != "node" {
		t.Errorf("canonical = %v", servers["foo"])
	}
}

func TestHermesSseAndHttp(t *testing.T) {
	home := hermesTestHome(t)
	if err := upsertServerForApp("hermes", "e", map[string]any{"type": "sse", "url": "https://e"}); err != nil {
		t.Fatalf("sse: %v", err)
	}
	if err := upsertServerForApp("hermes", "h", map[string]any{"type": "http", "url": "https://h"}); err != nil {
		t.Fatalf("http: %v", err)
	}
	servers := readYamlObj(t, filepath.Join(home, ".hermes", "config.yaml"))["mcp_servers"].(map[string]any)
	// SSE is encoded via transport: sse; HTTP has none.
	if servers["e"].(map[string]any)["transport"] != "sse" {
		t.Errorf("sse entry = %v", servers["e"])
	}
	if _, had := servers["h"].(map[string]any)["transport"]; had {
		t.Errorf("http entry should omit transport: %v", servers["h"])
	}
	canon, _ := readHermesServersImpl()
	if canon["e"]["type"] != "sse" || canon["h"]["type"] != "http" {
		t.Errorf("hermes canonical types = %v / %v", canon["e"]["type"], canon["h"]["type"])
	}
}

func TestHermesPassthroughAndRemove(t *testing.T) {
	hermesTestHome(t)
	// client_cert is a Hermes passthrough key preserved on write.
	if err := upsertServerForApp("hermes", "m", map[string]any{"type": "http", "url": "https://m", "client_cert": "/p/cert"}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	canon, _ := readHermesServersImpl()
	if canon["m"]["client_cert"] != "/p/cert" {
		t.Errorf("client_cert passthrough = %v", canon["m"])
	}
	removed, err := removeHermesServer("m")
	if err != nil || !removed {
		t.Fatalf("remove = %v, %v", removed, err)
	}
	if canon2, _ := readHermesServersImpl(); len(canon2) != 0 {
		t.Errorf("expected empty after remove, got %v", canon2)
	}
}
