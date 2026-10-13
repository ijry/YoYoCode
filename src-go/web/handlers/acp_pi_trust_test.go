package handlers_test

// acp_pi_trust_test.go covers the four pi project-trust routes end to end:
// the state projection's wire shape, the grant/revoke write path (into pi's
// trust.json), the acknowledgement store (our own file), and the review list.
//
// PI_CODING_AGENT_DIR is pinned to a temp dir so nothing touches the real
// ~/.pi/agent.

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// piTrustServer pins the pi agent dir and returns a server plus that dir.
func piTrustServer(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	agentDir := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", agentDir)
	srv, _ := newTestServerState(t)
	return srv, agentDir
}

// piWorkspaceKey asks the state route for the canonical key it uses, so the test
// never has to re-implement the realpath rule.
func piWorkspaceKey(t *testing.T, srv *httptest.Server, ws string) string {
	t.Helper()
	_, out := call(t, srv, "acp_pi_project_trust_state", map[string]any{"workspace": ws})
	key, _ := out["workspace"].(string)
	if key == "" {
		t.Fatalf("no workspace key in %v", out)
	}
	return key
}

func TestPiProjectTrustStateWireShape(t *testing.T) {
	srv, agentDir := piTrustServer(t)
	ws := t.TempDir()
	code, raw := rawPostJSON(t, srv, "acp_pi_project_trust_state", map[string]any{"workspace": ws})
	if code != 200 {
		t.Fatalf("status %d", code)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatalf("decode: %v (%s)", err, raw)
	}
	for _, key := range []string{"workspace", "resources", "decision", "decidedAt", "trustFile", "acknowledged"} {
		if _, ok := m[key]; !ok {
			t.Fatalf("missing key %q in %s", key, raw)
		}
	}
	if strings.Contains(string(raw), "trust_file") || strings.Contains(string(raw), "decided_at") {
		t.Fatalf("wire must be camelCase: %s", raw)
	}
	if string(m["decision"]) != "null" || string(m["decidedAt"]) != "null" {
		t.Fatalf("undecided state must report null: %s", raw)
	}
	if string(m["acknowledged"]) != "false" {
		t.Fatalf("acknowledged = %s", m["acknowledged"])
	}
	var resources []any
	if err := json.Unmarshal(m["resources"], &resources); err != nil {
		t.Fatalf("resources must be an array: %s", m["resources"])
	}
	var trustFile string
	if err := json.Unmarshal(m["trustFile"], &trustFile); err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(trustFile) != agentDir {
		t.Fatalf("trustFile = %q, want inside %q", trustFile, agentDir)
	}
}

func TestPiSetProjectTrustGrantAndRevoke(t *testing.T) {
	srv, agentDir := piTrustServer(t)
	ws := t.TempDir()
	key := piWorkspaceKey(t, srv, ws)

	// Grant.
	if code, out := call(t, srv, "acp_pi_set_project_trust", map[string]any{"workspace": ws, "trusted": true}); code != 200 {
		t.Fatalf("grant: %d %v", code, out)
	}
	_, out := call(t, srv, "acp_pi_project_trust_state", map[string]any{"workspace": ws})
	if out["decision"] != true {
		t.Fatalf("decision = %v", out["decision"])
	}
	if out["acknowledged"] != true {
		t.Fatalf("a grant must be acknowledged by construction: %v", out["acknowledged"])
	}
	if out["decidedAt"] != key {
		t.Fatalf("decidedAt = %v, want %v", out["decidedAt"], key)
	}

	// The decision really landed in pi's trust.json, keyed by the canonical dir.
	b, err := os.ReadFile(filepath.Join(agentDir, "trust.json"))
	if err != nil {
		t.Fatalf("read trust.json: %v", err)
	}
	var trust map[string]any
	if err := json.Unmarshal(b, &trust); err != nil {
		t.Fatalf("parse trust.json: %v", err)
	}
	if trust[key] != true {
		t.Fatalf("trust.json = %v (key %q)", trust, key)
	}

	// The review list shows it.
	code, arr := callArr(t, srv, "acp_pi_list_trust_entries", map[string]any{})
	if code != 200 || len(arr) != 1 {
		t.Fatalf("entries = %d %v", code, arr)
	}
	entry, _ := arr[0].(map[string]any)
	if entry["path"] != key || entry["trusted"] != true {
		t.Fatalf("entry = %v", entry)
	}

	// Revoke (trusted absent → null → clear our entry).
	if code, out := call(t, srv, "acp_pi_set_project_trust", map[string]any{"workspace": ws}); code != 200 {
		t.Fatalf("revoke: %d %v", code, out)
	}
	_, out = call(t, srv, "acp_pi_project_trust_state", map[string]any{"workspace": ws})
	if out["decision"] != nil {
		t.Fatalf("after revoke decision = %v", out["decision"])
	}
	if out["acknowledged"] != false {
		t.Fatalf("after revoke acknowledged = %v", out["acknowledged"])
	}
	if code, arr := callArr(t, srv, "acp_pi_list_trust_entries", map[string]any{}); code != 200 || len(arr) != 0 {
		t.Fatalf("entries after revoke = %d %v", code, arr)
	}
}

func TestPiSetProjectTrustDecline(t *testing.T) {
	srv, _ := piTrustServer(t)
	ws := t.TempDir()
	if code, out := call(t, srv, "acp_pi_set_project_trust", map[string]any{"workspace": ws, "trusted": false}); code != 200 {
		t.Fatalf("decline: %d %v", code, out)
	}
	_, out := call(t, srv, "acp_pi_project_trust_state", map[string]any{"workspace": ws})
	if out["decision"] != false {
		t.Fatalf("decision = %v", out["decision"])
	}
	if out["acknowledged"] != false {
		t.Fatalf("a decline must NOT be acknowledged: %v", out["acknowledged"])
	}
}

func TestPiAcknowledgeProjectTrust(t *testing.T) {
	srv, agentDir := piTrustServer(t)
	ws := t.TempDir()
	key := piWorkspaceKey(t, srv, ws)

	// Pre-seed a grant directly in pi's file (as an older build would have), so
	// the folder is trusted but not yet acknowledged here.
	seeded, err := json.Marshal(map[string]any{key: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "trust.json"), seeded, 0o644); err != nil {
		t.Fatal(err)
	}
	_, out := call(t, srv, "acp_pi_project_trust_state", map[string]any{"workspace": ws})
	if out["decision"] != true || out["acknowledged"] != false {
		t.Fatalf("seeded state = %v", out)
	}

	// Acknowledging records only OUR file; pi's trust.json is untouched.
	before, _ := os.ReadFile(filepath.Join(agentDir, "trust.json"))
	if code, o := call(t, srv, "acp_pi_acknowledge_project_trust", map[string]any{"workspace": ws}); code != 200 {
		t.Fatalf("ack: %d %v", code, o)
	}
	_, out = call(t, srv, "acp_pi_project_trust_state", map[string]any{"workspace": ws})
	if out["acknowledged"] != true {
		t.Fatalf("acknowledged = %v", out["acknowledged"])
	}
	after, _ := os.ReadFile(filepath.Join(agentDir, "trust.json"))
	if string(before) != string(after) {
		t.Fatalf("pi's trust.json must be untouched:\nbefore=%s\nafter=%s", before, after)
	}
}
