package handlers_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestConfigSyncRemoteRoundTrip(t *testing.T) {
	setHome(t)
	t.Setenv("YOYOCODE_HOME", t.TempDir())
	srv := newTestServer(t)

	// in-memory WebDAV: MKCOL→201, PUT→store, GET→body|404, PROPFIND→207
	var mu sync.Mutex
	store := map[string][]byte{}
	dav := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, _ := r.BasicAuth()
		if user != "alice" || pass != "secret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		switch r.Method {
		case "MKCOL", "PROPFIND":
			w.WriteHeader(http.StatusCreated)
		case "PUT":
			b, _ := io.ReadAll(r.Body)
			store[r.URL.Path] = b
			w.WriteHeader(http.StatusCreated)
		case "GET":
			if b, ok := store[r.URL.Path]; ok {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write(b)
			} else {
				w.WriteHeader(http.StatusNotFound)
			}
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	defer dav.Close()

	// seed a quick message so the snapshot carries something
	if code, _ := call(t, srv, "quick_messages_create", map[string]any{"title": "Hi", "content": "hello"}); code != 200 {
		t.Fatalf("seed failed")
	}

	// store settings (persists the password under its own key)
	if code, _ := call(t, srv, "config_sync_update_settings", map[string]any{
		"settings": map[string]any{"enabled": true, "serverUrl": dav.URL, "username": "alice", "password": "secret", "remoteDir": "yoyocode", "profile": "default", "intervalMinutes": 5},
	}); code != 200 {
		t.Fatalf("update_settings failed")
	}

	// upload_now → uploaded:true, files land on the server
	code, up := call(t, srv, "config_sync_upload_now", map[string]any{})
	if code != 200 {
		t.Fatalf("upload_now status = %d (%v)", code, up)
	}
	if up["uploaded"] != true || up["sha256"] == nil {
		t.Fatalf("upload result wrong: %v", up)
	}
	mu.Lock()
	hasConfig, hasManifest := false, false
	for p := range store {
		if strings.HasSuffix(p, "/config.json") {
			hasConfig = true
		}
		if strings.HasSuffix(p, "/manifest.json") {
			hasManifest = true
		}
	}
	mu.Unlock()
	if !hasConfig || !hasManifest {
		t.Fatalf("config/manifest not uploaded: config=%v manifest=%v", hasConfig, hasManifest)
	}

	// second upload without force → unchanged, nothing sent
	if _, up2 := call(t, srv, "config_sync_upload_now", map[string]any{}); up2["uploaded"] != false {
		t.Fatalf("unchanged upload should be a no-op, got %v", up2)
	}

	// peek_remote → returns the manifest
	code3, peek := call(t, srv, "config_sync_peek_remote", map[string]any{})
	if code3 != 200 || peek["schemaVersion"] == nil || peek["config"] == nil {
		t.Fatalf("peek_remote wrong: %d %v", code3, peek)
	}

	// download_apply → applies, returns manifest + applied + rollbackPath
	code4, dn := call(t, srv, "config_sync_download_apply", map[string]any{})
	if code4 != 200 {
		t.Fatalf("download_apply status = %d (%v)", code4, dn)
	}
	if dn["manifest"] == nil || dn["applied"] == nil {
		t.Fatalf("download_apply result wrong: %v", dn)
	}

	// peek against an empty remote (different profile) → null
	if code5, _ := call(t, srv, "config_sync_update_settings", map[string]any{
		"settings": map[string]any{"enabled": true, "serverUrl": dav.URL, "username": "alice", "password": "secret", "remoteDir": "yoyocode", "profile": "other", "intervalMinutes": 5},
	}); code5 != 200 {
		t.Fatalf("update_settings(profile=other) failed")
	}
	if _, peek2 := call(t, srv, "config_sync_peek_remote", map[string]any{}); peek2 != nil && len(peek2) != 0 {
		t.Fatalf("peek of empty remote should be null, got %v", peek2)
	}
}
