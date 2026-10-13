package handlers_test

// acp_hermes_config_test.go covers acp_update_hermes_config: the structured
// config.yaml `model:` merge, the `.env` key/endpoint mirroring (patch semantics
// that preserve comments/order and drop duplicates), the `custom` inline key, raw
// mode never touching `.env`, and provider-switch cleanup.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// hermesHome points HERMES_HOME at a fresh temp dir and returns it.
func hermesHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HERMES_HOME", dir)
	return dir
}

func readHermesYAML(t *testing.T, dir string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "config.yaml"))
	if err != nil {
		t.Fatalf("read config.yaml: %v", err)
	}
	var m map[string]any
	if err := yaml.Unmarshal(b, &m); err != nil {
		t.Fatalf("parse config.yaml: %v", err)
	}
	return m
}

func hermesModel(t *testing.T, dir string) map[string]any {
	t.Helper()
	m, _ := readHermesYAML(t, dir)["model"].(map[string]any)
	return m
}

func readHermesEnv(t *testing.T, dir string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, ".env"))
	if err != nil {
		t.Fatalf("read .env: %v", err)
	}
	return string(b)
}

func TestHermesConfigStructuredWrite(t *testing.T) {
	dir := hermesHome(t)
	// Pre-existing unrelated keys must survive both files.
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("theme: dark\nmodel:\n  provider: anthropic\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("# my key\nANTHROPIC_API_KEY=old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	srv := newTestServer(t)
	code, out := call(t, srv, "acp_update_hermes_config", map[string]any{
		"provider": "openai-api", "apiKey": "sk-new", "model": "gpt-4o",
		"baseUrl": "https://api.openai.com/v1",
	})
	if code != 200 {
		t.Fatalf("status %d: %v", code, out)
	}

	root := readHermesYAML(t, dir)
	if root["theme"] != "dark" {
		t.Fatalf("unrelated yaml key dropped: %v", root)
	}
	model := hermesModel(t, dir)
	if model["provider"] != "openai-api" || model["default"] != "gpt-4o" || model["base_url"] != "https://api.openai.com/v1" {
		t.Fatalf("model = %v", model)
	}
	env := readHermesEnv(t, dir)
	if !strings.Contains(env, "# my key\n") {
		t.Fatalf("comment dropped from .env:\n%s", env)
	}
	if !strings.Contains(env, "OPENAI_API_KEY=sk-new") || !strings.Contains(env, "OPENAI_BASE_URL=https://api.openai.com/v1") {
		t.Fatalf(".env missing mirrored values:\n%s", env)
	}
	if !strings.Contains(env, "ANTHROPIC_API_KEY=old") {
		t.Fatalf("unrelated .env key dropped:\n%s", env)
	}
}

func TestHermesConfigEnvPatchReplacesFirstAndDropsDuplicates(t *testing.T) {
	dir := hermesHome(t)
	env := "# head\n\nOPENROUTER_API_KEY=first\nexport OPENROUTER_API_KEY=dup\nOTHER=keep\n"
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte(env), 0o644); err != nil {
		t.Fatal(err)
	}
	srv := newTestServer(t)
	if code, out := call(t, srv, "acp_update_hermes_config", map[string]any{
		"provider": "openrouter", "apiKey": "fresh", "model": "m",
	}); code != 200 {
		t.Fatalf("status %d: %v", code, out)
	}
	got := readHermesEnv(t, dir)
	if strings.Contains(got, "dup") || strings.Contains(got, "first") {
		t.Fatalf("duplicate/first occurrence not cleaned:\n%s", got)
	}
	if !strings.Contains(got, "OPENROUTER_API_KEY=fresh") {
		t.Fatalf("key not rewritten:\n%s", got)
	}
	if !strings.Contains(got, "# head") || !strings.Contains(got, "OTHER=keep") {
		t.Fatalf("comments/unrelated keys dropped:\n%s", got)
	}
	// openrouter must mask the OpenAI fallback vars with explicit empties.
	if !strings.Contains(got, "OPENAI_API_KEY=\n") || !strings.Contains(got, "OPENAI_BASE_URL=\n") {
		t.Fatalf("openai fallback vars not masked:\n%s", got)
	}
}

func TestHermesConfigCustomInlineKey(t *testing.T) {
	dir := hermesHome(t)
	// A prior provider left a stale inline key + api_mode.
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("model:\n  provider: anthropic\n  api_key: stale\n  api_mode: anthropic_messages\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	srv := newTestServer(t)
	if code, out := call(t, srv, "acp_update_hermes_config", map[string]any{
		"provider": "custom", "apiKey": "inline-key", "model": "m", "baseUrl": "http://localhost:1234/v1",
	}); code != 200 {
		t.Fatalf("status %d: %v", code, out)
	}
	model := hermesModel(t, dir)
	if model["api_key"] != "inline-key" || model["base_url"] != "http://localhost:1234/v1" {
		t.Fatalf("custom inline key/base_url = %v", model)
	}
	// Switching TO custom scrubs a stale api_mode.
	if _, ok := model["api_mode"]; ok {
		t.Fatalf("stale api_mode not scrubbed: %v", model)
	}
	// `custom` has no `.env` key var → no .env file written.
	if _, err := os.Stat(filepath.Join(dir, ".env")); !os.IsNotExist(err) {
		t.Fatalf(".env should not be created for custom, err = %v", err)
	}
}

func TestHermesConfigRawModeSkipsEnv(t *testing.T) {
	dir := hermesHome(t)
	raw := "model:\n  provider: bedrock\n"
	srv := newTestServer(t)
	if code, out := call(t, srv, "acp_update_hermes_config", map[string]any{
		"provider": "openrouter", "apiKey": "should-not-write", "rawConfigYaml": raw,
	}); code != 200 {
		t.Fatalf("status %d: %v", code, out)
	}
	b, err := os.ReadFile(filepath.Join(dir, "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != raw {
		t.Fatalf("raw config not verbatim:\n%q", string(b))
	}
	// Raw mode never touches `.env`, even when a key is present in the payload.
	if _, err := os.Stat(filepath.Join(dir, ".env")); !os.IsNotExist(err) {
		t.Fatalf("raw mode must not write .env, err = %v", err)
	}
	// Invalid YAML is rejected before any write.
	if code, _ := call(t, srv, "acp_update_hermes_config", map[string]any{
		"provider": "openrouter", "rawConfigYaml": "a: [unclosed",
	}); code != 500 {
		t.Fatalf("invalid raw YAML should fail, got %d", code)
	}
}

func TestHermesConfigProviderSwitchClearsStaleBaseURL(t *testing.T) {
	dir := hermesHome(t)
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("model:\n  provider: openai-api\n  base_url: https://proxy.example/v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	srv := newTestServer(t)
	// Switching to anthropic (endpoint not panel-exposed) must CLEAR the stale
	// base URL left by the previous provider.
	if code, out := call(t, srv, "acp_update_hermes_config", map[string]any{
		"provider": "anthropic", "apiKey": "sk-a", "model": "claude-x",
	}); code != 200 {
		t.Fatalf("status %d: %v", code, out)
	}
	model := hermesModel(t, dir)
	if _, ok := model["base_url"]; ok {
		t.Fatalf("stale base_url not cleared on provider switch: %v", model)
	}
	// Saving the SAME provider again preserves an out-of-band base URL.
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("model:\n  provider: anthropic\n  base_url: https://oob.example/v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, _ := call(t, srv, "acp_update_hermes_config", map[string]any{
		"provider": "anthropic", "apiKey": "sk-a", "model": "claude-x",
	}); code != 200 {
		t.Fatalf("status %d", code)
	}
	if got := hermesModel(t, dir)["base_url"]; got != "https://oob.example/v1" {
		t.Fatalf("same-provider save must preserve base_url, got %v", got)
	}
}

func TestHermesConfigBlankBaseURLClearsStaleEnv(t *testing.T) {
	dir := hermesHome(t)
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("model:\n  provider: openai-api\n  base_url: https://old/v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("OPENAI_BASE_URL=https://old/v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	srv := newTestServer(t)
	if code, out := call(t, srv, "acp_update_hermes_config", map[string]any{
		"provider": "openai-api", "apiKey": "k", "model": "m", "baseUrl": "",
	}); code != 200 {
		t.Fatalf("status %d: %v", code, out)
	}
	if _, ok := hermesModel(t, dir)["base_url"]; ok {
		t.Fatalf("blank base URL must remove model.base_url")
	}
	if got := readHermesEnv(t, dir); !strings.Contains(got, "OPENAI_BASE_URL=\n") {
		t.Fatalf("stale OPENAI_BASE_URL not cleared:\n%s", got)
	}
}

func TestHermesConfigValidation(t *testing.T) {
	hermesHome(t)
	srv := newTestServer(t)
	if code, _ := call(t, srv, "acp_update_hermes_config", map[string]any{
		"provider": "nope", "model": "m",
	}); code != 500 {
		t.Errorf("unknown provider should fail, got %d", code)
	}
	if code, _ := call(t, srv, "acp_update_hermes_config", map[string]any{
		"provider": "openai-api", "model": "m", "apiKey": "bad\nkey",
	}); code != 500 {
		t.Errorf("newline in key should fail, got %d", code)
	}
}
