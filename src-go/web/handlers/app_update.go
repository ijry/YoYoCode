package handlers

// App self-update endpoints (B11), mirroring web/handlers/app_update.rs. This
// Go port is not a self-replacing binary runtime, so app_update_state reports a
// faithful Idle snapshot (with the runtime capability) and the mutating actions
// report that in-place update is unavailable in this build — the same shape the
// desktop build returns, keeping the upgrade UI from erroring on a missing route.

import (
	"os"

	"yoyocode/src-go/web"
)

func updateCapability() string {
	if os.Getenv("YOYOCODE_SUPERVISED") == "1" {
		return "supervised"
	}
	return "reexec"
}

// yoyocodeVersion tracks the upstream Cargo package version (src-tauri/Cargo.toml);
// overridable via YOYOCODE_VERSION so a build can stamp its own.
const yoyocodeVersion = "0.34.0"

func serverVersion() string {
	if v := os.Getenv("YOYOCODE_VERSION"); v != "" {
		return v
	}
	return yoyocodeVersion
}

// updateRuntimeLabel: "docker" when running in a container, else "standalone".
func updateRuntimeLabel() string {
	if os.Getenv("YOYOCODE_RUNTIME") == "docker" {
		return "docker"
	}
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return "docker"
	}
	return "standalone"
}

// appUpdateBase holds the fields shared by check_app_update and
// app_update_status. This Go build is not a self-replacing binary, so
// self_update_supported is false and rollback is unavailable; the shape still
// matches the desktop/server build so the upgrade UI renders without erroring.
func appUpdateBase() map[string]any {
	return map[string]any{
		"currentVersion":      serverVersion(),
		"selfUpdateSupported": false,
		"capability":          updateCapability(),
		"runtime":             updateRuntimeLabel(),
		"restartDelayMs":      1500,
		"rollbackAvailable":   false,
		"liveProgress":        true,
	}
}

func RegisterAppUpdate(reg *Registry) {
	// Mode-agnostic snapshot the upgrade UI re-syncs against. Idle: this build
	// has no staged update and no download in flight.
	reg.Register("app_update_state", func(c *web.Context) (any, error) {
		return map[string]any{
			"seq":        0,
			"status":     "idle",
			"capability": updateCapability(),
		}, nil
	})
	reg.Register("perform_app_update", func(c *web.Context) (any, error) {
		return nil, web.ErrInvalidInput("In-place update is not available in this build")
	})
	reg.Register("restart_app", func(c *web.Context) (any, error) {
		return nil, web.ErrInvalidInput("In-place update is not available in this build")
	})
	reg.Register("rollback_app", func(c *web.Context) (any, error) {
		return nil, web.ErrInvalidInput("In-place update is not available in this build")
	})
	// Update availability. No manifest is fetched (this build does not
	// self-update), so no newer version is ever reported — update is null.
	reg.Register("check_app_update", func(c *web.Context) (any, error) {
		res := appUpdateBase()
		res["update"] = nil
		return res, nil
	})
	// Local-only status counterpart; self_update_blocker omitted (nothing stands
	// in the way because nothing self-updates here).
	reg.Register("app_update_status", func(c *web.Context) (any, error) {
		return appUpdateBase(), nil
	})
}
