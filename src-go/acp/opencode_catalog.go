package acp

// opencode_catalog.go ports the OpenCode provider catalog backed by
// models.dev (acp/opencode_catalog.rs).
//
// OpenCode resolves its built-in providers and their model lists from the
// models.dev registry. To drive a guided "connect a provider" experience we
// mirror that catalog here, normalized to a slim shape the frontend renders.
//
// Resolution order (most fresh first):
//  1. live fetch of models.dev/api.json (when forced, or when the cache is
//     stale), normalized and written back to the on-disk cache;
//  2. the on-disk cache under `<dataDir>/cache/opencode/models-dev.json`;
//  3. the snapshot bundled into the binary, so the catalog is always available
//     offline.
//
// Wire note: the structs carry NO camelCase rename — the fields serialize
// snake_case (`auth_kind`, `tool_call`, `cost_in`, `cost_out`), matching the
// client's OpenCodeCatalogProvider type.

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"yoyocode/src-go/assets"
)

// modelsDevURL is the models.dev JSON API — the same source OpenCode reads.
// A variable so tests can point it at a local server.
var modelsDevURL = "https://models.dev/api.json"

// openCodeCacheTTL is the cache lifetime before we attempt a fresh fetch. Stale
// cache is still used as a fallback when the network is unavailable.
const openCodeCacheTTL = 24 * time.Hour

// CatalogModel is one model entry under a catalog provider.
type CatalogModel struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Reasoning bool     `json:"reasoning"`
	ToolCall  bool     `json:"tool_call"`
	Context   *int64   `json:"context"`
	CostIn    *float64 `json:"cost_in"`
	CostOut   *float64 `json:"cost_out"`
}

// CatalogProvider is one provider from the models.dev catalog. AuthKind is
// "api" or "oauth".
type CatalogProvider struct {
	ID       string         `json:"id"`
	Name     string         `json:"name"`
	NPM      *string        `json:"npm"`
	Env      []string       `json:"env"`
	Doc      *string        `json:"doc"`
	AuthKind string         `json:"auth_kind"`
	Models   []CatalogModel `json:"models"`
}

// isOAuthProvider reports whether OpenCode authenticates the provider with an
// OAuth/browser flow rather than a pasted API key (ChatGPT Plus/Pro, GitHub
// Copilot, GitLab Duo). Anthropic Claude Pro/Max OAuth is intentionally
// excluded — OpenCode removed its bundled plugin and Anthropic prohibits
// third-party OAuth use.
func isOAuthProvider(providerID string) bool {
	switch providerID {
	case "openai", "github-copilot", "gitlab":
		return true
	}
	return false
}

// NormalizeModelsDev normalizes the raw models.dev api.json (a
// `{ providerId: Provider }` map) into the slim catalog shape. Tolerant of
// missing/extra fields.
func NormalizeModelsDev(raw string) ([]CatalogProvider, error) {
	var root map[string]any
	if err := json.Unmarshal([]byte(raw), &root); err != nil {
		return nil, fmt.Errorf("parse models.dev: %w", err)
	}

	providers := make([]CatalogProvider, 0, len(root))
	for key, rawProvider := range root {
		p, ok := rawProvider.(map[string]any)
		if !ok {
			continue
		}
		id := jsonStr(p["id"])
		if id == "" {
			id = key
		}
		name := jsonStr(p["name"])
		if name == "" {
			name = id
		}
		var npm *string
		if v := jsonStr(p["npm"]); v != "" {
			npm = &v
		}
		var doc *string
		if v := jsonStr(p["doc"]); v != "" {
			doc = &v
		}
		env := []string{}
		if arr, ok := p["env"].([]any); ok {
			for _, x := range arr {
				if s, ok := x.(string); ok {
					env = append(env, s)
				}
			}
		}
		authKind := "api"
		if isOAuthProvider(id) {
			authKind = "oauth"
		}

		models := []CatalogModel{}
		if modelObj, ok := p["models"].(map[string]any); ok {
			for modelKey, rawModel := range modelObj {
				m, ok := rawModel.(map[string]any)
				if !ok {
					continue
				}
				modelID := jsonStr(m["id"])
				if modelID == "" {
					modelID = modelKey
				}
				modelName := jsonStr(m["name"])
				if modelName == "" {
					modelName = modelID
				}
				cm := CatalogModel{
					ID:        modelID,
					Name:      modelName,
					Reasoning: jsonBool(m["reasoning"]),
					ToolCall:  jsonBool(m["tool_call"]),
				}
				if limit, ok := m["limit"].(map[string]any); ok {
					cm.Context = jsonInt64(limit["context"])
				}
				if cost, ok := m["cost"].(map[string]any); ok {
					cm.CostIn = jsonFloat64(cost["input"])
					cm.CostOut = jsonFloat64(cost["output"])
				}
				models = append(models, cm)
			}
		}
		sort.Slice(models, func(i, j int) bool { return models[i].ID < models[j].ID })

		providers = append(providers, CatalogProvider{
			ID:       id,
			Name:     name,
			NPM:      npm,
			Env:      env,
			Doc:      doc,
			AuthKind: authKind,
			Models:   models,
		})
	}

	sort.SliceStable(providers, func(i, j int) bool {
		li, lj := strings.ToLower(providers[i].Name), strings.ToLower(providers[j].Name)
		if li != lj {
			return li < lj
		}
		return providers[i].ID < providers[j].ID
	})
	return providers, nil
}

// BundledCatalog is the snapshot compiled into the binary. Always available; the
// last-resort fallback when both the network and the on-disk cache are
// unavailable.
func BundledCatalog() []CatalogProvider {
	var providers []CatalogProvider
	if err := json.Unmarshal(assets.OpenCodeModelsDev, &providers); err != nil {
		return []CatalogProvider{}
	}
	for i := range providers {
		if providers[i].Env == nil {
			providers[i].Env = []string{}
		}
		if providers[i].Models == nil {
			providers[i].Models = []CatalogModel{}
		}
	}
	return providers
}

// OpenCodeCachePath is where the normalized catalog is cached on disk.
func OpenCodeCachePath(dataDir string) string {
	return filepath.Join(dataDir, "cache", "opencode", "models-dev.json")
}

// readOpenCodeCache reads the on-disk cache; requireFresh rejects a cache older
// than the TTL.
func readOpenCodeCache(dataDir string, requireFresh bool) ([]CatalogProvider, bool) {
	path := OpenCodeCachePath(dataDir)
	fi, err := os.Stat(path)
	if err != nil {
		return nil, false
	}
	if requireFresh {
		if time.Since(fi.ModTime()) > openCodeCacheTTL {
			return nil, false
		}
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	var providers []CatalogProvider
	if err := json.Unmarshal(b, &providers); err != nil {
		return nil, false
	}
	return providers, true
}

func writeOpenCodeCache(dataDir string, providers []CatalogProvider) {
	path := OpenCodeCachePath(dataDir)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	if b, err := json.Marshal(providers); err == nil {
		_ = os.WriteFile(path, b, 0o644)
	}
}

// fetchOpenCodeLive fetches and normalizes the live models.dev catalog. Bounded
// so a black-holed connection fails fast and we fall back to the cache or the
// bundled snapshot.
func fetchOpenCodeLive() ([]CatalogProvider, error) {
	client := &http.Client{
		Timeout: 12 * time.Second,
		Transport: &http.Transport{
			DialContext: (&net.Dialer{Timeout: 4 * time.Second}).DialContext,
		},
	}
	resp, err := client.Get(modelsDevURL)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch models.dev catalog: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("failed to fetch models.dev catalog: HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read models.dev response: %w", err)
	}
	return NormalizeModelsDev(string(body))
}

// ProviderCatalog resolves the OpenCode provider catalog with the
// live → cache → snapshot fallback chain. Infallible: the bundled snapshot
// guarantees a result.
func ProviderCatalog(dataDir string, forceRefresh bool) []CatalogProvider {
	if !forceRefresh {
		if fresh, ok := readOpenCodeCache(dataDir, true); ok {
			return fresh
		}
	}
	if providers, err := fetchOpenCodeLive(); err == nil && len(providers) > 0 {
		writeOpenCodeCache(dataDir, providers)
		return providers
	}
	if cached, ok := readOpenCodeCache(dataDir, false); ok {
		return cached
	}
	return BundledCatalog()
}

// ─── small JSON coercion helpers ─────────────────────────────────────────────

func jsonStr(v any) string {
	s, _ := v.(string)
	return s
}

func jsonBool(v any) bool {
	b, _ := v.(bool)
	return b
}

func jsonInt64(v any) *int64 {
	switch t := v.(type) {
	case float64:
		n := int64(t)
		return &n
	case int64:
		n := t
		return &n
	case int:
		n := int64(t)
		return &n
	}
	return nil
}

func jsonFloat64(v any) *float64 {
	switch t := v.(type) {
	case float64:
		return &t
	case int64:
		f := float64(t)
		return &f
	case int:
		f := float64(t)
		return &f
	}
	return nil
}
