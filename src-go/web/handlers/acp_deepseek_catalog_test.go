package handlers_test

// acp_deepseek_catalog_test.go covers acp_load_deepseek_model_catalog /
// acp_update_deepseek_model_catalog: the inherited-defaults projection, the
// configured/invalid/error reporting, and the comment-preserving block splice
// (insert / replace / clear / header drop / inline refusal).

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// dshHome points DSH_HOME at a fresh temp dir and returns it.
func dshHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("DSH_HOME", dir)
	return dir
}

func readSettingsYAML(t *testing.T, dir string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "settings.yaml"))
	if err != nil {
		t.Fatalf("read settings.yaml: %v", err)
	}
	var m map[string]any
	if err := yaml.Unmarshal(b, &m); err != nil {
		t.Fatalf("parse settings.yaml: %v", err)
	}
	return m
}

func settingsModels(t *testing.T, dir string) []any {
	t.Helper()
	root := readSettingsYAML(t, dir)
	sec, _ := root["llm-deepseek"].(map[string]any)
	if sec == nil {
		return nil
	}
	models, _ := sec["models"].([]any)
	return models
}

func TestDeepSeekCatalogLoadInheritsDefaults(t *testing.T) {
	dshHome(t)
	srv := newTestServer(t)
	code, out := call(t, srv, "acp_load_deepseek_model_catalog", nil)
	if code != 200 {
		t.Fatalf("status %d", code)
	}
	if out["exists"] != false || out["configured"] != false {
		t.Fatalf("expected inherited defaults: %v", out)
	}
	if out["error"] != nil || out["invalid"] != nil {
		t.Fatalf("error/invalid should be null: %v", out)
	}
	models, _ := out["models"].([]any)
	if len(models) != 2 {
		t.Fatalf("expected 2 default models, got %v", models)
	}
	first, _ := models[0].(map[string]any)
	if first["id"] != "deepseek-flash" || first["systemPromptUpdate"] != "in-history" {
		t.Fatalf("first default = %v", first)
	}
	if first["imagePixelBudget"] != float64(640000) {
		t.Fatalf("imagePixelBudget = %v", first["imagePixelBudget"])
	}
	second, _ := models[1].(map[string]any)
	if second["id"] != "deepseek-v4-pro" {
		t.Fatalf("second default = %v", second)
	}
	if _, ok := second["inputModalities"]; ok {
		t.Fatalf("text-only entry must omit inputModalities: %v", second)
	}
	if _, ok := first["imageDetail"]; ok {
		t.Fatalf("imageDetail must never be serialized: %v", first)
	}
}

func TestDeepSeekCatalogLoadConfigured(t *testing.T) {
	dir := dshHome(t)
	doc := "# top comment\nother-plugin:\n  enabled: true\nllm-deepseek:\n  baseURL: https://x\n  models:\n    - id: my-model\n      contextWindow: 1000\n"
	if err := os.WriteFile(filepath.Join(dir, "settings.yaml"), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	srv := newTestServer(t)
	code, out := call(t, srv, "acp_load_deepseek_model_catalog", nil)
	if code != 200 {
		t.Fatalf("status %d", code)
	}
	if out["exists"] != true || out["configured"] != true {
		t.Fatalf("expected configured: %v", out)
	}
	if out["error"] != nil || out["invalid"] != nil {
		t.Fatalf("error/invalid should be null: %v", out)
	}
	models, _ := out["models"].([]any)
	if len(models) != 1 {
		t.Fatalf("models = %v", models)
	}
	if m, _ := models[0].(map[string]any); m["id"] != "my-model" {
		t.Fatalf("model = %v", models[0])
	}
}

func TestDeepSeekCatalogLoadReportsInvalidAndUnusable(t *testing.T) {
	dir := dshHome(t)
	srv := newTestServer(t)

	// A stored list the agent would refuse is reported in `invalid`.
	dup := "llm-deepseek:\n  models:\n    - id: a\n    - id: a\n"
	if err := os.WriteFile(filepath.Join(dir, "settings.yaml"), []byte(dup), 0o644); err != nil {
		t.Fatal(err)
	}
	_, out := call(t, srv, "acp_load_deepseek_model_catalog", nil)
	if out["configured"] != true {
		t.Fatalf("expected configured: %v", out)
	}
	invalid, _ := out["invalid"].(string)
	if !strings.Contains(invalid, "duplicate") {
		t.Fatalf("invalid = %v", out["invalid"])
	}

	// An unknown field is a document we must not rewrite: reported in `error`,
	// with the built-in list inherited.
	unknown := "llm-deepseek:\n  models:\n    - id: a\n      mystery: 1\n"
	if err := os.WriteFile(filepath.Join(dir, "settings.yaml"), []byte(unknown), 0o644); err != nil {
		t.Fatal(err)
	}
	_, out = call(t, srv, "acp_load_deepseek_model_catalog", nil)
	if out["configured"] != false || out["exists"] != true {
		t.Fatalf("expected inherited with exists: %v", out)
	}
	msg, _ := out["error"].(string)
	if !strings.Contains(msg, "mystery") {
		t.Fatalf("error = %v", out["error"])
	}
	if models, _ := out["models"].([]any); len(models) != 2 {
		t.Fatalf("expected defaults on error, got %v", models)
	}
}

func TestDeepSeekCatalogUpdateInsertsPreservingComments(t *testing.T) {
	dir := dshHome(t)
	doc := "# keep me\nother-plugin:\n  enabled: true\n"
	if err := os.WriteFile(filepath.Join(dir, "settings.yaml"), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	srv := newTestServer(t)
	code, out := call(t, srv, "acp_update_deepseek_model_catalog", map[string]any{
		"models": []any{map[string]any{"id": "m1", "contextWindow": 1234}},
	})
	if code != 200 {
		t.Fatalf("status %d: %v", code, out)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "settings.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "# keep me") {
		t.Fatalf("comment dropped:\n%s", string(raw))
	}
	root := readSettingsYAML(t, dir)
	if other, _ := root["other-plugin"].(map[string]any); other["enabled"] != true {
		t.Fatalf("other section changed: %v", root)
	}
	models := settingsModels(t, dir)
	if len(models) != 1 {
		t.Fatalf("models = %v", models)
	}
	if m, _ := models[0].(map[string]any); m["id"] != "m1" || m["contextWindow"] != 1234 {
		t.Fatalf("model = %v", models[0])
	}
}

func TestDeepSeekCatalogUpdateReplacesKeepingSiblingKeys(t *testing.T) {
	dir := dshHome(t)
	doc := "llm-deepseek:\n  baseURL: https://x\n  models:\n    - id: old\n      contextWindow: 1\n  thinking: high\n"
	if err := os.WriteFile(filepath.Join(dir, "settings.yaml"), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	srv := newTestServer(t)
	if code, out := call(t, srv, "acp_update_deepseek_model_catalog", map[string]any{
		"models": []any{map[string]any{"id": "new", "contextWindow": 2}},
	}); code != 200 {
		t.Fatalf("status %d: %v", code, out)
	}
	root := readSettingsYAML(t, dir)
	sec, _ := root["llm-deepseek"].(map[string]any)
	if sec["baseURL"] != "https://x" || sec["thinking"] != "high" {
		t.Fatalf("sibling keys disturbed: %v", sec)
	}
	models, _ := sec["models"].([]any)
	if len(models) != 1 {
		t.Fatalf("models = %v", models)
	}
	if m, _ := models[0].(map[string]any); m["id"] != "new" {
		t.Fatalf("model = %v", models[0])
	}

	// Clearing removes just `models`, leaving the section (and its other keys).
	if code, out := call(t, srv, "acp_update_deepseek_model_catalog", map[string]any{}); code != 200 {
		t.Fatalf("clear: %d %v", code, out)
	}
	root = readSettingsYAML(t, dir)
	sec, ok := root["llm-deepseek"].(map[string]any)
	if !ok {
		t.Fatalf("section should remain (it has other keys): %v", root)
	}
	if _, ok := sec["models"]; ok {
		t.Fatalf("models not removed: %v", sec)
	}
}

func TestDeepSeekCatalogClearDropsEmptySection(t *testing.T) {
	dir := dshHome(t)
	doc := "keep: 1\nllm-deepseek:\n  models:\n    - id: only\n      contextWindow: 1\n"
	if err := os.WriteFile(filepath.Join(dir, "settings.yaml"), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	srv := newTestServer(t)
	if code, out := call(t, srv, "acp_update_deepseek_model_catalog", map[string]any{
		"models": []any{},
	}); code != 200 {
		t.Fatalf("status %d: %v", code, out)
	}
	root := readSettingsYAML(t, dir)
	if _, ok := root["llm-deepseek"]; ok {
		t.Fatalf("empty section header should be dropped: %v", root)
	}
	if root["keep"] != 1 {
		t.Fatalf("unrelated key lost: %v", root)
	}
}

func TestDeepSeekCatalogUpdateValidation(t *testing.T) {
	dshHome(t)
	srv := newTestServer(t)
	cases := []map[string]any{
		// duplicate id
		{"models": []any{map[string]any{"id": "a"}, map[string]any{"id": "a"}}},
		// non-positive context window
		{"models": []any{map[string]any{"id": "a", "contextWindow": 0}}},
		// text-only entry declaring image limits
		{"models": []any{map[string]any{"id": "a", "imageMaxBytes": 10}}},
		// retired imageDetail key
		{"models": []any{map[string]any{"id": "a", "imageDetail": "high"}}},
		// unknown modality
		{"models": []any{map[string]any{"id": "a", "inputModalities": []any{"audio"}}}},
		// unknown systemPromptUpdate
		{"models": []any{map[string]any{"id": "a", "systemPromptUpdate": "always"}}},
		// missing id
		{"models": []any{map[string]any{"contextWindow": 1}}},
	}
	for i, c := range cases {
		if code, out := call(t, srv, "acp_update_deepseek_model_catalog", c); code != 500 {
			t.Errorf("case %d: expected 500, got %d %v", i, code, out)
		}
	}
}

func TestDeepSeekCatalogRefusesInlineSection(t *testing.T) {
	dir := dshHome(t)
	if err := os.WriteFile(filepath.Join(dir, "settings.yaml"), []byte("llm-deepseek: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	srv := newTestServer(t)
	code, out := call(t, srv, "acp_update_deepseek_model_catalog", map[string]any{
		"models": []any{map[string]any{"id": "m", "contextWindow": 1}},
	})
	if code != 500 {
		t.Fatalf("expected 500, got %d %v", code, out)
	}
	if msg, _ := out["message"].(string); !strings.Contains(msg, "inline") {
		t.Fatalf("message = %v", out["message"])
	}
}

func TestDeepSeekCatalogWriteFollowsSymlink(t *testing.T) {
	dir := dshHome(t)
	realDir := t.TempDir()
	real := filepath.Join(realDir, "real-settings.yaml")
	if err := os.WriteFile(real, []byte("keep: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "settings.yaml")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	srv := newTestServer(t)
	if code, out := call(t, srv, "acp_update_deepseek_model_catalog", map[string]any{
		"models": []any{map[string]any{"id": "m", "contextWindow": 1}},
	}); code != 200 {
		t.Fatalf("status %d: %v", code, out)
	}
	// The link is still a link; the real file got the content.
	if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("symlink replaced by a real file: %v %v", fi, err)
	}
	b, err := os.ReadFile(real)
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := yaml.Unmarshal(b, &root); err != nil {
		t.Fatalf("parse real file: %v", err)
	}
	if root["keep"] != 1 {
		t.Fatalf("unrelated key lost in real file: %v", root)
	}
	sec, _ := root["llm-deepseek"].(map[string]any)
	if sec == nil {
		t.Fatalf("models not written to the real file: %v", root)
	}
	// No temp files left behind in either directory.
	for _, d := range []string{dir, realDir} {
		entries, _ := os.ReadDir(d)
		for _, e := range entries {
			if strings.HasSuffix(e.Name(), ".tmp") {
				t.Fatalf("stale temp file %s in %s", e.Name(), d)
			}
		}
	}
}

// sanity: the response shape uses camelCase keys and null (not omitted) for the
// optional error/invalid fields.
func TestDeepSeekCatalogWireShape(t *testing.T) {
	dshHome(t)
	srv := newTestServer(t)
	_, raw := rawPostJSON(t, srv, "acp_load_deepseek_model_catalog", map[string]any{})
	var generic map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &generic); err != nil {
		t.Fatalf("decode: %v (%s)", err, raw)
	}
	for _, key := range []string{"path", "exists", "configured", "models", "error", "invalid"} {
		if _, ok := generic[key]; !ok {
			t.Fatalf("missing key %q in %s", key, raw)
		}
	}
	if string(generic["error"]) != "null" || string(generic["invalid"]) != "null" {
		t.Fatalf("error/invalid should be null: %s", raw)
	}
}
