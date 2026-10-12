package handlers_test

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOpencodeListPluginsEmpty(t *testing.T) {
	cfg := t.TempDir()
	cache := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	t.Setenv("XDG_CACHE_HOME", cache)
	srv := newTestServer(t)

	// No opencode.json yet → empty plugin list, paths reported.
	_, out := call(t, srv, "opencode_list_plugins", map[string]any{})
	if out["config_path"] == nil || out["cache_dir"] == nil {
		t.Fatalf("expected config/cache paths, got %v", out)
	}
	plugins, _ := out["plugins"].([]any)
	if len(plugins) != 0 {
		t.Fatalf("expected no plugins, got %v", plugins)
	}
	if out["has_project_config_hint"] != false {
		t.Fatalf("hint should be false, got %v", out["has_project_config_hint"])
	}
}

func TestOpencodeListPluginsClassifies(t *testing.T) {
	cfg := t.TempDir()
	cache := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	t.Setenv("XDG_CACHE_HOME", cache)

	// Write a config declaring a package plugin + a (missing) absolute path plugin.
	ocDir := filepath.Join(cfg, "opencode")
	if err := os.MkdirAll(ocDir, 0o755); err != nil {
		t.Fatal(err)
	}
	missingPath := filepath.Join(t.TempDir(), "nope.js")
	conf := `{"plugin": ["my-pkg@1.2.3", "` + jsonEscape(missingPath) + `"]}`
	if err := os.WriteFile(filepath.Join(ocDir, "opencode.json"), []byte(conf), 0o644); err != nil {
		t.Fatal(err)
	}

	srv := newTestServer(t)
	_, out := call(t, srv, "opencode_list_plugins", map[string]any{})
	plugins, _ := out["plugins"].([]any)
	if len(plugins) != 2 {
		t.Fatalf("expected 2 plugins, got %d: %v", len(plugins), plugins)
	}
	byName := map[string]map[string]any{}
	for _, pr := range plugins {
		m := pr.(map[string]any)
		byName[m["name"].(string)] = m
	}
	pkg := byName["my-pkg"]
	if pkg == nil || pkg["status"] != "missing" {
		t.Fatalf("package plugin should be missing, got %v", pkg)
	}
	path := byName[missingPath]
	if path == nil || path["status"] != "path_missing" {
		t.Fatalf("path plugin should be path_missing, got %v", path)
	}

	// Now create the modern cache layout for my-pkg → installed.
	pkgDir := filepath.Join(cache, "opencode", "packages", "my-pkg@1.2.3", "node_modules", "my-pkg")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "package.json"), []byte(`{"version":"1.2.3"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	_, out2 := call(t, srv, "opencode_list_plugins", map[string]any{})
	for _, pr := range out2["plugins"].([]any) {
		m := pr.(map[string]any)
		if m["name"] == "my-pkg" {
			if m["status"] != "installed" || m["installed_version"] != "1.2.3" {
				t.Fatalf("my-pkg should be installed@1.2.3, got %v", m)
			}
		}
	}
}

// jsonEscape escapes a filesystem path for safe embedding in a JSON string
// literal (backslashes on Windows).
func jsonEscape(s string) string {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		if r == '\\' || r == '"' {
			out = append(out, '\\')
		}
		out = append(out, r)
	}
	return string(out)
}
