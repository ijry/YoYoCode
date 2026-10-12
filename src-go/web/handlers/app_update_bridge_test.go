package handlers_test

// app_update + browser_bridge: faithful "unavailable in this build" states and
// the browser-bridge target validation.

import "testing"

func TestAppUpdate(t *testing.T) {
	srv := newTestServer(t)
	_, st := call(t, srv, "app_update_state", map[string]any{})
	if st["status"] != "idle" {
		t.Fatalf("app_update_state status = %v, want idle", st["status"])
	}
	if _, ok := st["capability"]; !ok {
		t.Fatalf("app_update_state missing capability: %v", st)
	}
	// mutating actions report unavailable in this build.
	if code, out := call(t, srv, "perform_app_update", map[string]any{}); code != 400 || out["code"] != "invalid_input" {
		t.Fatalf("perform_app_update: %d %v", code, out)
	}
	if code, _ := call(t, srv, "restart_app", map[string]any{}); code != 400 {
		t.Fatalf("restart_app status %d", code)
	}
	if code, _ := call(t, srv, "rollback_app", map[string]any{}); code != 400 {
		t.Fatalf("rollback_app status %d", code)
	}
}

func TestBrowserBridge(t *testing.T) {
	srv := newTestServer(t)
	_, st := call(t, srv, "browser_bridge_status", map[string]any{})
	if st["enabled"] != false {
		t.Fatalf("bridge status enabled = %v", st["enabled"])
	}

	// a non-http target is rejected as invalid_input (validated before the
	// disabled check).
	if code, out := call(t, srv, "browser_bridge_open", map[string]any{"url": "https://example.com", "tabId": "t1"}); code != 400 || out["code"] != "invalid_input" {
		t.Fatalf("non-http target: %d %v", code, out)
	}
	// a non-loopback http target is rejected too.
	if code, out := call(t, srv, "browser_bridge_open", map[string]any{"url": "http://example.com:3000", "tabId": "t1"}); code != 400 || out["code"] != "invalid_input" {
		t.Fatalf("non-loopback target: %d %v", code, out)
	}
	// a valid loopback target passes validation but the bridge is disabled →
	// configuration_missing (422).
	if code, out := call(t, srv, "browser_bridge_open", map[string]any{"url": "http://localhost:3000/docs?x=1", "tabId": "t1"}); code != 422 || out["code"] != "configuration_missing" {
		t.Fatalf("disabled bridge: %d %v", code, out)
	}
	// close is always ok.
	_, cl := call(t, srv, "browser_bridge_close", map[string]any{"tabId": "t1"})
	if cl["ok"] != true {
		t.Fatalf("bridge close = %v", cl)
	}
}

func TestAppUpdateCheckAndStatus(t *testing.T) {
	srv := newTestServer(t)

	_, res := call(t, srv, "check_app_update", map[string]any{})
	if res["currentVersion"] == nil || res["currentVersion"] == "" {
		t.Fatalf("check_app_update missing currentVersion: %v", res)
	}
	if res["selfUpdateSupported"] != false {
		t.Errorf("selfUpdateSupported = %v, want false", res["selfUpdateSupported"])
	}
	if v, ok := res["update"]; !ok || v != nil {
		t.Errorf("update should be present and null: %v", res["update"])
	}
	if res["liveProgress"] != true {
		t.Errorf("liveProgress = %v", res["liveProgress"])
	}
	if _, ok := res["capability"]; !ok {
		t.Errorf("missing capability: %v", res)
	}

	_, st := call(t, srv, "app_update_status", map[string]any{})
	if st["currentVersion"] == nil {
		t.Fatalf("app_update_status missing currentVersion: %v", st)
	}
	// self_update_blocker is omitted when nothing stands in the way.
	if _, ok := st["selfUpdateBlocker"]; ok {
		t.Errorf("selfUpdateBlocker should be omitted: %v", st)
	}
	if _, ok := st["update"]; ok {
		t.Errorf("status must not carry an update field: %v", st)
	}
}

func TestEventMetricsSnapshot(t *testing.T) {
	srv := newTestServer(t)
	_, res := call(t, srv, "debug/event_metrics", map[string]any{})
	for _, k := range []string{"emitted_count", "lagged_count", "ring_buffer_evict_count", "replay_count", "worker_queue_full_count"} {
		v, ok := res[k]
		if !ok {
			t.Fatalf("missing metric %q: %v", k, res)
		}
		// JSON numbers decode to float64; all counters are zero in this build.
		if f, ok := v.(float64); !ok || f != 0 {
			t.Errorf("%s = %v, want 0", k, v)
		}
	}
}
