package handlers

// Skill link engine (upstream commands/experts.rs link primitives, shared by
// experts + custom skills): symlink a central-store skill (~/.yoyocode/skills/<id>)
// into an agent's own global skill directory so the agent loads it. Backs
// custom_apply_links / custom_list_all_install_statuses and
// experts_link_to_agent / experts_apply_links / experts_unlink_from_agent.
//
// This port creates real symlinks (os.Symlink). On Unix that always works; on
// Windows it needs the privilege/Developer-Mode that lets a process create
// symlinks — where that is unavailable the enable op returns a per-op error
// (never a crash), and the status reads back NotLinked. Upstream's Windows
// junction + copy-mode fallback is not reproduced; copy_mode is always false.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"yoyocode/src-go/acp"
	"yoyocode/src-go/web"
)

// skillSupportedAgents is the set of agents with a known skill store (every
// built-in is skills-capable).
func skillSupportedAgents() []string {
	out := make([]string, 0, len(acp.BuiltinAgents))
	for _, a := range acp.BuiltinAgents {
		out = append(out, a.AgentType)
	}
	return out
}

func skillLinkPath(agentType, id string) (string, bool) {
	dir, err := preferredScopeSkillDir(agentType, skillScopeGlobal, "")
	if err != nil {
		return "", false
	}
	return filepath.Join(dir, id), true
}

func pathsEquivalent(a, b string) bool {
	a = filepath.Clean(a)
	b = filepath.Clean(b)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

func resolveReal(p string) (string, bool) {
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		return "", false
	}
	return r, true
}

// classifyLink mirrors experts::classify_link: NotLinked / LinkedToYoyocode /
// LinkedElsewhere / BlockedByRealDirectory / Broken.
func classifyLink(linkPath, expectedTarget string) string {
	info, err := os.Lstat(linkPath)
	if err != nil {
		return "not_linked"
	}
	if info.Mode()&os.ModeSymlink == 0 {
		return "blocked_by_real_directory"
	}
	rl, okL := resolveReal(linkPath)
	if !okL {
		return "broken"
	}
	re, okE := resolveReal(expectedTarget)
	if okE && pathsEquivalent(rl, re) {
		return "linked_to_codeg"
	}
	return "linked_elsewhere"
}

func skillInstallStatus(id, agentType string) map[string]any {
	expected := customCentralPath(id)
	linkPath, ok := skillLinkPath(agentType, id)
	if !ok {
		return map[string]any{
			"expert_id":            id,
			"agent_type":           agentType,
			"state":                "not_linked",
			"link_path":            "",
			"target_path":          nil,
			"expected_target_path": expected,
			"copy_mode":            false,
		}
	}
	state := classifyLink(linkPath, expected)
	var targetPath any
	if t, err := os.Readlink(linkPath); err == nil {
		targetPath = t
	}
	return map[string]any{
		"expert_id":            id,
		"agent_type":           agentType,
		"state":                state,
		"link_path":            linkPath,
		"target_path":          targetPath,
		"expected_target_path": expected,
		"copy_mode":            false,
	}
}

// enableSkillLink links central/<id> into the agent's global skill dir.
func enableSkillLink(id, agentType string) (map[string]any, error) {
	src := customCentralPath(id)
	if fi, e := os.Stat(filepath.Join(src, "SKILL.md")); e != nil || fi.IsDir() {
		return nil, web.ErrNotFound("skill not found in central store: " + id)
	}
	linkPath, ok := skillLinkPath(agentType, id)
	if !ok {
		return nil, web.ErrTaskExecution("agent does not support skills: " + agentType)
	}
	if err := os.MkdirAll(filepath.Dir(linkPath), 0o755); err != nil {
		return nil, web.ErrIO(err.Error())
	}
	if info, e := os.Lstat(linkPath); e == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			_ = os.Remove(linkPath) // re-link
		} else {
			return nil, web.ErrTaskExecution("a real directory already exists at " + linkPath)
		}
	}
	if err := os.Symlink(src, linkPath); err != nil {
		return nil, web.ErrIO("failed to create link: " + err.Error())
	}
	return skillInstallStatus(id, agentType), nil
}

// disableSkillLink removes the link if (and only if) it is a symlink.
func disableSkillLink(id, agentType string) error {
	linkPath, ok := skillLinkPath(agentType, id)
	if !ok {
		return nil
	}
	if info, e := os.Lstat(linkPath); e == nil && info.Mode()&os.ModeSymlink != 0 {
		return os.Remove(linkPath)
	}
	return nil
}

func applySkillLinkOps(c *web.Context) []map[string]any {
	var ops []struct {
		ExpertID  string `json:"expertId"`
		ExpertID2 string `json:"expert_id"`
		AgentType string `json:"agentType"`
		AgentT2   string `json:"agent_type"`
		Enable    bool   `json:"enable"`
	}
	if raw := GetRaw(c, "ops"); len(raw) > 0 {
		_ = json.Unmarshal(raw, &ops)
	}
	out := make([]map[string]any, 0, len(ops))
	for _, op := range ops {
		id := firstNonEmptyStr(op.ExpertID, op.ExpertID2)
		agent := firstNonEmptyStr(op.AgentType, op.AgentT2)
		res := map[string]any{"expert_id": id, "agent_type": agent, "ok": true, "status": nil, "error": nil}
		if op.Enable {
			st, err := enableSkillLink(id, agent)
			if err != nil {
				res["ok"] = false
				res["error"] = err.Error()
			} else {
				res["status"] = st
			}
		} else {
			if err := disableSkillLink(id, agent); err != nil {
				res["ok"] = false
				res["error"] = err.Error()
			}
		}
		out = append(out, res)
	}
	return out
}

// RegisterSkillLinks wires the link engine routes for custom skills + experts.
func RegisterSkillLinks(reg *Registry) {
	reg.Register("custom_apply_links", func(c *web.Context) (any, error) {
		return applySkillLinkOps(c), nil
	})
	reg.Register("experts_apply_links", func(c *web.Context) (any, error) {
		return applySkillLinkOps(c), nil
	})

	reg.Register("custom_list_all_install_statuses", func(c *web.Context) (any, error) {
		ids, err := collectCustomIDs()
		if err != nil {
			return nil, err
		}
		agents := skillSupportedAgents()
		out := []map[string]any{}
		for _, id := range ids {
			for _, agent := range agents {
				out = append(out, skillInstallStatus(id, agent))
			}
		}
		return out, nil
	})

	reg.Register("experts_link_to_agent", func(c *web.Context) (any, error) {
		id, err := validateSkillID(GetString(c, "expertId", "expert_id"))
		if err != nil {
			return nil, err
		}
		agent := GetString(c, "agentType", "agent_type")
		return enableSkillLink(id, agent)
	})

	reg.Register("experts_unlink_from_agent", func(c *web.Context) (any, error) {
		id, err := validateSkillID(GetString(c, "expertId", "expert_id"))
		if err != nil {
			return nil, err
		}
		agent := GetString(c, "agentType", "agent_type")
		if e := disableSkillLink(id, agent); e != nil {
			return nil, web.ErrIO(e.Error())
		}
		return nil, nil
	})
}
