package apperror

import (
	"encoding/json"
	"net/http"
	"testing"
)

// The wire format is the compatibility contract with mcode-app: its
// `parseAppCommandError` requires BOTH `code` and `message` to be non-empty
// strings, and it branches recovery UX purely on `i18n_key`.
func TestWireFormatMinimal(t *testing.T) {
	raw, err := json.Marshal(New(CodeNotFound, "conversation not found"))
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	if body["code"] != "not_found" {
		t.Fatalf("want code not_found, got %#v", body["code"])
	}
	if body["message"] != "conversation not found" {
		t.Fatalf("bad message: %#v", body["message"])
	}
	// Optional fields must be ABSENT, not null — mcode-app's parser tolerates
	// null but the frontend's `i18n_key ?? null` branch is cleaner without them.
	for _, k := range []string{"detail", "i18n_key", "i18n_params"} {
		if _, ok := body[k]; ok {
			t.Fatalf("optional key %q must be omitted when unset: %s", k, raw)
		}
	}
}

func TestWireFormatWithI18n(t *testing.T) {
	e := New(CodeConfigurationMissing, "no forge account").
		WithDetail("host=git.example.com").
		WithI18n("Forge.errors.noAccount", Params("host", "git.example.com"))
	raw, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	if body["i18n_key"] != "Forge.errors.noAccount" {
		t.Fatalf("bad i18n_key: %#v", body["i18n_key"])
	}
	params, ok := body["i18n_params"].(map[string]any)
	if !ok || params["host"] != "git.example.com" {
		t.Fatalf("bad i18n_params: %#v", body["i18n_params"])
	}
	if body["detail"] != "host=git.example.com" {
		t.Fatalf("bad detail: %#v", body["detail"])
	}
}

// WithI18n drops an empty param map rather than emitting `{}` — upstream's
// `if !params.is_empty()` guard exists so the common no-param case stays
// byte-identical to the pre-feature shape.
func TestEmptyI18nParamsOmitted(t *testing.T) {
	raw, err := json.Marshal(New(CodeInvalidInput, "bad").WithI18n("some.key", nil))
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	if _, ok := body["i18n_params"]; ok {
		t.Fatalf("empty params must be omitted: %s", raw)
	}
	if body["i18n_key"] != "some.key" {
		t.Fatalf("key must survive: %s", raw)
	}
}

// Status mapping mirrors `status_for_app_error_code` in upstream
// web/handlers/error.rs. The 422 group matters most: forge's three recoverable
// failures all share `configuration_missing` and are told apart by i18n_key.
func TestStatusMapping(t *testing.T) {
	cases := []struct {
		code AppErrorCode
		want int
	}{
		{CodeInvalidInput, http.StatusBadRequest},
		{CodeNotFound, http.StatusNotFound},
		{CodeAlreadyExists, http.StatusConflict},
		{CodeTurnInProgress, http.StatusConflict},
		{CodePermissionDenied, http.StatusForbidden},
		{CodeConfigurationMissing, http.StatusUnprocessableEntity},
		{CodeConfigurationInvalid, http.StatusUnprocessableEntity},
		{CodeDependencyMissing, http.StatusUnprocessableEntity},
		{CodeNotAGitRepository, http.StatusUnprocessableEntity},
		{CodeAuthenticationFailed, http.StatusUnprocessableEntity},
		{CodeNetworkError, http.StatusInternalServerError},
		{CodeDatabaseError, http.StatusInternalServerError},
		{CodeIoError, http.StatusInternalServerError},
		{CodeExternalCommandFailed, http.StatusInternalServerError},
		{CodeWindowOperationFailed, http.StatusInternalServerError},
		{CodeTaskExecutionFailed, http.StatusInternalServerError},
	}
	for _, c := range cases {
		if got := StatusForCode(c.code); got != c.want {
			t.Errorf("code %s: want %d, got %d", c.code, c.want, got)
		}
	}
}
