package handlers_test

import "testing"

func TestConfigSyncSettingsRoundTrip(t *testing.T) {
	srv := newTestServer(t)

	// defaults
	_, def := call(t, srv, "config_sync_get_settings", map[string]any{})
	if def["enabled"] != false || def["remoteDir"] != "yoyocode" || def["profile"] != "default" {
		t.Fatalf("unexpected defaults: %v", def)
	}
	if def["autoSync"] != true || def["intervalMinutes"].(float64) != 5 {
		t.Fatalf("unexpected defaults: %v", def)
	}
	if def["hasPassword"] != false || def["hasPassphrase"] != false {
		t.Fatalf("defaults should have no secrets: %v", def)
	}

	// update: set server + password; interval clamps to <=1440
	_, v := call(t, srv, "config_sync_update_settings", map[string]any{
		"settings": map[string]any{
			"enabled":         true,
			"serverUrl":       "https://dav.example.com/dav",
			"username":        "user@example.com",
			"password":        "hunter2",
			"encrypt":         false,
			"autoSync":        true,
			"intervalMinutes": 99999,
		},
	})
	if v["hasPassword"] != true {
		t.Fatalf("hasPassword should be true after setting: %v", v)
	}
	if v["intervalMinutes"].(float64) != 1440 {
		t.Fatalf("interval should clamp to 1440, got %v", v["intervalMinutes"])
	}
	if v["serverUrl"] != "https://dav.example.com/dav" {
		t.Fatalf("serverUrl not persisted: %v", v)
	}

	// re-get: password persists (same account), empty password field keeps it
	_, v2 := call(t, srv, "config_sync_update_settings", map[string]any{
		"settings": map[string]any{
			"enabled":         true,
			"serverUrl":       "https://dav.example.com/dav",
			"username":        "user@example.com",
			"encrypt":         false,
			"autoSync":        false,
			"intervalMinutes": 10,
		},
	})
	if v2["hasPassword"] != true {
		t.Fatalf("password should be retained for same account: %v", v2)
	}
	if v2["autoSync"] != false {
		t.Fatalf("autoSync not updated: %v", v2)
	}

	// change account → password dropped
	_, v3 := call(t, srv, "config_sync_update_settings", map[string]any{
		"settings": map[string]any{
			"enabled":         true,
			"serverUrl":       "https://other.example.com/dav",
			"username":        "someone@else.com",
			"encrypt":         false,
			"autoSync":        false,
			"intervalMinutes": 10,
		},
	})
	if v3["hasPassword"] != false {
		t.Fatalf("password should be dropped on account change: %v", v3)
	}
}

func TestConfigSyncEncryptRequiresPassphrase(t *testing.T) {
	srv := newTestServer(t)
	code, _ := call(t, srv, "config_sync_update_settings", map[string]any{
		"settings": map[string]any{
			"enabled":         true,
			"serverUrl":       "https://dav.example.com/dav",
			"username":        "u",
			"encrypt":         true, // no passphrase → reject
			"autoSync":        true,
			"intervalMinutes": 5,
		},
	})
	if code == 200 {
		t.Fatalf("encrypt without passphrase should be rejected")
	}
}

func TestConfigSyncRejectsBadSegment(t *testing.T) {
	srv := newTestServer(t)
	code, _ := call(t, srv, "config_sync_update_settings", map[string]any{
		"settings": map[string]any{
			"enabled":         false,
			"serverUrl":       "",
			"username":        "",
			"remoteDir":       "a/b",
			"encrypt":         false,
			"autoSync":        true,
			"intervalMinutes": 5,
		},
	})
	if code == 200 {
		t.Fatalf("remoteDir with slash should be rejected")
	}
}

func TestConfigSyncGetState(t *testing.T) {
	srv := newTestServer(t)
	_, st := call(t, srv, "config_sync_get_state", map[string]any{})
	for _, k := range []string{"lastUploadedSha256", "lastUploadedTarget", "lastSyncAt", "lastError"} {
		if _, ok := st[k]; !ok {
			t.Fatalf("state missing key %s: %v", k, st)
		}
	}
}
