package acp

// codex_catalog_source.go ports the runtime source for codex's official model
// catalog (acp/codex_catalog_source.rs).
//
// Because `model_catalog_json` is a whole-table replace, we must reproduce
// codex's own catalog inside the generated file — so it has to match the codex
// we actually launch. That codex is the one NESTED under the pinned `codex-acp`
// npm package (`.../codex-acp/node_modules/@openai/codex`), NOT the `codex` on
// PATH (often an unrelated standalone install, and the version hoisted to the
// top-level `node_modules`).
//
// We resolve it with node's own nearest-`node_modules`-first resolution, run
// `codex debug models --bundled`, and cache the JSON on disk with a
// live → cache → bundled-snapshot fallback chain. Infallible by construction:
// the compiled-in snapshot guarantees a result offline.
//
// Note: the upstream cache lives under the app home dir while the OpenCode
// cache lives under the app data dir; the port has a single data dir, so both
// live under `<dataDir>/cache/`.

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"yoyocode/src-go/assets"
)

const (
	// codexCacheTTL is the on-disk cache freshness window — matches the OpenCode
	// catalog cache.
	codexCacheTTL = 24 * time.Hour
	// codexTimeout bounds each codex subprocess (node resolve + `debug models`).
	codexTimeout = 15 * time.Second
)

// codexLiveFetcher is a test hook: when set, it replaces the subprocess-based
// live fetch.
var codexLiveFetcher func() ([]any, bool)

// npmPrefixProbe is a test hook for the `npm prefix -g` probe.
var npmPrefixProbe func() (string, bool)

// CodexCachePath is where the fetched catalog is cached on disk.
func CodexCachePath(dataDir string) string {
	return filepath.Join(dataDir, "cache", "codex", "bundled-catalog.json")
}

// parseCodexModels extracts the `models` array from a `{"models":[...]}`
// document, in codex's picker order (see sortByPriority).
func parseCodexModels(text string) ([]any, bool) {
	var doc map[string]any
	if err := json.Unmarshal([]byte(text), &doc); err != nil {
		return nil, false
	}
	arr, ok := doc["models"].([]any)
	if !ok {
		return nil, false
	}
	sortByPriority(arr)
	return arr, true
}

// priorityOf is a catalog entry's `priority`: codex's picker position,
// ascending. An entry without one sorts last.
func priorityOf(model any) int64 {
	m, ok := model.(map[string]any)
	if !ok {
		return maxInt64
	}
	switch t := m["priority"].(type) {
	case float64:
		return int64(t)
	case int64:
		return t
	case int:
		return int64(t)
	}
	return maxInt64
}

const maxInt64 = int64(^uint64(0) >> 1)

// sortByPriority puts a catalog in the order codex itself presents it: ascending
// `priority`, ties kept in catalog order (codex's own build_available_models
// does a stable sort_by_key on the same field).
//
// `codex debug models --bundled` prints entries in FILE order, which is not
// priority order since codex 0.159.1, so anything reading "the first entry" as
// "codex's first choice" has to sort first.
func sortByPriority(models []any) {
	sort.SliceStable(models, func(i, j int) bool {
		return priorityOf(models[i]) < priorityOf(models[j])
	})
}

func readCodexCache(dataDir string, requireFresh bool) ([]any, bool) {
	path := CodexCachePath(dataDir)
	fi, err := os.Stat(path)
	if err != nil {
		return nil, false
	}
	if requireFresh && time.Since(fi.ModTime()) > codexCacheTTL {
		return nil, false
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	models, ok := parseCodexModels(string(b))
	if !ok || len(models) == 0 {
		return nil, false
	}
	return models, true
}

func writeCodexCache(dataDir string, models []any) {
	path := CodexCachePath(dataDir)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	if b, err := json.Marshal(map[string]any{"models": models}); err == nil {
		_ = os.WriteFile(path, b, 0o644)
	}
}

// BundledSnapshotModels is the compiled-in offline snapshot parsed into its
// `models` array, in codex's picker order.
func BundledSnapshotModels() []any {
	var doc map[string]any
	if err := json.Unmarshal(assets.CodexBundledCatalog, &doc); err != nil {
		return []any{}
	}
	arr, _ := doc["models"].([]any)
	if arr == nil {
		return []any{}
	}
	sortByPriority(arr)
	return arr
}

// RuntimeCatalog resolves the official codex catalog with the
// live → cache → bundled-snapshot fallback chain. Infallible.
func RuntimeCatalog(dataDir string, forceRefresh bool) []any {
	if !forceRefresh {
		if fresh, ok := readCodexCache(dataDir, true); ok {
			return fresh
		}
	}
	if models, ok := refreshLiveCatalog(dataDir); ok {
		return models
	}
	if cached, ok := readCodexCache(dataDir, false); ok {
		return cached
	}
	return BundledSnapshotModels()
}

// CachedOrBundledSnapshot is the synchronous catalog for config-write paths:
// the on-disk cache, else the bundled snapshot. Never spawns a subprocess.
func CachedOrBundledSnapshot(dataDir string) []any {
	if cached, ok := readCodexCache(dataDir, false); ok {
		return cached
	}
	return BundledSnapshotModels()
}

// refreshLiveCatalog re-reads the catalog from the installed codex and stores it
// in the cache. `false` when no live catalog could be produced — unlike
// RuntimeCatalog this never falls back.
func refreshLiveCatalog(dataDir string) ([]any, bool) {
	models, ok := fetchCodexLive(dataDir)
	if !ok {
		return nil, false
	}
	writeCodexCache(dataDir, models)
	return models, true
}

// codexACPDir is the codex-acp package directory under an npm prefix, where the
// nested `@openai/codex` lives.
func codexACPDir(prefix string) string {
	base := filepath.Join(prefix, "lib", "node_modules")
	if runtime.GOOS == "windows" {
		base = filepath.Join(prefix, "node_modules")
	}
	return filepath.Join(base, "@agentclientprotocol", "codex-acp")
}

// codexACPDirs are the candidate codex-acp package dirs: the global npm prefix
// and our own user prefix (`<dataDir>/npm-global`, used when a global install
// hit EACCES).
func codexACPDirs(dataDir string) []string {
	var dirs []string
	if prefix, ok := cachedNpmGlobalPrefix(); ok {
		dirs = append(dirs, codexACPDir(prefix))
	}
	dirs = append(dirs, codexACPDir(filepath.Join(dataDir, "npm-global")))
	return dirs
}

var (
	npmPrefixOnce sync.Once
	npmPrefixVal  string
	npmPrefixOK   bool
)

// cachedNpmGlobalPrefix resolves the global npm prefix once via `npm prefix -g`.
func cachedNpmGlobalPrefix() (string, bool) {
	if npmPrefixProbe != nil {
		return npmPrefixProbe()
	}
	npmPrefixOnce.Do(func() {
		npm, err := exec.LookPath("npm")
		if err != nil {
			return
		}
		out, err := runWithTimeout(npm, []string{"prefix", "-g"}, 1500*time.Millisecond)
		if err != nil {
			return
		}
		prefix := strings.TrimSpace(out)
		if prefix == "" {
			return
		}
		npmPrefixVal, npmPrefixOK = prefix, true
	})
	return npmPrefixVal, npmPrefixOK
}

// resolveCodexJS resolves the nested `@openai/codex/bin/codex.js` from a
// codex-acp package dir using node's own resolver (nearest node_modules first)
// so we get the version codex-acp uses, not a hoisted/PATH one.
func resolveCodexJS(acpDir, node string) (string, bool) {
	script := "process.stdout.write(require.resolve('@openai/codex/bin/codex.js',{paths:[process.argv[1]]}))"
	out, err := runWithTimeout(node, []string{"-e", script, acpDir}, codexTimeout)
	if err != nil {
		return "", false
	}
	resolved := strings.TrimSpace(out)
	if resolved == "" {
		return "", false
	}
	if _, err := os.Stat(resolved); err != nil {
		return "", false
	}
	return resolved, true
}

// fetchCodexLive runs the nested codex's `debug models --bundled` and returns
// its models.
func fetchCodexLive(dataDir string) ([]any, bool) {
	if codexLiveFetcher != nil {
		return codexLiveFetcher()
	}
	node, err := exec.LookPath("node")
	if err != nil {
		return nil, false
	}
	for _, acpDir := range codexACPDirs(dataDir) {
		if _, err := os.Stat(acpDir); err != nil {
			continue
		}
		codexJS, ok := resolveCodexJS(acpDir, node)
		if !ok {
			continue
		}
		out, err := runWithTimeout(node, []string{codexJS, "debug", "models", "--bundled"}, codexTimeout)
		if err != nil {
			continue
		}
		if models, ok := parseCodexModels(out); ok && len(models) > 0 {
			return models, true
		}
	}
	return nil, false
}

// runWithTimeout runs a command with a bounded timeout and returns its stdout.
func runWithTimeout(name string, args []string, timeout time.Duration) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, name, args...).Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}
