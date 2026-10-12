package handlers

// Office tools skill endpoints (upstream commands/office_tools.rs). OfficeCLI is
// an external document-conversion binary; this build does not bundle it, so
// officecli_detect reports not-installed and the bundled office skill catalog
// is empty. The skill-link routes reuse the shared central-store link engine
// (skills_link.go). The heavier office surface (install, sync, render_html,
// start/stop_office_watch + the office-watch HTTP proxy) is a separate pass.

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"yoyocode/src-go/web"
)

func officecliNotInstalled() map[string]any {
	return map[string]any{"installed": false, "version": nil, "path": nil, "runtime_error": nil}
}

// RegisterOfficeTools wires the officecli detect + skill endpoints.
func RegisterOfficeTools(reg *Registry) {
	reg.Register("officecli_detect", func(c *web.Context) (any, error) {
		return officecliNotInstalled(), nil
	})
	reg.Register("officecli_install", func(c *web.Context) (any, error) {
		// Not bundled in this build; nothing to install.
		return officecliNotInstalled(), nil
	})
	reg.Register("officecli_uninstall", func(c *web.Context) (any, error) {
		return officecliNotInstalled(), nil
	})

	reg.Register("officecli_render_html", officecliRenderHTML)

	reg.Register("officecli_list_skills", func(c *web.Context) (any, error) {
		return []map[string]any{}, nil
	})
	reg.Register("officecli_sync_skills", func(c *web.Context) (any, error) {
		return map[string]any{"synced": 0, "errors": []string{}}, nil
	})
	reg.Register("officecli_skill_get_install_status", func(c *web.Context) (any, error) {
		return []map[string]any{}, nil
	})
	reg.Register("officecli_skill_list_all_install_statuses", func(c *web.Context) (any, error) {
		return []map[string]any{}, nil
	})

	reg.Register("officecli_skill_read_content", func(c *web.Context) (any, error) {
		id, err := validateSkillID(GetString(c, "skillId", "skill_id"))
		if err != nil {
			return nil, err
		}
		path := filepath.Join(customCentralPath(id), "SKILL.md")
		if fi, e := os.Stat(path); e != nil || fi.IsDir() {
			return nil, web.ErrTaskExecution("office skill '" + id + "' has no SKILL.md on disk")
		}
		b, rerr := os.ReadFile(path)
		if rerr != nil {
			return nil, web.ErrIO(rerr.Error())
		}
		return string(b), nil
	})

	// Link routes reuse the shared engine. applySkillLinkOps reads expertId/
	// expert_id from each op — the office skill id travels in that same field.
	reg.Register("officecli_skill_apply_links", func(c *web.Context) (any, error) {
		return applySkillLinkOps(c), nil
	})
	reg.Register("officecli_skill_link_to_agent", func(c *web.Context) (any, error) {
		id, err := validateSkillID(GetString(c, "skillId", "skill_id"))
		if err != nil {
			return nil, err
		}
		return enableSkillLink(id, GetString(c, "agentType", "agent_type"))
	})
	reg.Register("officecli_skill_unlink_from_agent", func(c *web.Context) (any, error) {
		id, err := validateSkillID(GetString(c, "skillId", "skill_id"))
		if err != nil {
			return nil, err
		}
		if e := disableSkillLink(id, GetString(c, "agentType", "agent_type")); e != nil {
			return nil, web.ErrIO(e.Error())
		}
		return nil, nil
	})
}

// ── officecli_render_html (upstream commands/office_tools.rs) ──

func isOfficePath(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".docx", ".xlsx", ".pptx":
		return true
	}
	return false
}

// resolveOfficecli resolves the officecli binary on PATH, falling back to the
// official installers' known locations (mirrors resolve_officecli).
func resolveOfficecli() string {
	if p, err := exec.LookPath("officecli"); err == nil {
		return p
	}
	for _, p := range officecliKnownInstallPaths() {
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return p
		}
	}
	return ""
}

func officecliKnownInstallPaths() []string {
	paths := []string{}
	if runtime.GOOS == "windows" {
		if lad := strings.TrimSpace(os.Getenv("LOCALAPPDATA")); lad != "" {
			paths = append(paths, filepath.Join(lad, "OfficeCLI", "officecli.exe"))
		}
	} else {
		paths = append(paths, filepath.Join(skillHomeDir(), ".local", "bin", "officecli"))
	}
	return paths
}

// officecliRenderHTML renders an office file to self-contained HTML via
// `officecli view <file> html`, confined to the workspace root.
func officecliRenderHTML(c *web.Context) (any, error) {
	rootPath := GetString(c, "rootPath", "root_path")
	rel := GetString(c, "path")
	if err := requireFolderDir(rootPath); err != nil {
		return nil, web.ErrIO("workspace root does not exist")
	}
	target, terr := resolveTreePath(rootPath, rel)
	if terr != nil {
		return nil, terr
	}
	if e := ensureNavigable(rootPath, target); e != nil {
		return nil, web.ErrIO("path is outside workspace root")
	}
	if fi, serr := os.Stat(target); serr != nil || fi.IsDir() {
		return nil, web.ErrIO("path is not a file")
	}
	if !isOfficePath(target) {
		return nil, web.ErrIO("not a supported office file (.docx/.xlsx/.pptx)")
	}
	binary := resolveOfficecli()
	if binary == "" {
		return nil, web.ErrDependencyMissing("OfficeCLI is not installed")
	}
	out, err := exec.Command(binary, "view", target, "html").Output()
	if err != nil {
		stderr := ""
		if ee, ok := err.(*exec.ExitError); ok {
			stderr = strings.TrimSpace(string(ee.Stderr))
		}
		return nil, web.ErrExternalCommand("officecli view failed").WithDetail(firstNonEmptyStr(stderr, err.Error()))
	}
	html := string(out)
	if strings.TrimSpace(html) == "" {
		return nil, web.ErrExternalCommand("officecli produced empty output")
	}
	return html, nil
}
