package handlers

// Experts (bundled skill packs) read/graceful endpoints (upstream
// commands/experts.rs). The Go build ships NO bundled expert packs, so the
// catalog and install-status lists are empty and experts_read_content reads
// only the central store on disk (never an embedded bundle). The link engine
// (experts_link_to_agent / experts_apply_links / experts_unlink_from_agent),
// shared with custom_apply_links, symlinks a central skill into each agent's
// skill dir and is a separate pass.

import (
	"os"
	"path/filepath"

	"yoyocode/src-go/web"
)

// RegisterExperts wires the experts read + central-dir endpoints.
func RegisterExperts(reg *Registry) {
	// No bundled packs in this build → empty catalog / status lists.
	reg.Register("experts_list", func(c *web.Context) (any, error) {
		return []map[string]any{}, nil
	})
	reg.Register("experts_list_all_install_statuses", func(c *web.Context) (any, error) {
		return []map[string]any{}, nil
	})
	reg.Register("experts_get_install_status", func(c *web.Context) (any, error) {
		return []map[string]any{}, nil
	})

	reg.Register("experts_read_content", func(c *web.Context) (any, error) {
		id, err := validateSkillID(GetString(c, "expertId", "expert_id"))
		if err != nil {
			return nil, err
		}
		path := filepath.Join(customCentralPath(id), "SKILL.md")
		if fi, e := os.Stat(path); e != nil || fi.IsDir() {
			return nil, web.ErrTaskExecution("expert '" + id + "' has no SKILL.md on disk")
		}
		b, rerr := os.ReadFile(path)
		if rerr != nil {
			return nil, web.ErrIO(rerr.Error())
		}
		return string(b), nil
	})

	reg.Register("experts_open_central_dir", func(c *web.Context) (any, error) {
		dir := customCentralDir()
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, web.ErrIO(err.Error())
		}
		return dir, nil
	})
}
