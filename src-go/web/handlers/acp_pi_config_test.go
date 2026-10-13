package handlers_test

// acp_pi_config_test.go covers acp_load_pi_config / acp_update_pi_config: the
// update merge-writes pi's native files and the load projects them back, with
// unknown thinking levels dropped.

import (
	"path/filepath"
	"testing"
)

func TestAcpPiConfigRoundTrip(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", dir)
	srv := newTestServer(t)

	code, out := call(t, srv, "acp_update_pi_config", map[string]any{
		"provider":       "anthropic",
		"model":          "claude-3",
		"thinkingLevel":  "high",
		"apiKey":         "sk-test",
		"customBaseUrl":  "http://localhost:9",
		"customApi":      "openai-completions",
		"modelReasoning": map[string]any{"reasoning": true, "thinkingLevelMap": map[string]any{"low": "low", "bogus": "x"}},
	})
	if code != 200 {
		t.Fatalf("update: %d %v", code, out)
	}

	code, out = call(t, srv, "acp_load_pi_config", map[string]any{})
	if code != 200 {
		t.Fatalf("load: %d %v", code, out)
	}
	if out["defaultProvider"] != "anthropic" || out["defaultModel"] != "claude-3" || out["defaultThinkingLevel"] != "high" {
		t.Errorf("projection = %v", out)
	}
	auth, _ := out["authProviders"].([]any)
	if len(auth) != 1 || auth[0] != "anthropic" {
		t.Errorf("authProviders = %v", auth)
	}
	cps, _ := out["customProviders"].([]any)
	if len(cps) != 1 {
		t.Fatalf("customProviders = %v", cps)
	}
	cp, _ := cps[0].(map[string]any)
	if cp["id"] != "anthropic" || cp["baseUrl"] != "http://localhost:9" || cp["api"] != "openai-completions" {
		t.Errorf("custom provider = %v", cp)
	}
	ms, _ := cp["models"].([]any)
	if len(ms) != 1 {
		t.Fatalf("models = %v", ms)
	}
	m, _ := ms[0].(map[string]any)
	if m["id"] != "claude-3" || m["reasoning"] != true {
		t.Errorf("model = %v", m)
	}
	tlm, _ := m["thinkingLevelMap"].(map[string]any)
	if _, ok := tlm["bogus"]; ok {
		t.Errorf("unknown thinking level kept: %v", tlm)
	}
	if tlm["low"] != "low" {
		t.Errorf("low level = %v", tlm)
	}
}

func TestAcpPiConfigValidation(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", dir)
	srv := newTestServer(t)

	if code, _ := call(t, srv, "acp_update_pi_config", map[string]any{"provider": "", "model": "x"}); code != 500 {
		t.Errorf("empty provider should fail, got %d", code)
	}
	if code, _ := call(t, srv, "acp_update_pi_config", map[string]any{"provider": "p", "model": "  "}); code != 500 {
		t.Errorf("blank model should fail, got %d", code)
	}
	if code, _ := call(t, srv, "acp_update_pi_config", map[string]any{"provider": "p", "model": "m", "apiKey": "a\nb"}); code != 500 {
		t.Errorf("newline api key should fail, got %d", code)
	}
}

func TestAcpPiSettingsDirRefusesRelative(t *testing.T) {
	t.Setenv("PI_CODING_AGENT_DIR", filepath.Join("relative", "dir"))
	srv := newTestServer(t)
	if code, _ := call(t, srv, "acp_load_pi_config", map[string]any{}); code != 500 {
		t.Errorf("a relative pi dir should be refused, got %d", code)
	}
}
