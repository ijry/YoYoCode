// Package apperror mirrors upstream src-tauri/src/app_error.rs.
//
// The wire format here is part of the compatibility contract: mcode-app's
// `parseAppCommandError` (src/services/gateway/commandError.ts) requires BOTH
// `code` and `message` to be present strings before it treats a response body
// as a structured error — and it branches recovery UX purely on `i18n_key`
// (never on `code`, which is shared across several failure modes).
package apperror

import (
	"encoding/json"
	"net/http"
	"sort"
)

// AppErrorCode mirrors upstream `AppErrorCode` with serde(rename_all="snake_case").
type AppErrorCode string

const (
	CodeInvalidInput          AppErrorCode = "invalid_input"
	CodeConfigurationMissing  AppErrorCode = "configuration_missing"
	CodeConfigurationInvalid  AppErrorCode = "configuration_invalid"
	CodeNotFound              AppErrorCode = "not_found"
	CodeNotAGitRepository     AppErrorCode = "not_a_git_repository"
	CodeAlreadyExists         AppErrorCode = "already_exists"
	CodePermissionDenied      AppErrorCode = "permission_denied"
	CodeDependencyMissing     AppErrorCode = "dependency_missing"
	CodeNetworkError          AppErrorCode = "network_error"
	CodeAuthenticationFailed  AppErrorCode = "authentication_failed"
	CodeDatabaseError         AppErrorCode = "database_error"
	CodeIoError               AppErrorCode = "io_error"
	CodeExternalCommandFailed AppErrorCode = "external_command_failed"
	CodeWindowOperationFailed AppErrorCode = "window_operation_failed"
	CodeTaskExecutionFailed   AppErrorCode = "task_execution_failed"
	// TurnInProgress maps to HTTP 409: an expected, recoverable condition in
	// multi-client co-control (a second concurrent send), not a server fault.
	CodeTurnInProgress AppErrorCode = "turn_in_progress"
)

// StatusForCode mirrors `status_for_app_error_code` in
// src-tauri/src/web/handlers/error.rs. Keep in lockstep: forge's three
// recoverable failures (noAccount / unsupportedHost / wrongForge) are all 422
// and are discriminated by i18n_key, not by code.
func StatusForCode(code AppErrorCode) int {
	switch code {
	case CodeInvalidInput:
		return http.StatusBadRequest
	case CodeNotFound:
		return http.StatusNotFound
	case CodeAlreadyExists, CodeTurnInProgress:
		return http.StatusConflict
	case CodePermissionDenied:
		return http.StatusForbidden
	case CodeConfigurationMissing, CodeConfigurationInvalid, CodeDependencyMissing,
		CodeNotAGitRepository, CodeAuthenticationFailed:
		return http.StatusUnprocessableEntity
	default:
		// network_error / database_error / io_error / external_command_failed /
		// window_operation_failed / task_execution_failed
		return http.StatusInternalServerError
	}
}

// AppCommandError is the structured error body returned by every API command.
// Optional fields are omitted when unset (upstream uses
// skip_serializing_if = "Option::is_none").
type AppCommandError struct {
	Code       AppErrorCode      `json:"code"`
	Message    string            `json:"message"`
	Detail     string            `json:"detail,omitempty"`
	I18nKey    string            `json:"i18n_key,omitempty"`
	I18nParams map[string]string `json:"i18n_params,omitempty"`
}

// New constructs an AppCommandError with just a code and message.
func New(code AppErrorCode, message string) *AppCommandError {
	return &AppCommandError{Code: code, Message: message}
}

// WithDetail attaches a human-readable detail string.
func (e *AppCommandError) WithDetail(detail string) *AppCommandError {
	e.Detail = detail
	return e
}

// WithI18n attaches a dotted i18n key and its interpolation params. Mirrors
// upstream `with_i18n`: params is dropped when empty so the wire format omits
// `i18n_params` entirely rather than emitting `{}`.
func (e *AppCommandError) WithI18n(key string, params map[string]string) *AppCommandError {
	e.I18nKey = key
	if len(params) > 0 {
		cp := make(map[string]string, len(params))
		for k, v := range params {
			cp[k] = v
		}
		e.I18nParams = cp
	}
	return e
}

func (e *AppCommandError) Error() string { return e.Message }

// Status returns the HTTP status upstream would use for this error's code.
func (e *AppCommandError) Status() int { return StatusForCode(e.Code) }

// MarshalJSON emits keys in the upstream field order (code, message, detail,
// i18n_key, i18n_params) so byte-level diffs against upstream stay readable.
func (e *AppCommandError) MarshalJSON() ([]byte, error) {
	type plain struct {
		Code       AppErrorCode      `json:"code"`
		Message    string            `json:"message"`
		Detail     string            `json:"detail,omitempty"`
		I18nKey    string            `json:"i18n_key,omitempty"`
		I18nParams map[string]string `json:"i18n_params,omitempty"`
	}
	return json.Marshal(plain(*e))
}

// Params is a convenience builder for i18n params.
func Params(pairs ...string) map[string]string {
	if len(pairs) == 0 {
		return nil
	}
	out := make(map[string]string, len(pairs)/2)
	for i := 0; i+1 < len(pairs); i += 2 {
		out[pairs[i]] = pairs[i+1]
	}
	return out
}

// SortedKeys returns the i18n param keys in deterministic order (BTreeMap
// semantics upstream); useful for tests and for template rendering.
func (e *AppCommandError) SortedKeys() []string {
	keys := make([]string, 0, len(e.I18nParams))
	for k := range e.I18nParams {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// ─── Shared i18n keys ────────────────────────────────────────────────────
// Mirrors the constants at the top of upstream app_error.rs. These MUST stay in
// lockstep with the TypeScript constants in upstream src/lib/api.ts and with
// mcode-app's forge error branching.

const (
	I18nUploadTooLarge    = "errors.upload.tooLarge"
	I18nUploadNotAFile    = "errors.upload.notAFile"
	I18nUploadQuotaExceed = "errors.upload.quotaExceeded"
)
