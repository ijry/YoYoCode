package web

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// These tests lock the HTTP half of the mcode-app compatibility contract.
// They deliberately assert on the RAW response body, because the contract is
// about wire format — not about our internal types:
//
//   - 401 / 404 are bare text (axum's defaults), NOT JSON
//   - command failures are AppCommandError JSON with BOTH `code` and `message`
//   - the status code follows upstream's status_for_app_error_code table
//
// mcode-app's parseAppCommandError requires both strings before it treats a
// body as structured; without them the client silently falls back to generic
// text and every i18n_key-driven recovery branch dies.

func newCompatServer(t *testing.T) *httptest.Server {
	t.Helper()
	r := NewRouter(newStubState())
	r.SetToken("tok")
	r.RegisterPublic("health", func(c *Context) (any, error) {
		return map[string]any{"status": "ok"}, nil
	})
	r.Register("boom_invalid", func(c *Context) (any, error) {
		return nil, ErrInvalidInput("conversationId required")
	})
	r.Register("boom_notfound", func(c *Context) (any, error) {
		return nil, ErrNotFound("conversation not found")
	})
	r.Register("boom_conflict", func(c *Context) (any, error) {
		return nil, ErrTurnInProgress("a turn is already in flight")
	})
	r.Register("boom_unconfigured", func(c *Context) (any, error) {
		return nil, ErrConfigMissing("no forge account").WithI18n("Forge.errors.noAccount", nil)
	})
	r.Register("boom_untyped", func(c *Context) (any, error) {
		return nil, io.ErrUnexpectedEOF
	})
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv
}

func postJSON(t *testing.T, url string, auth bool) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("content-type", "application/json")
	if auth {
		req.Header.Set("authorization", "Bearer tok")
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return res.StatusCode, string(body)
}

func TestCompatUnauthorizedIsBareText(t *testing.T) {
	srv := newCompatServer(t)
	status, body := postJSON(t, srv.URL+"/api/boom_invalid", false)
	if status != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", status)
	}
	if strings.TrimSpace(body) != "Invalid or missing token" {
		t.Fatalf("want bare text body, got %q", body)
	}
	var probe map[string]any
	if err := json.Unmarshal([]byte(body), &probe); err == nil {
		t.Fatalf("401 must NOT be JSON (axum returns a plain &'static str): %q", body)
	}
}

func TestCompatUnknownRouteIsBareText(t *testing.T) {
	srv := newCompatServer(t)
	status, body := postJSON(t, srv.URL+"/api/no_such_command", true)
	if status != http.StatusNotFound {
		t.Fatalf("want 404, got %d", status)
	}
	var probe map[string]any
	if err := json.Unmarshal([]byte(body), &probe); err == nil {
		t.Fatalf("404 must NOT be JSON: %q", body)
	}
}

// The table-driven half of the contract: status mapping + the code/message gate.
func TestCompatErrorEnvelopeAndStatusMapping(t *testing.T) {
	srv := newCompatServer(t)
	cases := []struct {
		cmd    string
		status int
		code   string
	}{
		{"boom_invalid", http.StatusBadRequest, "invalid_input"},
		{"boom_notfound", http.StatusNotFound, "not_found"},
		{"boom_conflict", http.StatusConflict, "turn_in_progress"},
		{"boom_unconfigured", http.StatusUnprocessableEntity, "configuration_missing"},
		// An untyped error must still reach the client as a structured body —
		// otherwise mcode-app's error branch sees an unparseable payload.
		{"boom_untyped", http.StatusInternalServerError, "task_execution_failed"},
	}
	for _, c := range cases {
		status, body := postJSON(t, srv.URL+"/api/"+c.cmd, true)
		if status != c.status {
			t.Errorf("%s: want status %d, got %d (body %s)", c.cmd, c.status, status, body)
			continue
		}
		var parsed map[string]any
		if err := json.Unmarshal([]byte(body), &parsed); err != nil {
			t.Errorf("%s: body is not JSON: %q", c.cmd, body)
			continue
		}
		code, _ := parsed["code"].(string)
		message, _ := parsed["message"].(string)
		if strings.TrimSpace(code) == "" || strings.TrimSpace(message) == "" {
			t.Errorf("%s: mcode-app requires BOTH code and message — got %q", c.cmd, body)
		}
		if code != c.code {
			t.Errorf("%s: want code %q, got %q", c.cmd, c.code, code)
		}
	}
}

func TestCompatI18nKeySurvivesTheWire(t *testing.T) {
	srv := newCompatServer(t)
	_, body := postJSON(t, srv.URL+"/api/boom_unconfigured", true)
	var parsed map[string]any
	if err := json.Unmarshal([]byte(body), &parsed); err != nil {
		t.Fatalf("not JSON: %q", body)
	}
	// forge's three recoverable failures all share `configuration_missing`;
	// i18n_key is the ONLY discriminator the client has.
	if parsed["i18n_key"] != "Forge.errors.noAccount" {
		t.Fatalf("i18n_key lost: %q", body)
	}
	if _, ok := parsed["i18n_params"]; ok {
		t.Fatalf("empty params must be omitted: %q", body)
	}
}

func TestCompatSuccessBodiesAreUntouched(t *testing.T) {
	srv := newCompatServer(t)
	status, body := postJSON(t, srv.URL+"/api/health", true)
	if status != http.StatusOK {
		t.Fatalf("want 200, got %d", status)
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(body), &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed["status"] != "ok" {
		t.Fatalf("bad body: %s", body)
	}
}
