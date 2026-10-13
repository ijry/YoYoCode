package handlers

// acp_pi_catalog_internal_test.go covers the pi model-catalog query: the RPC
// answer parser, the runtime-path stability gate, the command/agent-dir
// resolution rules, and an end-to-end exchange against a fake pi.

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestParsePiModelCapabilities(t *testing.T) {
	// Not the answer to our request → matched=false.
	if _, matched, _ := parsePiModelCapabilities(`{"id":"other","type":"response"}`); matched {
		t.Fatal("foreign id must not match")
	}
	if _, matched, _ := parsePiModelCapabilities("not json"); matched {
		t.Fatal("non-JSON must not match")
	}
	if _, matched, _ := parsePiModelCapabilities(`{"id":"yoyocode-models","type":"response","command":"other"}`); matched {
		t.Fatal("other command must not match")
	}

	// A matching refusal → matched=true, ok=false.
	if _, matched, ok := parsePiModelCapabilities(
		`{"id":"yoyocode-models","type":"response","command":"get_available_models","success":false}`); !matched || ok {
		t.Fatalf("refusal: matched=%v ok=%v", matched, ok)
	}

	// A matching success.
	line := `{"id":"yoyocode-models","type":"response","command":"get_available_models","success":true,` +
		`"data":{"models":[{"provider":"anthropic","id":"claude-x","reasoning":true,` +
		`"thinkingLevelMap":{"low":null,"high":"x"}}]}}`
	models, matched, ok := parsePiModelCapabilities(line)
	if !matched || !ok {
		t.Fatalf("matched=%v ok=%v", matched, ok)
	}
	if len(models) != 1 || models[0].Provider != "anthropic" || models[0].ID != "claude-x" || !models[0].Reasoning {
		t.Fatalf("models = %+v", models)
	}
	if v, present := models[0].ThinkingLevelMap["low"]; !present || v != nil {
		t.Fatalf("low = %v (present=%v)", v, present)
	}
	if v, present := models[0].ThinkingLevelMap["high"]; !present || v == nil || *v != "x" {
		t.Fatalf("high = %v (present=%v)", v, present)
	}

	// A success with no models array is unreadable.
	if _, matched, ok := parsePiModelCapabilities(
		`{"id":"yoyocode-models","type":"response","command":"get_available_models","success":true,"data":{}}`); !matched || ok {
		t.Fatalf("missing models: matched=%v ok=%v", matched, ok)
	}

	// An entry with no thinkingLevelMap gets an empty map (never null on wire).
	models, _, ok = parsePiModelCapabilities(
		`{"id":"yoyocode-models","type":"response","command":"get_available_models","success":true,` +
			`"data":{"models":[{"provider":"p","id":"m"}]}}`)
	if !ok {
		t.Fatal("parse failed")
	}
	if models[0].ThinkingLevelMap == nil || len(models[0].ThinkingLevelMap) != 0 {
		t.Fatalf("thinkingLevelMap = %#v", models[0].ThinkingLevelMap)
	}
}

func TestPiCommandAndAgentDirResolution(t *testing.T) {
	t.Setenv("PI_ACP_PI_COMMAND", "")
	t.Setenv("PI_CODING_AGENT_DIR", "")
	// Default command.
	if got := piCommandForRuntimeEnv(map[string]string{}); got != "pi" {
		t.Fatalf("default command = %q", got)
	}
	// An exactly-empty override removes it → default.
	if got := piCommandForRuntimeEnv(map[string]string{"PI_ACP_PI_COMMAND": ""}); got != "pi" {
		t.Fatalf("empty override = %q", got)
	}
	// A real override wins, verbatim (not trimmed).
	if got := piCommandForRuntimeEnv(map[string]string{"PI_ACP_PI_COMMAND": " /x/pi "}); got != " /x/pi " {
		t.Fatalf("override = %q", got)
	}

	// Default agent dir is absolute and ends with .pi/agent.
	def := piAgentDirForRuntimeEnv(map[string]string{})
	if !filepath.IsAbs(def) || !strings.HasSuffix(filepath.ToSlash(def), "/.pi/agent") {
		t.Fatalf("default agent dir = %q", def)
	}
	// A runtime override wins.
	over := piAgentDirForRuntimeEnv(map[string]string{"PI_CODING_AGENT_DIR": filepath.Join(t.TempDir(), "custom")})
	if !filepath.IsAbs(over) || !strings.HasSuffix(filepath.ToSlash(over), "/custom") {
		t.Fatalf("override agent dir = %q", over)
	}
	// A relative override stays relative (that is what the stability gate sees).
	rel := piAgentDirForRuntimeEnv(map[string]string{"PI_CODING_AGENT_DIR": "rel/agent"})
	if filepath.IsAbs(rel) {
		t.Fatalf("relative override should stay relative, got %q", rel)
	}
}

func TestPiCommandLooksLikePath(t *testing.T) {
	for _, c := range []string{"/usr/bin/pi", "./pi", `C:\pi`, "a/b", "a\\b", "C:pi"} {
		if !piCommandLooksLikePath(c) {
			t.Errorf("%q should look like a path", c)
		}
	}
	for _, c := range []string{"pi", "pi-acp", "my.pi"} {
		if piCommandLooksLikePath(c) {
			t.Errorf("%q should not look like a path", c)
		}
	}
}

func TestPiRuntimePathsAreStable(t *testing.T) {
	abs := filepath.Join(t.TempDir(), "pi")
	if !piRuntimePathsAreStable(map[string]string{"PI_ACP_PI_COMMAND": abs}) {
		t.Fatal("absolute command + default agent dir should be stable")
	}
	if piRuntimePathsAreStable(map[string]string{"PI_ACP_PI_COMMAND": "./pi"}) {
		t.Fatal("a relative command must be refused")
	}
	if piRuntimePathsAreStable(map[string]string{"PI_CODING_AGENT_DIR": "rel/agent"}) {
		t.Fatal("a relative agent dir must be refused")
	}
}

func TestResolvePiCommandIn(t *testing.T) {
	cwd := t.TempDir()
	// A path command resolves against cwd.
	script := filepath.Join(cwd, "pi")
	if err := os.WriteFile(script, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, ok := resolvePiCommandIn("pi", nil, cwd) // "pi" is a name, not a path
	if ok {
		t.Fatalf("bare name should be looked up on PATH, got %q", got)
	}
	got, ok = resolvePiCommandIn("./pi", nil, cwd)
	if !ok || got != script {
		t.Fatalf("relative path command = (%q, %v), want (%q, true)", got, ok, script)
	}

	// A bare name resolves through a relative PATH entry anchored in cwd.
	env := map[string]string{"PATH": "."}
	got, ok = resolvePiCommandIn("pi", env, cwd)
	if !ok || got != filepath.Join(cwd, "pi") {
		t.Fatalf("PATH=. lookup = (%q, %v)", got, ok)
	}

	// An empty PATH entry means the cwd.
	env = map[string]string{"PATH": ""}
	if runtime.GOOS != "windows" {
		got, ok = resolvePiCommandIn("pi", env, cwd)
		if !ok || got != filepath.Join(cwd, "pi") {
			t.Fatalf("PATH= lookup = (%q, %v)", got, ok)
		}
	}

	// Not found anywhere.
	if _, ok := resolvePiCommandIn("definitely-absent-cmd", map[string]string{"PATH": t.TempDir()}, cwd); ok {
		t.Fatal("absent command should not resolve")
	}
	if _, ok := resolvePiCommandIn("", nil, cwd); ok {
		t.Fatal("empty command should not resolve")
	}
}

// writeFakePi writes an executable fake `pi` that answers the RPC request.
// Returns its absolute path.
func writeFakePi(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	if runtime.GOOS == "windows" {
		p := filepath.Join(dir, "pi.bat")
		content := "@echo off\r\nset /p line=\r\n" + strings.ReplaceAll(body, "\n", "\r\n") + "\r\n"
		if err := os.WriteFile(p, []byte(content), 0o755); err != nil {
			t.Fatal(err)
		}
		return p
	}
	p := filepath.Join(dir, "pi")
	if err := os.WriteFile(p, []byte("#!/bin/sh\nread line\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestQueryPiModelCatalogEndToEnd(t *testing.T) {
	answer := `{"id":"yoyocode-models","type":"response","command":"get_available_models","success":true,` +
		`"data":{"models":[{"provider":"openai","id":"gpt-x","reasoning":true,"thinkingLevelMap":{"low":"l"}}]}}`
	program := writeFakePi(t, "echo "+answer)
	env := map[string]string{"PI_ACP_PI_COMMAND": program}
	got := queryPiModelCatalog(env, 20*time.Second)
	if got.Status != "ok" {
		t.Fatalf("status = %q (models=%v)", got.Status, got.Models)
	}
	if len(got.Models) != 1 || got.Models[0].Provider != "openai" || got.Models[0].ID != "gpt-x" {
		t.Fatalf("models = %+v", got.Models)
	}
}

func TestQueryPiModelCatalogRefusal(t *testing.T) {
	program := writeFakePi(t, `echo {"id":"yoyocode-models","type":"response","command":"get_available_models","success":false}`)
	got := queryPiModelCatalog(map[string]string{"PI_ACP_PI_COMMAND": program}, 20*time.Second)
	if got.Status != "failed" {
		t.Fatalf("status = %q", got.Status)
	}
	if got.Models == nil || len(got.Models) != 0 {
		t.Fatalf("models must be an empty slice, got %#v", got.Models)
	}
}

func TestQueryPiModelCatalogStatuses(t *testing.T) {
	// Relative command → relative_path (no spawn attempted).
	if got := queryPiModelCatalog(map[string]string{"PI_ACP_PI_COMMAND": "./pi"}, time.Second); got.Status != "relative_path" {
		t.Fatalf("status = %q", got.Status)
	}
	// Absolute but missing → not_found.
	missing := filepath.Join(t.TempDir(), "nope-pi")
	if got := queryPiModelCatalog(map[string]string{"PI_ACP_PI_COMMAND": missing}, time.Second); got.Status != "not_found" {
		t.Fatalf("status = %q", got.Status)
	}
}

func TestQueryPiModelCatalogTimeout(t *testing.T) {
	sleep := "ping -n 5 127.0.0.1 >nul"
	if runtime.GOOS != "windows" {
		sleep = "sleep 5"
	}
	program := writeFakePi(t, sleep)
	got := queryPiModelCatalog(map[string]string{"PI_ACP_PI_COMMAND": program}, 500*time.Millisecond)
	if got.Status != "timed_out" {
		t.Fatalf("status = %q", got.Status)
	}
}
