package handlers_test

// acp_agent_probes_test.go covers the cursor/qoder probe routes' wire shape and
// their honest "not installed" reporting (PATH emptied so the probes find
// nothing — the routes never fail, they report in-band).

import (
	"encoding/json"
	"testing"
)

func TestQoderAuthStatusWireShape(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	srv := newTestServer(t)
	code, raw := rawPostJSON(t, srv, "acp_qoder_auth_status", map[string]any{"personalAccessToken": "tok"})
	if code != 200 {
		t.Fatalf("status %d", code)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatalf("decode: %v (%s)", err, raw)
	}
	for _, key := range []string{"installed", "logged_in", "username", "email", "user_type", "version", "allow_byok", "error", "binary_path"} {
		if _, ok := m[key]; !ok {
			t.Fatalf("missing key %q in %s", key, raw)
		}
	}
	if string(m["installed"]) != "false" || string(m["logged_in"]) != "false" {
		t.Fatalf("not-installed report wrong: %s", raw)
	}
	for _, key := range []string{"username", "email", "user_type", "version", "allow_byok", "error", "binary_path"} {
		if string(m[key]) != "null" {
			t.Fatalf("%s should be null, got %s", key, m[key])
		}
	}
	if _, ok := m["loggedIn"]; ok {
		t.Fatalf("wire must be snake_case: %s", raw)
	}
}

func TestCursorAuthStatusNotInstalled(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	srv := newTestServer(t)
	code, raw := rawPostJSON(t, srv, "acp_cursor_auth_status", map[string]any{})
	if code != 200 {
		t.Fatalf("status %d", code)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatalf("decode: %v (%s)", err, raw)
	}
	for _, key := range []string{"installed", "is_authenticated", "raw_status", "email", "membership", "error", "binary_path", "credential_verified"} {
		if _, ok := m[key]; !ok {
			t.Fatalf("missing key %q in %s", key, raw)
		}
	}
	if string(m["installed"]) != "false" || string(m["is_authenticated"]) != "false" {
		t.Fatalf("not-installed report wrong: %s", raw)
	}
	if string(m["credential_verified"]) != "null" || string(m["binary_path"]) != "null" {
		t.Fatalf("optional fields should be null: %s", raw)
	}
}

func TestCursorListModelsNotInstalled(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	srv := newTestServer(t)
	code, out := call(t, srv, "acp_cursor_list_models", map[string]any{})
	if code != 200 {
		t.Fatalf("status %d", code)
	}
	models, ok := out["models"].([]any)
	if !ok {
		t.Fatalf("models must be an array, got %v", out["models"])
	}
	if len(models) != 0 {
		t.Fatalf("models = %v", models)
	}
	if out["default_model"] != nil {
		t.Fatalf("default_model = %v", out["default_model"])
	}
	if out["error"] == nil {
		t.Fatal("error should report the probe failure")
	}
}
