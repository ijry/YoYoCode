package handlers

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"yoyocode/src-go/web"
)

func TestEnvLevelIsSet(t *testing.T) {
	t.Setenv("YOYOCODE_LOG", "")
	t.Setenv("RUST_LOG", "")
	if envLevelIsSet() {
		t.Error("expected not locked with no env")
	}
	t.Setenv("RUST_LOG", "debug")
	if !envLevelIsSet() {
		t.Error("expected locked with RUST_LOG set")
	}
}

func TestReadLogSettingsDefault(t *testing.T) {
	level, targets := readLogSettings(nil)
	if level != "info" || len(targets) != 0 {
		t.Fatalf("defaults = %q, %v", level, targets)
	}
}

func TestSetLogSettingsValidationAndNormalize(t *testing.T) {
	// Valid level, empty targets → targets omitted from the returned object.
	res, err := setLogSettings(&web.Context{Body: map[string]any{
		"settings": map[string]any{"level": "debug"},
	}})
	if err != nil {
		t.Fatalf("set: %v", err)
	}
	out := res.(map[string]any)
	if out["level"] != "debug" {
		t.Errorf("level = %v", out["level"])
	}
	if _, had := out["targets"]; had {
		t.Errorf("empty targets must be omitted: %v", out)
	}

	// Targets normalized and preserved.
	res, err = setLogSettings(&web.Context{Body: map[string]any{
		"settings": map[string]any{
			"level":   "info",
			"targets": []any{map[string]any{"target": "acp", "level": "trace"}},
		},
	}})
	if err != nil {
		t.Fatalf("set with targets: %v", err)
	}
	tg := res.(map[string]any)["targets"].([]any)
	if len(tg) != 1 || tg[0].(map[string]any)["target"] != "acp" {
		t.Errorf("targets = %v", tg)
	}

	// Invalid level rejected.
	if _, err := setLogSettings(&web.Context{Body: map[string]any{"settings": map[string]any{"level": "verbose"}}}); err == nil {
		t.Error("expected invalid-level error")
	}
	// Invalid target directive rejected.
	if _, err := setLogSettings(&web.Context{Body: map[string]any{"settings": map[string]any{
		"level": "info", "targets": []any{map[string]any{"target": "x", "level": "nope"}},
	}}}); err == nil {
		t.Error("expected invalid-target error")
	}
}

func TestListLogFiles(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"a.log", "b.log", "notes.txt"} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// b.log newer than a.log.
	old := time.Now().Add(-time.Hour)
	_ = os.Chtimes(filepath.Join(dir, "a.log"), old, old)

	files := listLogFiles(dir)
	if len(files) != 2 {
		t.Fatalf("expected 2 .log files, got %d: %v", len(files), files)
	}
	if files[0]["name"] != "b.log" {
		t.Errorf("expected newest (b.log) first, got %v", files[0]["name"])
	}
}

func TestReadLogFile(t *testing.T) {
	dir := t.TempDir()
	content := "line1\nline2\nline3\n"
	if err := os.WriteFile(filepath.Join(dir, "app.log"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	// Full read.
	full, err := readLogFile(dir, "app.log", 0)
	if err != nil || full.(string) != content {
		t.Fatalf("full read = %q, %v", full, err)
	}
	// Tail read: last 5 bytes.
	tail, err := readLogFile(dir, "app.log", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(tail.(string)) != 5 || tail.(string) != content[len(content)-5:] {
		t.Errorf("tail = %q", tail)
	}
	// Traversal / non-log / missing rejected.
	if _, err := readLogFile(dir, "../etc/passwd.log", 0); err == nil {
		t.Error("expected traversal rejection")
	}
	if _, err := readLogFile(dir, "app.txt", 0); err == nil {
		t.Error("expected non-log rejection")
	}
	if _, err := readLogFile(dir, "missing.log", 0); err == nil {
		t.Error("expected not-found")
	}
}
