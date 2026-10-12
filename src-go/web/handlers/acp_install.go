package handlers

// Agent install/uninstall family. prepare_npx_agent / install_uv_tool prime the
// launcher's package cache (best-effort real tool invocation, bounded by a
// timeout) and record the installed version; download_agent_binary needs the
// binary-cache/download subsystem (not yet ported) and reports that cleanly;
// uninstall_agent clears the recorded install. A task-progress event is emitted
// on the taskId channel so the settings UI can show status.

import (
	"context"
	"os/exec"
	"strings"
	"time"

	"yoyocode/src-go/acp"
	"yoyocode/src-go/web"
)

const agentInstallTimeout = 10 * time.Minute

func RegisterACPInstall(reg *Registry) {
	reg.Register("acp_prepare_npx_agent", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		agentType := strVal(c.Body["agentType"], c.Body["agent_type"])
		taskID := strVal(c.Body["taskId"], c.Body["task_id"])
		spec, ok := acp.LaunchSpecFor(agentType)
		if !ok || spec.Kind != "npx" {
			return nil, web.ErrInvalidInput("not an npx agent: " + agentType)
		}
		if !lookPathOK("npx") {
			return nil, web.ErrDependencyMissing("npx (Node.js) is not installed")
		}
		emitAgentTask(c, taskID, agentType, "running", "")
		// Prime the npx cache by resolving the package's bin once.
		ctx, cancel := context.WithTimeout(context.Background(), agentInstallTimeout)
		defer cancel()
		out, runErr := runToolVersion(ctx, "npx", "-y", "-p", spec.Package, spec.Cmd, "--version")
		version := registryVersion(spec.Package)
		if v := parseVersion(out); v != "" {
			version = v
		}
		if runErr != nil {
			emitAgentTask(c, taskID, agentType, "error", runErr.Error())
			return nil, web.ErrExternalCommand("npx prepare failed").WithDetail(runErr.Error())
		}
		upsertAgentSetting(d, agentType, map[string]any{"installed_version": version})
		emitAgentTask(c, taskID, agentType, "done", version)
		return version, nil
	})

	reg.Register("acp_install_uv_tool", func(c *web.Context) (any, error) {
		taskID := strVal(c.Body["taskId"], c.Body["task_id"])
		if !lookPathOK("uv") && !lookPathOK("uvx") {
			return nil, web.ErrDependencyMissing("uv (Astral uv) is not installed")
		}
		emitAgentTask(c, taskID, "pi", "running", "")
		ctx, cancel := context.WithTimeout(context.Background(), agentInstallTimeout)
		defer cancel()
		spec, _ := acp.LaunchSpecFor("pi")
		_, runErr := runToolVersion(ctx, "uvx", "--from", spec.Package, spec.Cmd, "--version")
		if runErr != nil {
			emitAgentTask(c, taskID, "pi", "error", runErr.Error())
			return nil, web.ErrExternalCommand("uv tool install failed").WithDetail(runErr.Error())
		}
		emitAgentTask(c, taskID, "pi", "done", "")
		return nil, nil
	})

	reg.Register("acp_download_agent_binary", func(c *web.Context) (any, error) {
		// Binary agents (cursor/qoder/antigravity) need the per-platform
		// download+cache subsystem, which is not yet ported. Report cleanly so
		// the UI shows "install unavailable" rather than hanging.
		agentType := strVal(c.Body["agentType"], c.Body["agent_type"])
		return nil, web.ErrDependencyMissing("binary agent install not available yet: " + agentType)
	})

	reg.Register("acp_uninstall_agent", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		agentType := strVal(c.Body["agentType"], c.Body["agent_type"])
		taskID := strVal(c.Body["taskId"], c.Body["task_id"])
		if agentType == "" {
			return nil, web.ErrInvalidInput("agentType required")
		}
		if d != nil && d.Conn != nil {
			_, _ = d.Conn.Exec(`UPDATE agent_setting SET installed_version = NULL, updated_at = ? WHERE agent_type = ?`, nowRFC3339(), agentType)
		}
		emitAgentTask(c, taskID, agentType, "done", "")
		Emit(c, "agents://changed", map[string]any{})
		return nil, nil
	})
}

func runToolVersion(ctx context.Context, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func parseVersion(out string) string {
	for _, line := range strings.Split(out, "\n") {
		t := strings.TrimSpace(line)
		if t == "" {
			continue
		}
		// A bare semver-ish line is the version; otherwise skip chatter.
		if t[0] >= '0' && t[0] <= '9' {
			return t
		}
	}
	return ""
}

// registryVersion extracts the pinned version from a package@version spec.
func registryVersion(pkg string) string {
	if i := strings.LastIndex(pkg, "@"); i > 0 {
		return pkg[i+1:]
	}
	return ""
}

// emitAgentTask broadcasts an agent install/uninstall progress event on the
// taskId channel the settings UI listens on.
func emitAgentTask(c *web.Context, taskID, agentType, status, detail string) {
	if taskID == "" {
		return
	}
	Emit(c, "acp-agent-task://"+taskID, map[string]any{
		"task_id": taskID, "agent_type": agentType, "status": status, "detail": detail,
	})
}
