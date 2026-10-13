package handlers_test

// acp_kimi_test.go covers acp_fetch_kimi_models: the OpenAI-compatible model
// list (sorted + de-duplicated), the provider's own error message on a non-2xx,
// and the required-field validation.

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAcpFetchKimiModels(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" {
			t.Errorf("path = %s, want /models", r.URL.Path)
		}
		if r.Header.Get("authorization") != "Bearer sk-x" {
			t.Errorf("auth = %q", r.Header.Get("authorization"))
		}
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"b"},{"id":"a"},{"id":"a"},{"nope":1}]}`))
	}))
	defer ts.Close()

	srv := newTestServer(t)
	code, arr := callArr(t, srv, "acp_fetch_kimi_models", map[string]any{"baseUrl": ts.URL + "/", "apiKey": "sk-x"})
	if code != 200 {
		t.Fatalf("status %d", code)
	}
	if len(arr) != 2 || arr[0] != "a" || arr[1] != "b" {
		t.Fatalf("ids = %v, want [a b]", arr)
	}
}

func TestAcpFetchKimiModelsErrors(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"bad key"}}`))
	}))
	defer ts.Close()

	srv := newTestServer(t)
	code, out := call(t, srv, "acp_fetch_kimi_models", map[string]any{"baseUrl": ts.URL, "apiKey": "sk-bad"})
	if code != 500 {
		t.Fatalf("status %d", code)
	}
	if msg, _ := out["message"].(string); msg != "401: bad key" {
		t.Errorf("message = %q, want %q", msg, "401: bad key")
	}

	if code, _ := call(t, srv, "acp_fetch_kimi_models", map[string]any{"baseUrl": "", "apiKey": "x"}); code != 500 {
		t.Errorf("empty base URL should fail, got %d", code)
	}
	if code, _ := call(t, srv, "acp_fetch_kimi_models", map[string]any{"baseUrl": "http://x", "apiKey": ""}); code != 500 {
		t.Errorf("empty api key should fail, got %d", code)
	}
}
