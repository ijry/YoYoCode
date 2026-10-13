package acp

// binary_cache_test.go covers the binary download cache: the platform mapping,
// the archive extraction (zip + tar.gz), path-traversal refusal, and the
// cache-hit fast path (no network).

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
)

func TestCurrentPlatformNonEmpty(t *testing.T) {
	if p := CurrentPlatform(); p == "" {
		t.Fatal("CurrentPlatform returned empty")
	}
}

// TestBinaryDistMetadata locks the binary registry: cursor/open_code/antigravity
// are binary with a current-platform recipe; qoder is NOT binary (upstream runs
// it via npx).
func TestBinaryDistMetadata(t *testing.T) {
	for _, at := range []string{"cursor", "open_code", "antigravity"} {
		ab, ok := BinaryDistFor(at)
		if !ok {
			t.Fatalf("%s: no binary recipe for %s", at, CurrentPlatform())
		}
		if ab.URL == "" || ab.Version == "" {
			t.Errorf("%s: incomplete recipe: %+v", at, ab)
		}
	}
	if _, ok := BinaryDistFor("qoder"); ok {
		t.Error("qoder must not be a binary agent (upstream uses npx)")
	}
	// antigravity is a dir-tree agent with a required sibling on the current
	// platform.
	if ab, ok := BinaryDistFor("antigravity"); ok {
		if !ab.DirTree || len(ab.Siblings) == 0 {
			t.Errorf("antigravity should be dir-tree with a sibling: %+v", ab)
		}
	}
	// open_code is a single-file agent (no dir entry → the cmd file).
	if ab, ok := BinaryDistFor("open_code"); ok {
		if ab.DirTree || ab.Entry != "" {
			t.Errorf("open_code should be single-file: %+v", ab)
		}
	}
}

// TestInstallBinaryTreeValidatesSiblings locks the sibling check: a dir-tree
// archive missing its required sibling fails rather than installing a broken
// agent.
func TestInstallBinaryTreeValidatesSiblings(t *testing.T) {
	dir := t.TempDir()
	zipPath := filepath.Join(dir, "agy.zip")
	// Entry present, required sibling absent.
	writeZip(t, zipPath, map[string]string{"agy_acp_server.par": "entry"})
	target := filepath.Join(dir, "out", "agy_acp_server.par")
	if err := installBinaryTree(zipPath, filepath.Join(dir, "out"), target, []string{"localharness_external"}); err == nil {
		t.Fatal("expected a missing-sibling error")
	}

	// With the sibling present it installs.
	dir2 := t.TempDir()
	zip2 := filepath.Join(dir2, "agy2.zip")
	writeZip(t, zip2, map[string]string{"agy_acp_server.par": "entry", "localharness_external": "harness"})
	out2 := filepath.Join(dir2, "out")
	if err := installBinaryTree(zip2, out2, filepath.Join(out2, "agy_acp_server.par"), []string{"localharness_external"}); err != nil {
		t.Fatalf("install with sibling: %v", err)
	}
}

func writeZip(t *testing.T, path string, files map[string]string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zw := zip.NewWriter(f)
	for name, body := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
}

func writeTarGz(t *testing.T, path string, files map[string]string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(body))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestExtractZipAndTarGz(t *testing.T) {
	dir := t.TempDir()
	zipPath := filepath.Join(dir, "a.zip")
	writeZip(t, zipPath, map[string]string{"dist-package/cursor-agent": "#!/bin/sh\n"})
	zipOut := filepath.Join(dir, "zipout")
	if err := extractArchive(zipPath, zipOut); err != nil {
		t.Fatalf("extract zip: %v", err)
	}
	if b, err := os.ReadFile(filepath.Join(zipOut, "dist-package", "cursor-agent")); err != nil || len(b) == 0 {
		t.Fatalf("zip entry missing: %v", err)
	}

	tgzPath := filepath.Join(dir, "b.tar.gz")
	writeTarGz(t, tgzPath, map[string]string{"agy_acp_server.par": "par-bytes"})
	tgzOut := filepath.Join(dir, "tgzout")
	if err := extractArchive(tgzPath, tgzOut); err != nil {
		t.Fatalf("extract tar.gz: %v", err)
	}
	if b, err := os.ReadFile(filepath.Join(tgzOut, "agy_acp_server.par")); err != nil || string(b) != "par-bytes" {
		t.Fatalf("tar entry missing: %v", err)
	}
}

func TestExtractRefusesTraversal(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.zip")
	writeZip(t, bad, map[string]string{"../escape": "x"})
	if err := extractArchive(bad, filepath.Join(dir, "out")); err == nil {
		t.Fatal("expected traversal to be refused")
	}
}

// TestEnsureAgentBinaryCacheHit locks the fast path: an already-extracted entry
// is returned without any download.
func TestEnsureAgentBinaryCacheHit(t *testing.T) {
	root := t.TempDir()
	ab, ok := BinaryDistFor("antigravity")
	if !ok {
		t.Skipf("no antigravity recipe on %s", CurrentPlatform())
	}
	entry := CachedBinaryPath(root, "antigravity", ab.Version)
	if entry == "" {
		t.Fatal("CachedBinaryPath empty")
	}
	if err := os.MkdirAll(filepath.Dir(entry), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(entry, []byte("cached"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := EnsureAgentBinary("antigravity", root, "", nil)
	if err != nil {
		t.Fatalf("cache hit: %v", err)
	}
	if got != entry {
		t.Errorf("path = %q, want %q", got, entry)
	}
}

func TestEnsureAgentBinaryRejectsCustomVersion(t *testing.T) {
	if _, err := EnsureAgentBinary("antigravity", t.TempDir(), "9.9.9", nil); err == nil {
		t.Fatal("expected a custom-version refusal")
	}
}

// TestResolveSpawnQoderUsesNpx locks the qoder fidelity fix: it resolves via
// npx, not the (absent) binary cache.
func TestResolveSpawnQoderUsesNpx(t *testing.T) {
	cfg, ok := ResolveSpawn("qoder", "/tmp/x", nil)
	if !ok {
		t.Fatal("qoder should resolve")
	}
	if cfg.Command != "npx" {
		t.Errorf("qoder command = %q, want npx", cfg.Command)
	}
}
