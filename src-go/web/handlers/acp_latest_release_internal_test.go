package handlers

import "testing"

func TestVersionFromSpec(t *testing.T) {
	cases := map[string]string{
		"@agentclientprotocol/claude-agent-acp@0.86.0": "0.86.0",
		"deepseek-acp@0.9.0":                           "0.9.0",
		"pkg":                                          "",
		"@scope/pkg":                                   "",
	}
	for spec, want := range cases {
		if got := versionFromSpec(spec); got != want {
			t.Fatalf("versionFromSpec(%q) = %q, want %q", spec, got, want)
		}
	}
}

func TestIsStrictlyNewer(t *testing.T) {
	if !isStrictlyNewer("1.2.4", "1.2.3") {
		t.Fatal("1.2.4 > 1.2.3")
	}
	if isStrictlyNewer("1.2.3", "1.2.3") {
		t.Fatal("equal is not strictly newer")
	}
	if isStrictlyNewer("1.2.2", "1.2.3") {
		t.Fatal("older is not newer")
	}
	if !isStrictlyNewer("2.0.0", "1.9.9") {
		t.Fatal("major bump newer")
	}
	if !isStrictlyNewer("v0.87.0", "0.86.0") {
		t.Fatal("v-prefix tolerated")
	}
}

func TestSanitizeCustomVersion(t *testing.T) {
	if sanitizeCustomVersion("1.2.3") != "1.2.3" {
		t.Fatal("plain version")
	}
	if sanitizeCustomVersion("v2.1.0") != "2.1.0" {
		t.Fatal("strip v")
	}
	if sanitizeCustomVersion("2") != "" {
		t.Fatal("bare major rejected (needs dot)")
	}
	if sanitizeCustomVersion("latest") != "" {
		t.Fatal("non-numeric rejected")
	}
}
