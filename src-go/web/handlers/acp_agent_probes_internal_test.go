package handlers

// acp_agent_probes_internal_test.go covers the unexported probe helpers: the
// cursor `models` parser (ANSI stripping, default markers, duplicates), the
// credential-verified judgement, and the probe env overlay semantics.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"yoyocode/src-go/acp"
	"yoyocode/src-go/db"
)

func TestParseCursorModels(t *testing.T) {
	stdout := "\x1b[1mAvailable models\x1b[0m\n" +
		"\n" +
		"auto - Auto (default)\n" +
		"claude-opus-4-8-high - Opus 4.8 1M\n" +
		"gpt-5.6-sol\n" + // bare id, no label
		"auto - Auto (current)\n" + // duplicate id → skipped
		"some prose line here\n" // has spaces → skipped
	models, def := parseCursorModels(stdout)
	if len(models) != 3 {
		t.Fatalf("models = %+v", models)
	}
	if models[0].ID != "auto" || models[0].Label != "Auto" || !models[0].IsDefault {
		t.Fatalf("first = %+v", models[0])
	}
	if models[1].ID != "claude-opus-4-8-high" || models[1].Label != "Opus 4.8 1M" || models[1].IsDefault {
		t.Fatalf("second = %+v", models[1])
	}
	if models[2].ID != "gpt-5.6-sol" || models[2].Label != "" {
		t.Fatalf("third = %+v", models[2])
	}
	if def == nil || *def != "auto" {
		t.Fatalf("default = %v", def)
	}
}

func TestParseCursorModelsCurrentMarker(t *testing.T) {
	models, def := parseCursorModels("solo - Solo (current)\n")
	if len(models) != 1 || !models[0].IsDefault {
		t.Fatalf("models = %+v", models)
	}
	if models[0].Label != "Solo" {
		t.Fatalf("label = %q", models[0].Label)
	}
	if def == nil || *def != "solo" {
		t.Fatalf("default = %v", def)
	}
}

func TestParseCursorModelsEmpty(t *testing.T) {
	models, def := parseCursorModels("")
	if models == nil || len(models) != 0 {
		t.Fatalf("models = %#v", models)
	}
	if def != nil {
		t.Fatalf("default = %v", def)
	}
}

func TestStripANSI(t *testing.T) {
	in := "\x1b[31mred\x1b[0m plain \x1b[1;32mgreen\x1b[m"
	if got := stripANSI(in); got != "red plain green" {
		t.Fatalf("stripANSI = %q", got)
	}
	if got := stripANSI("no escapes"); got != "no escapes" {
		t.Fatalf("stripANSI = %q", got)
	}
}

func TestCursorCredentialVerified(t *testing.T) {
	cases := []struct {
		name string
		v    map[string]any
		want *bool
	}{
		{"unauthenticated → nil", map[string]any{"isAuthenticated": false}, nil},
		{"authenticated, no message → verified", map[string]any{"isAuthenticated": true}, boolPtr(true)},
		{"authenticated, other message → verified", map[string]any{"isAuthenticated": true, "message": "ok"}, boolPtr(true)},
		{
			"authenticated, unverified marker → not verified",
			map[string]any{"isAuthenticated": true, "message": "Warning: Unable to fetch user details"},
			boolPtr(false),
		},
		{"missing flag → nil", map[string]any{}, nil},
	}
	for _, c := range cases {
		got := cursorCredentialVerified(c.v)
		if c.want == nil {
			if got != nil {
				t.Errorf("%s: got %v, want nil", c.name, *got)
			}
			continue
		}
		if got == nil || *got != *c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, *c.want)
		}
	}
}

func TestTruncateProbeOutput(t *testing.T) {
	if got := truncateProbeOutput("  short  "); got != "short" {
		t.Fatalf("got %q", got)
	}
	long := strings.Repeat("x", 500)
	got := truncateProbeOutput(long)
	if !strings.HasSuffix(got, "…") || len([]rune(got)) != 401 {
		t.Fatalf("len = %d, suffix ok = %v", len([]rune(got)), strings.HasSuffix(got, "…"))
	}
}

func TestProbeEnvRemovesEmptyAndSetsNonEmpty(t *testing.T) {
	t.Setenv("YOYOCODE_PROBE_TEST", "inherited")
	env := probeEnv(map[string]string{
		"YOYOCODE_PROBE_TEST": "",
		"YOYOCODE_PROBE_NEW":  "value",
	})
	for _, kv := range env {
		if strings.HasPrefix(kv, "YOYOCODE_PROBE_TEST=") {
			t.Fatalf("empty override should remove the var, got %q", kv)
		}
	}
	found := false
	for _, kv := range env {
		if kv == "YOYOCODE_PROBE_NEW=value" {
			found = true
		}
	}
	if !found {
		t.Fatal("non-empty override should be set")
	}
}

func TestCursorProbeEnvOverlay(t *testing.T) {
	database, err := db.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Conn.Close() })
	upsertAgentSetting(database, "cursor", map[string]any{
		"env_json": `{"CURSOR_API_KEY":"saved","OTHER":"keep"}`,
	})

	// API-key mode: the form value wins.
	env := cursorProbeEnv(database, " form-key ", true)
	if env["CURSOR_API_KEY"] != "form-key" {
		t.Fatalf("CURSOR_API_KEY = %q", env["CURSOR_API_KEY"])
	}
	if env["OTHER"] != "keep" {
		t.Fatalf("unrelated key dropped: %v", env)
	}
	if env["CURSOR_API_BASE_URL"] != "" {
		t.Fatalf("CURSOR_API_BASE_URL must be cleared: %q", env["CURSOR_API_BASE_URL"])
	}

	// Subscription mode (apiKey present but empty): the key is materialized
	// empty so the probe makes an explicit set-or-remove decision.
	env = cursorProbeEnv(database, "", true)
	if v, ok := env["CURSOR_API_KEY"]; !ok || v != "" {
		t.Fatalf("CURSOR_API_KEY = %q (present=%v)", v, ok)
	}

	// Absent param: a saved key survives, and the var is still materialized.
	env = cursorProbeEnv(database, "", false)
	if env["CURSOR_API_KEY"] != "saved" {
		t.Fatalf("saved key lost: %q", env["CURSOR_API_KEY"])
	}
}

func TestQoderProbeEnvMaterializesToken(t *testing.T) {
	database, err := db.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Conn.Close() })

	env := qoderProbeEnv(database, "  tok  ", true)
	if env["QODER_PERSONAL_ACCESS_TOKEN"] != "tok" {
		t.Fatalf("token = %q", env["QODER_PERSONAL_ACCESS_TOKEN"])
	}
	env = qoderProbeEnv(database, "", false)
	if v, ok := env["QODER_PERSONAL_ACCESS_TOKEN"]; !ok || v != "" {
		t.Fatalf("token must be materialized empty, got %q (present=%v)", v, ok)
	}
}

func TestAgentEnvJSONHandlesBadInput(t *testing.T) {
	database, err := db.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Conn.Close() })
	if got := agentEnvJSON(database, "cursor"); len(got) != 0 {
		t.Fatalf("missing row should be empty, got %v", got)
	}
	upsertAgentSetting(database, "cursor", map[string]any{"env_json": "not json"})
	if got := agentEnvJSON(database, "cursor"); len(got) != 0 {
		t.Fatalf("bad JSON should be empty, got %v", got)
	}
}

func TestFindBestCachedBinaryAndVersionCompare(t *testing.T) {
	// VersionCompare: numeric parts, missing parts = 0, ties by raw string.
	cases := []struct {
		a, b string
		want int
	}{
		{"1.2.3", "1.2.4", -1},
		{"1.10", "1.9", 1},
		{"2.0", "2", 1},      // equal parts → raw string tiebreak
		{"1.2", "1.2.0", -1}, // equal parts → raw string tiebreak (prefix sorts first)
		{"v1.2.3", "1.2.3", 1},
	}
	for _, c := range cases {
		got := acp.VersionCompare(c.a, c.b)
		if (got < 0) != (c.want < 0) || (got > 0) != (c.want > 0) {
			t.Errorf("acp.VersionCompare(%q,%q) = %d, want sign %d", c.a, c.b, got, c.want)
		}
	}
}

func TestProbeBinaryResolutionAbsent(t *testing.T) {
	// With an empty PATH and no cached binaries, the probes find nothing.
	t.Setenv("PATH", t.TempDir())
	if got := resolveCursorBinary(t.TempDir()); got != "" {
		t.Fatalf("resolveCursorBinary = %q", got)
	}
	if got := resolveQoderBinary(t.TempDir()); got != "" {
		t.Fatalf("resolveQoderBinary = %q", got)
	}
}

func boolPtr(v bool) *bool { return &v }

// TestFindBestCachedBinaryPicksNewest plants two cached versions and checks the
// highest one wins (and that a version dir without a launchable entry falls
// through).
func TestFindBestCachedBinaryPicksNewest(t *testing.T) {
	root := t.TempDir()
	dist, ok := acp.BinaryDistFor("cursor")
	if !ok {
		t.Skipf("cursor has no binary recipe on %s", acp.CurrentPlatform())
	}
	plant := func(version string) string {
		p := acp.CachedBinaryPath(root, "cursor", version)
		if p == "" {
			t.Fatalf("no cached path for %s", version)
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		return p
	}
	_ = plant("2025.01.01-aaa")
	newest := plant("2026.10.01-14929f9")
	// A newer version dir with no entry must be skipped.
	if err := os.MkdirAll(filepath.Join(root, "agent-binaries", "cursor", "2099.01.01-zzz"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, version, ok := acp.FindBestCachedBinary(root, "cursor")
	if !ok || got != newest || version != dist.Version {
		t.Fatalf("got (%q, %q, %v), want (%q, %q)", got, version, ok, newest, dist.Version)
	}
}
