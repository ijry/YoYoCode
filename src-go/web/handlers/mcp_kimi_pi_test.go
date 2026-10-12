package handlers

import (
	"path/filepath"
	"testing"
)

func TestKimiEntryToCanonical(t *testing.T) {
	// Explicit transport discriminant sets the canonical type; on-disk type dropped.
	got, err := kimiEntryToCanonical(map[string]any{"transport": "sse", "url": "https://x", "type": "ignored"}, "s")
	if err != nil || got["type"] != "sse" || got["url"] != "https://x" {
		t.Fatalf("sse entry = %v, %v", got, err)
	}
	// No transport → inferred from command.
	got, _ = kimiEntryToCanonical(map[string]any{"command": "node"}, "s")
	if got["type"] != "stdio" {
		t.Errorf("inferred = %v", got)
	}
	// Unknown transport literal is rejected.
	if _, err := kimiEntryToCanonical(map[string]any{"transport": "bogus", "url": "https://x"}, "s"); err == nil {
		t.Error("expected unsupported-transport error")
	}
}

func TestCanonicalToKimiEntry(t *testing.T) {
	// Remote: keep auth (valid), drop runtime_id (stdio-only), emit transport.
	got, err := canonicalToKimiEntry(map[string]any{"type": "http", "url": "https://x", "auth": "oauth", "runtime_id": "r"})
	if err != nil {
		t.Fatal(err)
	}
	if got["transport"] != "http" || got["auth"] != "oauth" {
		t.Errorf("remote entry = %v", got)
	}
	if _, had := got["runtime_id"]; had {
		t.Errorf("runtime_id must be dropped for remote: %v", got)
	}
	// Stdio: keep runtime_id (valid), drop auth (remote-only), no transport.
	got, _ = canonicalToKimiEntry(map[string]any{"type": "stdio", "command": "node", "runtime_id": "node", "auth": "oauth"})
	if got["runtime_id"] != "node" {
		t.Errorf("runtime_id should be kept for stdio: %v", got)
	}
	if _, had := got["auth"]; had {
		t.Errorf("auth must be dropped for stdio: %v", got)
	}
	if _, had := got["transport"]; had {
		t.Errorf("stdio needs no transport: %v", got)
	}
}

func TestMergeKimiExtensionFields(t *testing.T) {
	canonical := map[string]any{"type": "http", "url": "https://x", "auth": "stale"}
	kimi := map[string]any{"auth": "oauth", "bearerTokenEnvVar": "TOK"}
	owner := map[string]any{"auth": "oauth"} // what OpenClaw declared
	mergeKimiExtensionFields(canonical, kimi, owner)
	if canonical["auth"] != "oauth" {
		t.Errorf("shared auth should come from owner: %v", canonical["auth"])
	}
	if canonical["bearerTokenEnvVar"] != "TOK" {
		t.Errorf("owned key should come from kimi: %v", canonical["bearerTokenEnvVar"])
	}
}

func TestPiAndKimiRoundTrip(t *testing.T) {
	home := mcpTestHome(t)
	t.Setenv("PI_CODING_AGENT_DIR", "")
	t.Setenv("KIMI_CODE_HOME", "")

	if err := upsertServerForApp("pi", "foo", map[string]any{"type": "stdio", "command": "node"}); err != nil {
		t.Fatalf("pi upsert: %v", err)
	}
	if err := upsertServerForApp("kimi_code", "foo", map[string]any{"type": "http", "url": "https://x", "auth": "oauth"}); err != nil {
		t.Fatalf("kimi upsert: %v", err)
	}
	// pi config is Claude-shaped at ~/.pi/agent/mcp.json.
	piCfg := readJSONObj(t, filepath.Join(home, ".pi", "agent", "mcp.json"))
	if _, ok := piCfg["mcpServers"].(map[string]any)["foo"]; !ok {
		t.Fatalf("pi foo not written: %v", piCfg)
	}
	// Kimi config carries the transport discriminant.
	kimiCfg := readJSONObj(t, filepath.Join(home, ".kimi-code", "mcp.json"))
	entry := kimiCfg["mcpServers"].(map[string]any)["foo"].(map[string]any)
	if entry["transport"] != "http" || entry["auth"] != "oauth" {
		t.Fatalf("kimi entry = %v", entry)
	}

	// Both agents hold id "foo"; scan merges apps (sorted by enum order:
	// kimi_code before pi) and the kimi-only auth field survives.
	scan := mcpScanLocal()
	servers := scan["servers"].([]map[string]any)
	if len(servers) != 1 || servers[0]["id"] != "foo" {
		t.Fatalf("scan = %v", servers)
	}
	apps := servers[0]["apps"].([]McpAppType)
	if len(apps) != 2 || apps[0] != "kimi_code" || apps[1] != "pi" {
		t.Fatalf("apps = %v, want [kimi_code pi]", apps)
	}

	// Remove from pi only; kimi keeps it.
	if _, err := removeServerForApp("pi", "foo"); err != nil {
		t.Fatalf("pi remove: %v", err)
	}
	if findLocalServer("foo") == nil {
		t.Error("foo should still exist via kimi")
	}
}
