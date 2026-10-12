package handlers_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestConfigSyncTestConnection(t *testing.T) {
	srv := newTestServer(t)

	// a fake WebDAV endpoint: 207 for good creds, 401 otherwise
	dav := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "PROPFIND" {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		user, pass, _ := r.BasicAuth()
		if user == "alice" && pass == "secret" {
			w.WriteHeader(http.StatusMultiStatus)
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer dav.Close()

	// good credentials → 200
	if code, _ := call(t, srv, "config_sync_test_connection", map[string]any{
		"settings": map[string]any{"serverUrl": dav.URL, "username": "alice", "password": "secret", "remoteDir": "yoyocode", "profile": "default"},
	}); code != 200 {
		t.Fatalf("good creds should succeed, got %d", code)
	}

	// bad credentials → auth error (non-200)
	if code, _ := call(t, srv, "config_sync_test_connection", map[string]any{
		"settings": map[string]any{"serverUrl": dav.URL, "username": "alice", "password": "wrong"},
	}); code == 200 {
		t.Fatalf("bad creds should fail")
	}

	// empty server url → invalid input
	if code, _ := call(t, srv, "config_sync_test_connection", map[string]any{
		"settings": map[string]any{"serverUrl": ""},
	}); code == 200 {
		t.Fatalf("empty server url should fail")
	}

	// unreachable host → network error (non-200), not a panic
	if code, _ := call(t, srv, "config_sync_test_connection", map[string]any{
		"settings": map[string]any{"serverUrl": "http://127.0.0.1:9/", "username": "x", "password": "y"},
	}); code == 200 {
		t.Fatalf("unreachable host should fail")
	}
}
