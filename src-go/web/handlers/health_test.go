package handlers_test

// TestHealthRequiresToken locks the upstream auth contract: /api/health sits on
// the PROTECTED router (src-tauri/src/web/router.rs), so the login page can
// probe it with a Bearer token. A missing or wrong token must 401; only the
// real token passes.

import (
	"bytes"
	"net/http"
	"testing"
)

func TestHealthRequiresToken(t *testing.T) {
	srv := newTestServer(t)

	post := func(token string) int {
		t.Helper()
		req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/health", bytes.NewReader([]byte("{}")))
		req.Header.Set("content-type", "application/json")
		if token != "" {
			req.Header.Set("authorization", "Bearer "+token)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("health: %v", err)
		}
		defer resp.Body.Close()
		return resp.StatusCode
	}

	if code := post(""); code != http.StatusUnauthorized {
		t.Errorf("health without token = %d, want 401", code)
	}
	if code := post("wrong"); code != http.StatusUnauthorized {
		t.Errorf("health with wrong token = %d, want 401", code)
	}
	if code := post("tok"); code != http.StatusOK {
		t.Errorf("health with valid token = %d, want 200", code)
	}
}
