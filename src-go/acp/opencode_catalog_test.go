package acp

// opencode_catalog_test.go covers the models.dev normalization, the OAuth
// classification, the bundled snapshot, and the live → cache → snapshot chain.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestOpenCodeNormalizeExtractsSlimShape(t *testing.T) {
	raw := `{
        "demo": {
            "id": "demo",
            "name": "Demo Co",
            "npm": "@ai-sdk/openai-compatible",
            "env": ["DEMO_API_KEY"],
            "doc": "https://demo.example/docs",
            "models": {
                "demo-large": {
                    "id": "demo-large",
                    "name": "Demo Large",
                    "reasoning": true,
                    "tool_call": true,
                    "limit": { "context": 128000, "output": 8192 },
                    "cost": { "input": 1.5, "output": 6.0 }
                }
            }
        }
    }`
	providers, err := NormalizeModelsDev(raw)
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if len(providers) != 1 {
		t.Fatalf("providers = %v", providers)
	}
	p := providers[0]
	if p.ID != "demo" || p.Name != "Demo Co" || p.AuthKind != "api" {
		t.Fatalf("provider = %+v", p)
	}
	if p.NPM == nil || *p.NPM != "@ai-sdk/openai-compatible" {
		t.Fatalf("npm = %v", p.NPM)
	}
	if p.Doc == nil || *p.Doc != "https://demo.example/docs" {
		t.Fatalf("doc = %v", p.Doc)
	}
	if len(p.Env) != 1 || p.Env[0] != "DEMO_API_KEY" {
		t.Fatalf("env = %v", p.Env)
	}
	if len(p.Models) != 1 {
		t.Fatalf("models = %v", p.Models)
	}
	m := p.Models[0]
	if m.ID != "demo-large" || !m.Reasoning || !m.ToolCall {
		t.Fatalf("model = %+v", m)
	}
	if m.Context == nil || *m.Context != 128000 {
		t.Fatalf("context = %v", m.Context)
	}
	if m.CostIn == nil || *m.CostIn != 1.5 || m.CostOut == nil || *m.CostOut != 6.0 {
		t.Fatalf("cost = %v / %v", m.CostIn, m.CostOut)
	}
}

func TestOpenCodeNormalizeRejectsNonObjectRoot(t *testing.T) {
	if _, err := NormalizeModelsDev("[]"); err == nil {
		t.Error("array root should be rejected")
	}
	if _, err := NormalizeModelsDev("not json"); err == nil {
		t.Error("invalid JSON should be rejected")
	}
}

func TestOpenCodeNormalizeDefaultsAndEmptySlices(t *testing.T) {
	// A provider with no id/name falls back to its map key; absent env/models
	// must be empty slices (never null on the wire).
	providers, err := NormalizeModelsDev(`{"solo":{"npm":null}}`)
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if len(providers) != 1 {
		t.Fatalf("providers = %v", providers)
	}
	p := providers[0]
	if p.ID != "solo" || p.Name != "solo" {
		t.Fatalf("provider = %+v", p)
	}
	if p.Env == nil || len(p.Env) != 0 {
		t.Fatalf("env should be an empty slice, got %#v", p.Env)
	}
	if p.Models == nil || len(p.Models) != 0 {
		t.Fatalf("models should be an empty slice, got %#v", p.Models)
	}
}

func TestOpenCodeOAuthClassification(t *testing.T) {
	for _, id := range []string{"openai", "github-copilot", "gitlab"} {
		if !isOAuthProvider(id) {
			t.Errorf("%s should be oauth", id)
		}
	}
	for _, id := range []string{"anthropic", "openrouter"} {
		if isOAuthProvider(id) {
			t.Errorf("%s should be api", id)
		}
	}
}

func TestOpenCodeBundledCatalogIsNonTrivial(t *testing.T) {
	catalog := BundledCatalog()
	if len(catalog) <= 50 {
		t.Fatalf("bundled snapshot should carry the full models.dev catalog, got %d", len(catalog))
	}
	var openai *CatalogProvider
	for i := range catalog {
		if catalog[i].ID == "openai" {
			openai = &catalog[i]
			break
		}
	}
	if openai == nil {
		t.Fatal("snapshot must include the openai provider")
	}
	if openai.AuthKind != "oauth" {
		t.Fatalf("openai auth_kind = %q", openai.AuthKind)
	}
	if len(openai.Models) == 0 {
		t.Fatal("openai should carry models")
	}
	if openai.Env == nil {
		t.Fatal("env must be an empty slice, not nil")
	}
}

func TestOpenCodeProviderCatalogLiveWritesCacheThenServesIt(t *testing.T) {
	dir := t.TempDir()
	body := `{"demo":{"id":"demo","name":"Demo","env":[],"models":{"m":{"id":"m","name":"M"}}}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	oldURL := modelsDevURL
	modelsDevURL = srv.URL
	t.Cleanup(func() { modelsDevURL = oldURL })

	got := ProviderCatalog(dir, true)
	if len(got) != 1 || got[0].ID != "demo" {
		t.Fatalf("live catalog = %v", got)
	}
	if _, err := os.Stat(OpenCodeCachePath(dir)); err != nil {
		t.Fatalf("live fetch should write the cache: %v", err)
	}

	// The cache is fresh, so the server is no longer needed.
	srv.Close()
	got = ProviderCatalog(dir, false)
	if len(got) != 1 || got[0].ID != "demo" {
		t.Fatalf("cache should be served: %v", got)
	}
}

func TestOpenCodeProviderCatalogFallsBackToSnapshot(t *testing.T) {
	dir := t.TempDir()
	oldURL := modelsDevURL
	modelsDevURL = "http://127.0.0.1:1/api.json" // nothing listening
	t.Cleanup(func() { modelsDevURL = oldURL })

	got := ProviderCatalog(dir, true)
	if len(got) <= 50 {
		t.Fatalf("should fall back to the bundled snapshot, got %d providers", len(got))
	}
	if _, err := os.Stat(OpenCodeCachePath(dir)); err == nil {
		t.Fatal("a failed live fetch must not write the cache")
	}
}

func TestOpenCodeProviderCatalogServesStaleCacheWhenLiveFails(t *testing.T) {
	dir := t.TempDir()
	writeOpenCodeCache(dir, []CatalogProvider{{ID: "stale", Name: "Stale", Env: []string{}, AuthKind: "api", Models: []CatalogModel{}}})
	// Age the cache past the TTL.
	old := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(OpenCodeCachePath(dir), old, old); err != nil {
		t.Fatal(err)
	}
	oldURL := modelsDevURL
	modelsDevURL = "http://127.0.0.1:1/api.json"
	t.Cleanup(func() { modelsDevURL = oldURL })

	got := ProviderCatalog(dir, false)
	if len(got) != 1 || got[0].ID != "stale" {
		t.Fatalf("stale cache should be the fallback, got %v", got)
	}
}

func TestOpenCodeCatalogWireFieldNames(t *testing.T) {
	// The client type is snake_case: guard against an accidental camelCase tag.
	b := BundledCatalog()
	if len(b) == 0 {
		t.Skip("no bundled catalog")
	}
	raw, err := json.Marshal(b[0])
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"auth_kind"`} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("provider JSON missing %s: %s", want, raw)
		}
	}
	if strings.Contains(string(raw), "authKind") {
		t.Fatalf("provider JSON must not use camelCase: %s", raw)
	}
}
