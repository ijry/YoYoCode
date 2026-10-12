package handlers

import (
	"strings"
	"testing"
)

func baseDiagInputs() diagInputs {
	return diagInputs{
		node: cmdProbe{path: "/usr/bin/node", version: "v20.11.1"},
		npm:  cmdProbe{path: "/usr/bin/npm", version: "10.2.4"},
	}
}

func npxAgentInstalledUnresolved() *agentDiag {
	return &agentDiag{name: "Codex CLI", cmd: "codex-acp", distribution: "npx", dbVersion: "1.1.2"}
}

func TestRedactSecret(t *testing.T) {
	if got := redactSecret("NVM_DIR", "/home/u/.nvm"); got != "/home/u/.nvm" {
		t.Fatalf("non-secret key must pass through, got %q", got)
	}
	if got := redactSecret("SHELL", "/bin/zsh"); got != "/bin/zsh" {
		t.Fatalf("non-secret key must pass through, got %q", got)
	}
	masked := redactSecret("XAI_API_KEY", "sk-\u6d4b\u8bd5secret-1234567890")
	if !strings.Contains(masked, "\u2022") || strings.Contains(masked, "secret") {
		t.Fatalf("secret-ish key must be masked, got %q", masked)
	}
	if got := redactSecret("MY_TOKEN", "abcd"); got != "\u2022\u2022\u2022\u2022" {
		t.Fatalf("short secret fully masked, got %q", got)
	}
}

func TestParseNodeMajor(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want int
		ok   bool
	}{
		{"v20.11.1", 20, true},
		{"20.11.1", 20, true},
		{"nope", 0, false},
	} {
		got, ok := parseNodeMajor(tc.in)
		if ok != tc.ok || (ok && got != tc.want) {
			t.Fatalf("parseNodeMajor(%q) = %d,%v want %d,%v", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

func TestComputeVerdict(t *testing.T) {
	// node missing (base env)
	if v := computeVerdict(&diagInputs{}); v["code"] != "node_missing" {
		t.Fatalf("empty inputs → node_missing, got %v", v["code"])
	}
	// ok without agent
	bi := baseDiagInputs()
	if v := computeVerdict(&bi); v["code"] != "ok" {
		t.Fatalf("base inputs → ok, got %v", v["code"])
	}
	// user_prefix_not_on_path
	in := baseDiagInputs()
	a := npxAgentInstalledUnresolved()
	a.userPrefixBin = "/home/u/.yoyocode/npm-global/bin/codex-acp"
	in.agent = a
	v := computeVerdict(&in)
	if v["code"] != "user_prefix_not_on_path" || v["level"] != "fail" {
		t.Fatalf("expected user_prefix_not_on_path/fail, got %v", v)
	}
	// homebrew_bin_not_on_path
	in = baseDiagInputs()
	a = npxAgentInstalledUnresolved()
	a.homebrewBin = "/opt/homebrew/bin/codex-acp"
	in.agent = a
	if v := computeVerdict(&in); v["code"] != "homebrew_bin_not_on_path" {
		t.Fatalf("expected homebrew_bin_not_on_path, got %v", v["code"])
	}
	// terminal_only_path (recorded version + only terminal resolves)
	in = baseDiagInputs()
	a = npxAgentInstalledUnresolved()
	a.detectedVersion = "1.1.2"
	in.agent = a
	in.terminal.cmdResolved = "/Users/u/.nvm/versions/node/v20/bin/codex-acp"
	if v := computeVerdict(&in); v["code"] != "terminal_only_path" {
		t.Fatalf("expected terminal_only_path, got %v", v["code"])
	}
	// node_missing outranks terminal_only_path for an npx agent
	in = diagInputs{agent: &agentDiag{name: "Hermes", cmd: "hermes", distribution: "npx"}}
	in.terminal.cmdResolved = "/Users/u/.local/bin/hermes"
	if v := computeVerdict(&in); v["code"] != "node_missing" {
		t.Fatalf("node_missing should outrank terminal_only_path, got %v", v["code"])
	}
	// not_installed when nothing recorded and nothing resolves
	in = baseDiagInputs()
	in.agent = &agentDiag{name: "Hermes", cmd: "hermes", distribution: "npx"}
	if v := computeVerdict(&in); v["code"] != "not_installed" {
		t.Fatalf("expected not_installed, got %v", v["code"])
	}
}
