// Package web mirrors upstream src-tauri/src/web (the HTTP + WebSocket server).
package web

import (
	"encoding/json"
	"net/http"

	"yoyocode/src-go/app_error"
)

// HandlerFunc is the signature every API command handler implements.
type HandlerFunc func(c *Context) (any, error)

// Context carries the parsed request body and shared state.
type Context struct {
	Command string
	Body    map[string]any
	RawBody []byte
	W       http.ResponseWriter
	R       *http.Request
	State   any
	// Handled marks that the handler wrote the HTTP response itself (raw bytes,
	// a file stream, a zip). serveAPI then writes nothing further. Returning
	// (nil, nil) with Handled=true is how a command emits a non-JSON response.
	Handled bool
}

// ─── Command error constructors ───────────────────────────────────────────
// Thin aliases so handlers read like upstream's AppCommandError::new(...).

// They return the concrete *AppCommandError (not `error`) so callers can chain
// `.WithDetail(...)` / `.WithI18n(...)` exactly like upstream's builder.
func ErrInvalidInput(msg string) *apperror.AppCommandError {
	return apperror.New(apperror.CodeInvalidInput, msg)
}
func ErrNotFound(msg string) *apperror.AppCommandError {
	return apperror.New(apperror.CodeNotFound, msg)
}
func ErrAlreadyExists(msg string) *apperror.AppCommandError {
	return apperror.New(apperror.CodeAlreadyExists, msg)
}
func ErrPermissionDenied(msg string) *apperror.AppCommandError {
	return apperror.New(apperror.CodePermissionDenied, msg)
}
func ErrConfigMissing(msg string) *apperror.AppCommandError {
	return apperror.New(apperror.CodeConfigurationMissing, msg)
}
func ErrConfigInvalid(msg string) *apperror.AppCommandError {
	return apperror.New(apperror.CodeConfigurationInvalid, msg)
}
func ErrDependencyMissing(msg string) *apperror.AppCommandError {
	return apperror.New(apperror.CodeDependencyMissing, msg)
}
func ErrNotAGitRepository(msg string) *apperror.AppCommandError {
	return apperror.New(apperror.CodeNotAGitRepository, msg)
}
func ErrAuthFailed(msg string) *apperror.AppCommandError {
	return apperror.New(apperror.CodeAuthenticationFailed, msg)
}
func ErrNetwork(msg string) *apperror.AppCommandError {
	return apperror.New(apperror.CodeNetworkError, msg)
}
func ErrDatabase(msg string) *apperror.AppCommandError {
	return apperror.New(apperror.CodeDatabaseError, msg)
}
func ErrIO(msg string) *apperror.AppCommandError { return apperror.New(apperror.CodeIoError, msg) }
func ErrExternalCommand(msg string) *apperror.AppCommandError {
	return apperror.New(apperror.CodeExternalCommandFailed, msg)
}
func ErrWindowOperation(msg string) *apperror.AppCommandError {
	return apperror.New(apperror.CodeWindowOperationFailed, msg)
}
func ErrTaskExecution(msg string) *apperror.AppCommandError {
	return apperror.New(apperror.CodeTaskExecutionFailed, msg)
}
func ErrTurnInProgress(msg string) *apperror.AppCommandError {
	return apperror.New(apperror.CodeTurnInProgress, msg)
}

// ErrNotImplemented is the placeholder for commands not yet ported. Upstream
// has no such code, so it surfaces as a 500 task_execution_failed — loud
// enough that an unfinished command never looks like a silent success.
func ErrNotImplemented(cmd string) *apperror.AppCommandError {
	return apperror.New(apperror.CodeTaskExecutionFailed, "not implemented: "+cmd)
}

// ─── Response writers ─────────────────────────────────────────────────────

// WriteJSON writes a JSON response with the given status.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// WriteOK writes a 200 JSON response.
func WriteOK(w http.ResponseWriter, v any) { WriteJSON(w, http.StatusOK, v) }

// WriteErr writes an upstream-compatible error body.
//   - `*apperror.AppCommandError` → `{code,message,detail?,i18n_key?,i18n_params?}`
//     with the status derived from `code`.
//   - anything else → wrapped as `task_execution_failed` / 500.
func WriteErr(w http.ResponseWriter, err error) {
	appErr, ok := err.(*apperror.AppCommandError)
	if !ok {
		appErr = apperror.New(apperror.CodeTaskExecutionFailed, err.Error())
	}
	WriteJSON(w, appErr.Status(), appErr)
}

// WritePlain mirrors axum's `(StatusCode, &'static str)` responses — used for
// the auth middleware rejection and unknown-route 404, which upstream returns
// as bare text rather than JSON.
func WritePlain(w http.ResponseWriter, status int, text string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(text))
}
