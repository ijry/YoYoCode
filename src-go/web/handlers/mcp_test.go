package handlers

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNormalizeMCPType(t *testing.T) {
	cases := map[string]string{
		"stdio": "stdio", "  HTTP ": "http", "sse": "sse",
		"local": "local", "remote": "remote",
		"streamable-http": "http", "streamableHttp": "http", "Streamable HTTP": "http",
		"streamable_http": "http", "bogus": "", "": "",
	}
	for in, want := range cases {
		if got := normalizeMCPType(in); got != want {
			t.Errorf("normalizeMCPType(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCanonicalizeSpecStdio(t *testing.T) {
	got, err := canonicalizeSpec(map[string]any{
		"type":    "stdio",
		"command": " node ",
		"args":    []any{" a ", "", "b"},
		"env":     map[string]any{"K": " v ", "empty": ""},
		"cwd":     " /tmp ",
		"extra":   "keep",
		"nil":     nil,
	}, "test")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got["command"] != "node" {
		t.Errorf("command = %v", got["command"])
	}
	args := got["args"].([]any)
	if len(args) != 2 || args[0] != "a" || args[1] != "b" {
		t.Errorf("args = %v", args)
	}
	env := got["env"].(map[string]any)
	if env["K"] != "v" || len(env) != 1 {
		t.Errorf("env = %v", env)
	}
	if got["cwd"] != "/tmp" {
		t.Errorf("cwd = %v", got["cwd"])
	}
	if got["extra"] != "keep" {
		t.Errorf("extra not preserved: %v", got["extra"])
	}
	if _, ok := got["nil"]; ok {
		t.Errorf("null value should be dropped")
	}
}

func TestCanonicalizeSpecRemoteAndInference(t *testing.T) {
	// http explicit
	got, err := canonicalizeSpec(map[string]any{"type": "streamable-http", "url": "https://x", "headers": map[string]any{"A": "b"}}, "t")
	if err != nil || got["type"] != "http" || got["url"] != "https://x" {
		t.Fatalf("http canon = %v, %v", got, err)
	}
	// type inference: command => stdio
	got, _ = canonicalizeSpec(map[string]any{"command": "node"}, "t")
	if got["type"] != "stdio" {
		t.Errorf("inferred type = %v, want stdio", got["type"])
	}
	// type inference: url => http
	got, _ = canonicalizeSpec(map[string]any{"url": "https://y"}, "t")
	if got["type"] != "http" {
		t.Errorf("inferred type = %v, want http", got["type"])
	}
	// missing everything => error
	if _, err := canonicalizeSpec(map[string]any{}, "t"); err == nil {
		t.Error("expected missing-type error")
	}
	// stdio without command => error
	if _, err := canonicalizeSpec(map[string]any{"type": "stdio"}, "t"); err == nil {
		t.Error("expected missing-command error")
	}
}

func TestCanonicalizeOpencodeSpec(t *testing.T) {
	// local with command array
	got, err := canonicalizeSpec(map[string]any{"type": "local", "command": []any{"node", "server.js"}, "environment": map[string]any{"K": "v"}}, "t")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got["type"] != "stdio" || got["command"] != "node" {
		t.Errorf("local => %v", got)
	}
	if args := got["args"].([]any); len(args) != 1 || args[0] != "server.js" {
		t.Errorf("args = %v", got["args"])
	}
	if env := got["env"].(map[string]any); env["K"] != "v" {
		t.Errorf("env = %v", got["env"])
	}
	// remote with transport sse
	got, _ = canonicalizeSpec(map[string]any{"type": "remote", "transport": "sse", "url": "https://z"}, "t")
	if got["type"] != "sse" || got["url"] != "https://z" {
		t.Errorf("remote => %v", got)
	}
}

func TestMcpListMarketplaces(t *testing.T) {
	list := mcpListMarketplaces().([]map[string]any)
	if len(list) != 2 || list[0]["id"] != "official_registry" || list[1]["id"] != "smithery" {
		t.Fatalf("providers = %v", list)
	}
}

func writeFileTree(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestMcpScanLocalMergesAndWarns(t *testing.T) {
	home := t.TempDir()
	old := mcpHomeOverride
	mcpHomeOverride = home
	defer func() { mcpHomeOverride = old }()
	// Isolate env-based paths that could leak into the temp home's agents.
	for _, k := range []string{"DSH_HOME", "QODER_CONFIG_DIR", "QODER_CLI_HOME", "GEMINI_CLI_HOME", "QODER_CONFIG_DIR_NAME", "GEMINI_HOME"} {
		t.Setenv(k, "")
	}

	// Claude declares foo (stdio) and bar (http).
	writeFileTree(t, filepath.Join(home, ".claude.json"),
		`{"mcpServers":{"foo":{"type":"stdio","command":"node","args":["x"]},"bar":{"type":"http","url":"https://b"}}}`)
	// Cursor also declares foo (shape-based, no type) → apps union with claude.
	writeFileTree(t, filepath.Join(home, ".cursor", "mcp.json"),
		`{"mcpServers":{"foo":{"command":"node","args":["x"]}}}`)
	// Gemini config is malformed → a warning, not a scan failure.
	writeFileTree(t, filepath.Join(home, ".gemini", "settings.json"), "{ this is not json")

	scan := mcpScanLocal()
	servers := scan["servers"].([]map[string]any)
	warnings := scan["warnings"].([]map[string]any)

	if len(servers) != 2 {
		t.Fatalf("expected 2 servers (foo, bar), got %d: %v", len(servers), servers)
	}
	// Sorted by id: bar, foo.
	if servers[0]["id"] != "bar" || servers[1]["id"] != "foo" {
		t.Fatalf("server order = %v", []any{servers[0]["id"], servers[1]["id"]})
	}
	fooApps := servers[1]["apps"].([]McpAppType)
	if len(fooApps) != 2 || fooApps[0] != "claude_code" || fooApps[1] != "cursor" {
		t.Errorf("foo apps = %v, want [claude_code cursor]", fooApps)
	}
	barApps := servers[0]["apps"].([]McpAppType)
	if len(barApps) != 1 || barApps[0] != "claude_code" {
		t.Errorf("bar apps = %v", barApps)
	}
	// Gemini's malformed file produced exactly one warning naming the app.
	found := false
	for _, w := range warnings {
		if w["app"] == "gemini" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a gemini warning, got %v", warnings)
	}
}
