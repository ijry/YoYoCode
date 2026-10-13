package handlers_test

// acp_catalogs_test.go covers opencode_provider_catalog / codex_bundled_catalog:
// the cached (offline, deterministic) path and the snake_case wire field names
// the client's OpenCodeCatalogProvider type expects.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"yoyocode/src-go/acp"
)

func TestOpencodeProviderCatalogWireShape(t *testing.T) {
	srv, st := newTestServerState(t)
	// Pre-seed a fresh cache so the route never touches the network.
	cache := []acp.CatalogProvider{{
		ID: "demo", Name: "Demo", NPM: nil, Env: []string{"DEMO_API_KEY"}, Doc: nil,
		AuthKind: "api",
		Models: []acp.CatalogModel{{
			ID: "m1", Name: "M1", Reasoning: true, ToolCall: true,
			Context: int64Ptr(1000), CostIn: float64Ptr(1.5), CostOut: float64Ptr(2.0),
		}},
	}}
	b, err := json.Marshal(cache)
	if err != nil {
		t.Fatal(err)
	}
	path := acp.OpenCodeCachePath(st.DataDir)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}

	code, arr := callArr(t, srv, "opencode_provider_catalog", map[string]any{})
	if code != 200 {
		t.Fatalf("status %d", code)
	}
	if len(arr) != 1 {
		t.Fatalf("providers = %v", arr)
	}
	p, _ := arr[0].(map[string]any)
	if p["id"] != "demo" || p["auth_kind"] != "api" {
		t.Fatalf("provider = %v", p)
	}
	if _, ok := p["authKind"]; ok {
		t.Fatalf("wire must be snake_case: %v", p)
	}
	if _, ok := p["doc"]; !ok {
		t.Fatalf("null-valued doc must still be present: %v", p)
	}
	models, _ := p["models"].([]any)
	if len(models) != 1 {
		t.Fatalf("models = %v", models)
	}
	m, _ := models[0].(map[string]any)
	if m["tool_call"] != true || m["cost_in"] != 1.5 || m["cost_out"] != 2.0 || m["context"] != float64(1000) {
		t.Fatalf("model = %v", m)
	}
	if m["reasoning"] != true {
		t.Fatalf("reasoning = %v", m["reasoning"])
	}
}

func TestCodexBundledCatalogUsesCacheAndPriorityOrder(t *testing.T) {
	srv, st := newTestServerState(t)
	path := acp.CodexCachePath(st.DataDir)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"models":[{"slug":"b","priority":2},{"slug":"a","priority":1}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	code, arr := callArr(t, srv, "codex_bundled_catalog", map[string]any{})
	if code != 200 {
		t.Fatalf("status %d", code)
	}
	if len(arr) != 2 {
		t.Fatalf("models = %v", arr)
	}
	first, _ := arr[0].(map[string]any)
	if first["slug"] != "a" {
		t.Fatalf("expected priority order, got %v", arr)
	}
}

func TestCatalogRoutesFallBackToBundledSnapshots(t *testing.T) {
	// A fresh data dir with no cache and an unreachable network still yields a
	// non-empty catalog (the bundled snapshots) — the routes are infallible.
	srv, _ := newTestServerState(t)
	code, arr := callArr(t, srv, "codex_bundled_catalog", map[string]any{})
	if code != 200 {
		t.Fatalf("codex status %d", code)
	}
	if len(arr) == 0 {
		t.Fatal("codex catalog must never be empty")
	}
}

func int64Ptr(v int64) *int64       { return &v }
func float64Ptr(v float64) *float64 { return &v }
