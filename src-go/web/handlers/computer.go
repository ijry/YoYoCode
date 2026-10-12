package handlers

// Computer-use endpoints (upstream web/handlers/computer.rs). Computer use lets
// web clients drive the screen the server runs on, and is offered ONLY by a
// desktop session that opted in (YOYOCODE_COMPUTER_USE=1) — the headless Go server
// build has no computer-use service wired, so computer_available answers
// available=false and every operating call is refused up front with the same
// configuration_invalid message the upstream uses. This keeps mcode-app's
// capability probe (computer_available) working and makes the rest fail
// legibly rather than 404.

import (
	"runtime"

	"yoyocode/src-go/web"
)

func computerPlatformName() string {
	switch runtime.GOOS {
	case "darwin":
		return "macos"
	case "windows":
		return "windows"
	default:
		return "linux"
	}
}

const computerUnavailableMsg = "computer use is not available here: yoyocode-server offers it only when started with YOYOCODE_COMPUTER_USE=1 in a desktop session, and the desktop app only in its own window"

func computerUnavailable() error { return web.ErrConfigInvalid(computerUnavailableMsg) }

// RegisterComputer wires the computer-use endpoints. Only computer_available
// answers; the rest report the service is not served here.
func RegisterComputer(reg *Registry) {
	reg.Register("computer_available", func(c *web.Context) (any, error) {
		return map[string]any{
			"available": false,
			"platform":  computerPlatformName(),
		}, nil
	})

	// Explicit (literal) registrations below so the route-inventory scanner and
	// a human reader both see every computer_* route; all share one refusal.
	reg.Register("computer_status", computerRefused)
	reg.Register("computer_request_permission", computerRefused)
	reg.Register("computer_open_permission_settings", computerRefused)
	reg.Register("computer_reveal_helper", computerRefused)
	reg.Register("computer_list_shareable_windows", computerRefused)
	reg.Register("computer_window_thumbnail", computerRefused)
	reg.Register("computer_share_window", computerRefused)
	reg.Register("computer_share_windows", computerRefused)
	reg.Register("computer_shared_state", computerRefused)
	reg.Register("computer_share_app", computerRefused)
	reg.Register("computer_share_screen", computerRefused)
	reg.Register("computer_revoke_all", computerRefused)
	reg.Register("computer_stop", computerRefused)
	reg.Register("computer_stop_key_status", computerRefused)
	reg.Register("computer_driver_info", computerRefused)
	reg.Register("computer_driver_install", computerRefused)
	reg.Register("computer_driver_uninstall", computerRefused)
}

// computerRefused is the shared handler for every computer-use operation the
// headless build cannot serve.
func computerRefused(c *web.Context) (any, error) {
	return nil, computerUnavailable()
}
