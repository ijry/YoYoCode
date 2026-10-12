package handlers

// Science skill packs (upstream commands/science.rs) — a bundled skill-pack
// family parallel to Experts, sharing the same central store + link engine.
// This build ships no bundled science packs, so the catalog/status lists are
// empty and science_read_content reads only the central store on disk; the
// link routes reuse the shared skill link engine (skills_link.go).

import (
	"os"
	"path/filepath"

	"yoyocode/src-go/web"
)

// RegisterScience wires the science skill-pack endpoints.
func RegisterScience(reg *Registry) {
	reg.Register("science_list", func(c *web.Context) (any, error) {
		return []map[string]any{}, nil
	})
	reg.Register("science_list_all_install_statuses", func(c *web.Context) (any, error) {
		return []map[string]any{}, nil
	})
	reg.Register("science_get_install_status", func(c *web.Context) (any, error) {
		return []map[string]any{}, nil
	})

	reg.Register("science_read_content", func(c *web.Context) (any, error) {
		id, err := validateSkillID(GetString(c, "scienceId", "science_id", "expertId", "expert_id", "id"))
		if err != nil {
			return nil, err
		}
		path := filepath.Join(customCentralPath(id), "SKILL.md")
		if fi, e := os.Stat(path); e != nil || fi.IsDir() {
			return nil, web.ErrTaskExecution("science skill '" + id + "' has no SKILL.md on disk")
		}
		b, rerr := os.ReadFile(path)
		if rerr != nil {
			return nil, web.ErrIO(rerr.Error())
		}
		return string(b), nil
	})

	reg.Register("science_open_central_dir", func(c *web.Context) (any, error) {
		dir := customCentralDir()
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, web.ErrIO(err.Error())
		}
		return dir, nil
	})

	// Link routes reuse the shared engine (central store ↔ agent skill dir).
	reg.Register("science_apply_links", func(c *web.Context) (any, error) {
		return applySkillLinkOps(c), nil
	})
	reg.Register("science_link_to_agent", func(c *web.Context) (any, error) {
		id, err := validateSkillID(GetString(c, "scienceId", "science_id", "expertId", "expert_id", "id"))
		if err != nil {
			return nil, err
		}
		return enableSkillLink(id, GetString(c, "agentType", "agent_type"))
	})
	reg.Register("science_unlink_from_agent", func(c *web.Context) (any, error) {
		id, err := validateSkillID(GetString(c, "scienceId", "science_id", "expertId", "expert_id", "id"))
		if err != nil {
			return nil, err
		}
		if e := disableSkillLink(id, GetString(c, "agentType", "agent_type")); e != nil {
			return nil, web.ErrIO(e.Error())
		}
		return nil, nil
	})
}
