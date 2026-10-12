package handlers_test

import (
	"testing"
)

func TestAcpEnvDiagnosticsShape(t *testing.T) {
	srv := newTestServer(t)
	code, out := call(t, srv, "acp_env_diagnostics", map[string]any{})
	if code != 200 {
		t.Fatalf("status = %d", code)
	}
	for _, k := range []string{"generated_at", "verdict", "sections", "plain_text"} {
		if _, ok := out[k]; !ok {
			t.Fatalf("missing field %q in report: %v", k, out)
		}
	}
	// agent_type omitted → null
	if out["agent_type"] != nil {
		t.Fatalf("base report agent_type should be null, got %v", out["agent_type"])
	}
	v, _ := out["verdict"].(map[string]any)
	if v == nil || v["code"] == nil || v["level"] == nil {
		t.Fatalf("verdict missing code/level: %v", out["verdict"])
	}
	secs, _ := out["sections"].([]any)
	if len(secs) == 0 {
		t.Fatalf("expected sections, got %v", out["sections"])
	}
	pt, _ := out["plain_text"].(string)
	if pt == "" || pt[:5] != "=====" {
		t.Fatalf("plain_text should be a text blob, got %q", pt)
	}
}
