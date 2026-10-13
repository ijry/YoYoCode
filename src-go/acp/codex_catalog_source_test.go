package acp

// codex_catalog_source_test.go covers the `{"models":[...]}` parse + priority
// ordering, the bundled snapshot, and the live → cache → snapshot chain.

import (
	"os"
	"testing"
)

func TestCodexParseModelsPriorityOrder(t *testing.T) {
	models, ok := parseCodexModels(`{"models":[{"slug":"astra","priority":2},{"slug":"sol","priority":1},{"slug":"x"},{"slug":"luna","priority":2}]}`)
	if !ok {
		t.Fatal("parse failed")
	}
	slugs := make([]string, 0, len(models))
	for _, m := range models {
		slugs = append(slugs, m.(map[string]any)["slug"].(string))
	}
	// Ascending priority, ties in file order, no priority last.
	want := []string{"sol", "astra", "luna", "x"}
	for i := range want {
		if slugs[i] != want[i] {
			t.Fatalf("slugs = %v, want %v", slugs, want)
		}
	}
}

func TestCodexParseModelsRejectsBadShapes(t *testing.T) {
	if _, ok := parseCodexModels("not json"); ok {
		t.Error("invalid JSON should fail")
	}
	if _, ok := parseCodexModels(`{"nope":1}`); ok {
		t.Error("missing models should fail")
	}
}

func TestCodexBundledSnapshotIsOrderedAndNonEmpty(t *testing.T) {
	models := BundledSnapshotModels()
	if len(models) == 0 {
		t.Fatal("bundled snapshot must be non-empty")
	}
	// The first entry is codex's highest-priority (lowest number) choice.
	first := priorityOf(models[0])
	for _, m := range models[1:] {
		if priorityOf(m) < first {
			t.Fatalf("snapshot is not sorted by priority: %v before %d", m, first)
		}
	}
	firstModel := models[0].(map[string]any)
	if _, ok := firstModel["slug"]; !ok {
		t.Fatalf("snapshot entry should carry a slug: %v", firstModel)
	}
}

func TestCodexRuntimeCatalogFallsBackToSnapshot(t *testing.T) {
	dir := t.TempDir()
	oldFetcher := codexLiveFetcher
	codexLiveFetcher = func() ([]any, bool) { return nil, false }
	t.Cleanup(func() { codexLiveFetcher = oldFetcher })

	got := RuntimeCatalog(dir, true)
	if len(got) == 0 {
		t.Fatal("expected the bundled snapshot")
	}
	if _, err := os.Stat(CodexCachePath(dir)); err == nil {
		t.Fatal("a failed live fetch must not write the cache")
	}
}

func TestCodexRuntimeCatalogCachesLiveResult(t *testing.T) {
	dir := t.TempDir()
	oldFetcher := codexLiveFetcher
	codexLiveFetcher = func() ([]any, bool) {
		return []any{map[string]any{"slug": "live", "priority": float64(1)}}, true
	}
	t.Cleanup(func() { codexLiveFetcher = oldFetcher })

	got := RuntimeCatalog(dir, true)
	if len(got) != 1 || got[0].(map[string]any)["slug"] != "live" {
		t.Fatalf("live catalog = %v", got)
	}
	if _, err := os.Stat(CodexCachePath(dir)); err != nil {
		t.Fatalf("live fetch should write the cache: %v", err)
	}

	// With the fetcher disabled, the fresh cache is served.
	codexLiveFetcher = func() ([]any, bool) { return nil, false }
	got = RuntimeCatalog(dir, false)
	if len(got) != 1 || got[0].(map[string]any)["slug"] != "live" {
		t.Fatalf("cache should be served: %v", got)
	}
}

func TestCodexCachedOrBundledNeverSpawns(t *testing.T) {
	dir := t.TempDir()
	// A cache written with an unsorted list comes back priority-ordered.
	writeCodexCache(dir, []any{
		map[string]any{"slug": "b", "priority": float64(2)},
		map[string]any{"slug": "a", "priority": float64(1)},
	})
	got := CachedOrBundledSnapshot(dir)
	if len(got) != 2 {
		t.Fatalf("cached = %v", got)
	}
	if got[0].(map[string]any)["slug"] != "a" {
		t.Fatalf("cache should be priority-ordered: %v", got)
	}

	// No cache → the bundled snapshot.
	got = CachedOrBundledSnapshot(t.TempDir())
	if len(got) == 0 {
		t.Fatal("expected the bundled snapshot when no cache exists")
	}
}
