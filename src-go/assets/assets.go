// Package assets holds data snapshots compiled into the binary, mirroring the
// upstream `src-tauri/resources/` layout.
//
// These are the offline fallbacks for the two model catalogs: the OpenCode
// provider catalog (a normalized models.dev snapshot) and codex's bundled
// official model catalog. Both guarantee a non-empty catalog when the network
// and the on-disk cache are unavailable.
//
// The snapshot files are point-in-time data generated from the upstream
// resources; refresh them by re-copying from the upstream `resources/` dir.
package assets

import _ "embed"

// OpenCodeModelsDev is the normalized models.dev provider catalog
// (`[]CatalogProvider`), embedded verbatim.
//
//go:embed opencode/models-dev.json
var OpenCodeModelsDev []byte

// CodexBundledCatalog is codex's bundled official model catalog, a
// `{"models":[...]}` document of opaque ModelInfo entries.
//
//go:embed codex/bundled-catalog.json
var CodexBundledCatalog []byte
