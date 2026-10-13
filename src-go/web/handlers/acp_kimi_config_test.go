package handlers_test

// acp_kimi_config_test.go covers acp_update_kimi_code_config: the three modes
// (apikey / login / raw), the managed config.toml block (preserving user
// sections), the gate credential (seeded, never clobbering a real login), the
// reasoning metadata, and the KIMI_MODEL_* env-override clearing.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	toml "github.com/pelletier/go-toml/v2"
)

// kimiHome points KIMI_CODE_HOME at a fresh temp dir and returns it.
func kimiHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("KIMI_CODE_HOME", dir)
	return dir
}

func readKimiToml(t *testing.T, dir string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "config.toml"))
	if err != nil {
		t.Fatalf("read config.toml: %v", err)
	}
	var m map[string]any
	if err := toml.Unmarshal(b, &m); err != nil {
		t.Fatalf("parse config.toml: %v", err)
	}
	return m
}

func readKimiCredential(t *testing.T, dir string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "credentials", "kimi-code.json"))
	if err != nil {
		t.Fatalf("read credential: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("parse credential: %v", err)
	}
	return m
}

func TestKimiConfigApiKeyWritesManagedBlock(t *testing.T) {
	dir := kimiHome(t)
	// A user-authored section must survive the merge-write.
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte("[providers.mine]\ntype = \"openai\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	srv := newTestServer(t)
	code, out := call(t, srv, "acp_update_kimi_code_config", map[string]any{
		"mode": "apikey", "interfaceType": "openai", "apiKey": "sk-1",
		"model": "gpt-4o", "baseUrl": "https://api.x/v1",
	})
	if code != 200 {
		t.Fatalf("status %d: %v", code, out)
	}
	if out["id"] != nil {
		t.Fatalf("unexpected payload %v", out)
	}

	root := readKimiToml(t, dir)
	providers := root["providers"].(map[string]any)
	managed := providers["yoyocode"].(map[string]any)
	if managed["type"] != "openai" || managed["api_key"] != "sk-1" || managed["base_url"] != "https://api.x/v1" {
		t.Fatalf("managed provider = %v", managed)
	}
	// The user's own provider is untouched.
	if _, ok := providers["mine"]; !ok {
		t.Fatalf("user provider dropped: %v", providers)
	}
	models := root["models"].(map[string]any)
	mm := models["yoyocode-managed"].(map[string]any)
	if mm["model"] != "gpt-4o" {
		t.Fatalf("managed model = %v", mm)
	}
	// Kimi requires a positive max_context_size; the default must be written.
	if ctx, _ := mm["max_context_size"].(int64); ctx != 262144 {
		t.Fatalf("max_context_size = %v (%T)", mm["max_context_size"], mm["max_context_size"])
	}
	if root["default_model"] != "yoyocode-managed" {
		t.Fatalf("default_model = %v", root["default_model"])
	}

	cred := readKimiCredential(t, dir)
	if cred["access_token"] != "yoyocode-local-gate" || cred["_yoyocode_synthetic"] != true {
		t.Fatalf("credential = %v", cred)
	}
}

func TestKimiConfigReasoningMetadata(t *testing.T) {
	dir := kimiHome(t)
	srv := newTestServer(t)
	code, out := call(t, srv, "acp_update_kimi_code_config", map[string]any{
		"mode": "apikey", "interfaceType": "kimi", "apiKey": "k", "model": "m",
		"reasoningEnabled": true, "supportEfforts": []string{"low", "high", "low"},
		"defaultEffort": "medium", // not in supportEfforts → dropped
	})
	if code != 200 {
		t.Fatalf("status %d: %v", code, out)
	}
	mm := readKimiToml(t, dir)["models"].(map[string]any)["yoyocode-managed"].(map[string]any)
	caps := mm["capabilities"].([]any)
	if len(caps) != 4 || caps[0] != "thinking" || caps[1] != "image_in" || caps[2] != "video_in" || caps[3] != "tool_use" {
		t.Fatalf("capabilities = %v", caps)
	}
	efforts := mm["support_efforts"].([]any)
	if len(efforts) != 2 || efforts[0] != "low" || efforts[1] != "high" {
		t.Fatalf("support_efforts = %v", efforts)
	}
	if _, ok := mm["default_effort"]; ok {
		t.Fatalf("unlisted default_effort must be dropped, got %v", mm["default_effort"])
	}

	// always_thinking swaps the first capability.
	if code, _ := call(t, srv, "acp_update_kimi_code_config", map[string]any{
		"mode": "apikey", "interfaceType": "kimi", "apiKey": "k", "model": "m",
		"reasoningEnabled": true, "alwaysThinking": true,
	}); code != 200 {
		t.Fatalf("status %d", code)
	}
	caps = readKimiToml(t, dir)["models"].(map[string]any)["yoyocode-managed"].(map[string]any)["capabilities"].([]any)
	if caps[0] != "always_thinking" {
		t.Fatalf("capabilities = %v", caps)
	}
}

func TestKimiConfigVertexUsesEnvSubTable(t *testing.T) {
	dir := kimiHome(t)
	srv := newTestServer(t)
	code, out := call(t, srv, "acp_update_kimi_code_config", map[string]any{
		"mode": "apikey", "interfaceType": "vertexai", "model": "gemini-x",
		"vertexProject": "proj-1", "vertexLocation": "us-central1",
	})
	if code != 200 {
		t.Fatalf("status %d: %v", code, out)
	}
	managed := readKimiToml(t, dir)["providers"].(map[string]any)["yoyocode"].(map[string]any)
	if _, ok := managed["api_key"]; ok {
		t.Fatalf("vertex must not write api_key: %v", managed)
	}
	env := managed["env"].(map[string]any)
	if env["GOOGLE_CLOUD_PROJECT"] != "proj-1" || env["GOOGLE_CLOUD_LOCATION"] != "us-central1" {
		t.Fatalf("vertex env = %v", env)
	}
}

func TestKimiConfigEnvAuthUsesKeyVar(t *testing.T) {
	dir := kimiHome(t)
	srv := newTestServer(t)
	code, out := call(t, srv, "acp_update_kimi_code_config", map[string]any{
		"mode": "apikey", "interfaceType": "anthropic", "authType": "env",
		"apiKey": "sk-a", "model": "claude-x",
	})
	if code != 200 {
		t.Fatalf("status %d: %v", code, out)
	}
	managed := readKimiToml(t, dir)["providers"].(map[string]any)["yoyocode"].(map[string]any)
	if _, ok := managed["api_key"]; ok {
		t.Fatalf("env auth must not write inline api_key: %v", managed)
	}
	if managed["env"].(map[string]any)["ANTHROPIC_API_KEY"] != "sk-a" {
		t.Fatalf("env = %v", managed["env"])
	}
}

func TestKimiConfigLoginClearsAndKeepsRealToken(t *testing.T) {
	dir := kimiHome(t)
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte("[providers.mine]\ntype = \"openai\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	srv := newTestServer(t)
	if code, out := call(t, srv, "acp_update_kimi_code_config", map[string]any{
		"mode": "apikey", "interfaceType": "kimi", "apiKey": "k", "model": "m",
	}); code != 200 {
		t.Fatalf("seed apikey: %d %v", code, out)
	}
	// login clears the managed block + removes OUR token.
	if code, out := call(t, srv, "acp_update_kimi_code_config", map[string]any{"mode": "login"}); code != 200 {
		t.Fatalf("login: %d %v", code, out)
	}
	root := readKimiToml(t, dir)
	if _, ok := root["providers"].(map[string]any)["yoyocode"]; ok {
		t.Fatalf("managed provider not cleared: %v", root["providers"])
	}
	if _, ok := root["models"]; ok {
		t.Fatalf("models table should be gone when empty: %v", root["models"])
	}
	if _, ok := root["default_model"]; ok {
		t.Fatalf("default_model should be cleared: %v", root["default_model"])
	}
	if _, ok := root["providers"].(map[string]any)["mine"]; !ok {
		t.Fatalf("user provider dropped: %v", root["providers"])
	}
	if _, err := os.Stat(filepath.Join(dir, "credentials", "kimi-code.json")); !os.IsNotExist(err) {
		t.Fatalf("synthetic credential should be removed, stat err = %v", err)
	}

	// A REAL login token must never be removed by login mode.
	realDir := filepath.Join(dir, "credentials")
	if err := os.MkdirAll(realDir, 0o755); err != nil {
		t.Fatal(err)
	}
	real := `{"access_token":"real-token","token_type":"Bearer"}`
	if err := os.WriteFile(filepath.Join(realDir, "kimi-code.json"), []byte(real), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, out := call(t, srv, "acp_update_kimi_code_config", map[string]any{"mode": "login"}); code != 200 {
		t.Fatalf("login(real): %d %v", code, out)
	}
	if _, err := os.Stat(filepath.Join(realDir, "kimi-code.json")); err != nil {
		t.Fatalf("real login token must survive, err = %v", err)
	}

	// Seeding must also preserve a real token.
	if code, out := call(t, srv, "acp_update_kimi_code_config", map[string]any{
		"mode": "apikey", "interfaceType": "kimi", "apiKey": "k", "model": "m",
	}); code != 200 {
		t.Fatalf("apikey(real): %d %v", code, out)
	}
	if got := readKimiCredential(t, dir)["access_token"]; got != "real-token" {
		t.Fatalf("real token clobbered by seed: %v", got)
	}
}

func TestKimiConfigRawMode(t *testing.T) {
	dir := kimiHome(t)
	srv := newTestServer(t)
	raw := "[providers.raw]\ntype = \"openai\"\nbase_url = \"https://r\"\n"
	if code, out := call(t, srv, "acp_update_kimi_code_config", map[string]any{
		"mode": "raw", "rawConfigToml": raw,
	}); code != 200 {
		t.Fatalf("raw: %d %v", code, out)
	}
	b, err := os.ReadFile(filepath.Join(dir, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != raw {
		t.Fatalf("raw config not verbatim:\n%q", string(b))
	}
	if got := readKimiCredential(t, dir)["access_token"]; got != "yoyocode-local-gate" {
		t.Fatalf("raw mode should seed the gate token, got %v", got)
	}

	// Invalid TOML is rejected before any write.
	if code, _ := call(t, srv, "acp_update_kimi_code_config", map[string]any{
		"mode": "raw", "rawConfigToml": "this is not = = toml",
	}); code != 500 {
		t.Fatalf("invalid raw TOML should fail, got %d", code)
	}
}

func TestKimiConfigValidation(t *testing.T) {
	kimiHome(t)
	srv := newTestServer(t)
	cases := []map[string]any{
		{"mode": "apikey", "interfaceType": "bogus", "model": "m"},
		{"mode": "apikey", "interfaceType": "kimi", "model": ""},
		{"mode": "nope"},
	}
	for _, c := range cases {
		if code, out := call(t, srv, "acp_update_kimi_code_config", c); code != 500 {
			t.Errorf("payload %v: expected 500, got %d %v", c, code, out)
		}
	}
}

func TestKimiConfigClearsModelEnvOverride(t *testing.T) {
	kimiHome(t)
	srv, st := newTestServerState(t)
	if _, err := st.DB.Conn.Exec(
		`INSERT INTO agent_setting (agent_type, registry_id, enabled, env_json, created_at, updated_at) VALUES (?,?,?,?,?,?)`,
		"kimi_code", "kimi-code", 1, `{"KIMI_MODEL_API_KEY":"stale","FOO":"bar"}`, "t", "t",
	); err != nil {
		t.Fatalf("seed agent_setting: %v", err)
	}
	if code, out := call(t, srv, "acp_update_kimi_code_config", map[string]any{
		"mode": "apikey", "interfaceType": "kimi", "apiKey": "k", "model": "m",
	}); code != 200 {
		t.Fatalf("status %d: %v", code, out)
	}
	var envJSON string
	if err := st.DB.Conn.QueryRow(`SELECT env_json FROM agent_setting WHERE agent_type = ?`, "kimi_code").Scan(&envJSON); err != nil {
		t.Fatalf("read env_json: %v", err)
	}
	var env map[string]any
	if err := json.Unmarshal([]byte(envJSON), &env); err != nil {
		t.Fatalf("parse env_json: %v", err)
	}
	if _, ok := env["KIMI_MODEL_API_KEY"]; ok {
		t.Fatalf("KIMI_MODEL_API_KEY not cleared: %v", env)
	}
	if env["FOO"] != "bar" {
		t.Fatalf("unrelated env key dropped: %v", env)
	}
}
