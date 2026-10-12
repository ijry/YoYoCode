package handlers_test

import "testing"

func TestAcpAddRegistryAgentRejectsBuiltin(t *testing.T) {
	srv := newTestServer(t)
	// A built-in registry id is rejected before any network fetch happens.
	if code, _ := call(t, srv, "acp_add_registry_agent", map[string]any{"registryId": "claude-acp"}); code == 200 {
		t.Fatalf("adding a built-in registry id should be refused")
	}
	// A built-in alias too.
	if code, _ := call(t, srv, "acp_add_registry_agent", map[string]any{"registryId": "kimi"}); code == 200 {
		t.Fatalf("adding a built-in alias should be refused")
	}
	// Empty id → invalid input (no network).
	if code, _ := call(t, srv, "acp_add_registry_agent", map[string]any{"registryId": ""}); code == 200 {
		t.Fatalf("empty registryId should be refused")
	}
}
