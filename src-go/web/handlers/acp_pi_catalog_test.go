package handlers_test

// acp_pi_catalog_test.go covers the acp_list_pi_model_capabilities route's wire
// shape and its honest "not found" reporting (PATH emptied so no pi resolves).

import (
	"encoding/json"
	"testing"
)

func TestListPiModelCapabilitiesNotInstalled(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	t.Setenv("PI_ACP_PI_COMMAND", "")
	srv := newTestServer(t)
	code, raw := rawPostJSON(t, srv, "acp_list_pi_model_capabilities", map[string]any{})
	if code != 200 {
		t.Fatalf("status %d", code)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatalf("decode: %v (%s)", err, raw)
	}
	for _, key := range []string{"status", "models"} {
		if _, ok := m[key]; !ok {
			t.Fatalf("missing key %q in %s", key, raw)
		}
	}
	if string(m["status"]) != `"not_found"` {
		t.Fatalf("status = %s (want the snake_case not_found)", m["status"])
	}
	if string(m["models"]) != "[]" {
		t.Fatalf("models must be an empty array, got %s", m["models"])
	}
}
