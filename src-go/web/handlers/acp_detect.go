package handlers

// acp_detect_agent_local_version (upstream commands/acp.rs
// acp_detect_agent_local_version_core): probe the locally installed version of
// an agent and persist it to agent_setting.installed_version so the composer's
// "installed?" badge heals. For npx agents this queries the global npm store
// ("npm ls -g <pkg> --json"); binary/uvx agents are not downloaded by this
// build, so they fall back to the stored value. Original Go reimplementation.

import (
	"encoding/json"
	"os/exec"
	"runtime"
	"strings"

	"yoyocode/src-go/acp"
	"yoyocode/src-go/web"
)

// packageNameFromSpec strips a trailing @version from an npm package spec,
// preserving a leading @scope: "@scope/pkg@1.2.3" -> "@scope/pkg",
// "pkg@1.2.3" -> "pkg".
func packageNameFromSpec(spec string) string {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return ""
	}
	if strings.HasPrefix(spec, "@") {
		// @scope/name(@version): the version @ is after the slash.
		if slash := strings.Index(spec, "/"); slash >= 0 {
			rest := spec[slash+1:]
			if at := strings.Index(rest, "@"); at >= 0 {
				return spec[:slash+1+at]
			}
		}
		return spec
	}
	if at := strings.Index(spec, "@"); at >= 0 {
		return spec[:at]
	}
	return spec
}

// npmGlobalVersion returns the installed version of a global npm package, or ""
// when npm is unavailable or the package is not installed globally.
func npmGlobalVersion(pkgName string) string {
	if pkgName == "" {
		return ""
	}
	npm := "npm"
	if runtime.GOOS == "windows" {
		npm = "npm.cmd"
	}
	out, err := exec.Command(npm, "ls", "-g", pkgName, "--json", "--depth=0").Output()
	if err != nil && len(out) == 0 {
		return ""
	}
	var doc struct {
		Dependencies map[string]struct {
			Version string `json:"version"`
		} `json:"dependencies"`
	}
	if json.Unmarshal(out, &doc) != nil {
		return ""
	}
	if dep, ok := doc.Dependencies[pkgName]; ok {
		return dep.Version
	}
	return ""
}

// RegisterACPDetect wires acp_detect_agent_local_version.
func RegisterACPDetect(reg *Registry) {
	reg.Register("acp_detect_agent_local_version", func(c *web.Context) (any, error) {
		agentType := strVal(c.Body["agentType"], c.Body["agent_type"])
		if agentType == "" {
			return nil, web.ErrInvalidInput("agentType required")
		}
		d := stateDB(c)
		_, prev := agentSettingStatus(d, agentType)

		spec, ok := acp.LaunchSpecFor(agentType)
		var detected string
		if ok && spec.Kind == "npx" {
			detected = npmGlobalVersion(packageNameFromSpec(spec.Package))
		}
		if detected != "" {
			if d != nil {
				upsertAgentSetting(d, agentType, map[string]any{"installed_version": detected})
			}
			if prevStr, _ := prev.(string); prevStr != detected {
				Emit(c, "acp://agents-updated", map[string]any{"reason": "local_version_detected", "agent_type": agentType})
			}
			return detected, nil
		}
		// Not detected: keep the previously-stored value (best-effort), which is
		// null when nothing was ever recorded.
		return prev, nil
	})
}
