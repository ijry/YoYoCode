package handlers

// acp_catalogs.go wires the two model-catalog routes:
//
//   - opencode_provider_catalog — the models.dev provider catalog (live → cache
//     → bundled snapshot), used by the guided "connect a provider" dialog.
//   - codex_bundled_catalog — codex's official model catalog (live → cache →
//     bundled snapshot), used by the settings editor's official list.
//
// Both are infallible upstream: the bundled snapshot guarantees a result, so
// neither route reports an error.
//
// Wire note: the OpenCode catalog's fields are snake_case (`auth_kind`,
// `tool_call`, `cost_in`, `cost_out`) — the structs carry no camelCase rename.
// codex_bundled_catalog returns codex's own opaque ModelInfo objects verbatim.

import (
	"yoyocode/src-go/acp"
	"yoyocode/src-go/web"
)

func registerACPCatalogs(reg *Registry) {
	reg.Register("opencode_provider_catalog", func(c *web.Context) (any, error) {
		forceRefresh := GetBool(c, "forceRefresh", "force_refresh")
		return acp.ProviderCatalog(catalogDataDir(c), forceRefresh), nil
	})

	reg.Register("codex_bundled_catalog", func(c *web.Context) (any, error) {
		forceRefresh := GetBool(c, "forceRefresh", "force_refresh")
		return acp.RuntimeCatalog(catalogDataDir(c), forceRefresh), nil
	})
}

// catalogDataDir is the AppState data dir (where the catalog caches live).
func catalogDataDir(c *web.Context) string {
	if st, ok := appState(c); ok && st != nil {
		return st.DataDir
	}
	return ""
}
