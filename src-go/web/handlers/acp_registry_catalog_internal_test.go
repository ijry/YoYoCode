package handlers

import "testing"

func TestDistributionKindsOf(t *testing.T) {
	spec := map[string]any{
		"binary": map[string]any{"linux-x86_64": map[string]any{"archive": "u"}},
		"npx":    map[string]any{"package": "p"},
	}
	got := distributionKindsOf(spec)
	if len(got) != 2 || got[0] != "binary" || got[1] != "npx" {
		t.Fatalf("expected [binary npx], got %v", got)
	}
	if distributionKindsOf(map[string]any{"uvx": map[string]any{"package": "p"}})[0] != "uvx" {
		t.Fatalf("uvx-only spec")
	}
	if len(distributionKindsOf(map[string]any{})) != 0 {
		t.Fatalf("empty spec → no kinds")
	}
}

func TestKindRunsHere(t *testing.T) {
	if !kindRunsHere("npx", map[string]any{}) {
		t.Fatalf("npx always runs here")
	}
	if !kindRunsHere("uvx", map[string]any{}) {
		t.Fatalf("uvx always runs here")
	}
	// binary with no archive for this platform → false
	spec := map[string]any{"binary": map[string]any{"nonexist-platform": map[string]any{"archive": "u"}}}
	if kindRunsHere("binary", spec) {
		t.Fatalf("binary without this-platform archive should not run here")
	}
	// binary present for this platform → true
	here := map[string]any{"binary": map[string]any{acpRegistryPlatformKey(): map[string]any{"archive": "u"}}}
	if !kindRunsHere("binary", here) {
		t.Fatalf("binary with this-platform archive should run here")
	}
	// empty archive → false
	empty := map[string]any{"binary": map[string]any{acpRegistryPlatformKey(): map[string]any{"archive": "  "}}}
	if kindRunsHere("binary", empty) {
		t.Fatalf("empty archive should not run here")
	}
}

func TestEffectiveRegistryKind(t *testing.T) {
	spec := map[string]any{"npx": map[string]any{"package": "p"}}
	if k, err := effectiveRegistryKind(spec, ""); err != nil || k != "npx" {
		t.Fatalf("default npx, got %q %v", k, err)
	}
	if _, err := effectiveRegistryKind(spec, "uvx"); err == nil {
		t.Fatalf("explicit uvx not published → error")
	}
	if _, err := effectiveRegistryKind(map[string]any{}, ""); err == nil {
		t.Fatalf("no distribution → error")
	}
}

func TestIsBuiltinRegistryID(t *testing.T) {
	if !isBuiltinRegistryID("kimi") || !isBuiltinRegistryID("qoder") {
		t.Fatalf("aliases must count as builtin")
	}
	if !isBuiltinRegistryID("claude-acp") {
		t.Fatalf("claude-acp is a builtin registry id")
	}
	if isBuiltinRegistryID("totally-not-a-builtin") {
		t.Fatalf("unknown id must not be builtin")
	}
}
