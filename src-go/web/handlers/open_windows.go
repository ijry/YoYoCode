package handlers

// Window-open + worktree-resolve routes (upstream web/handlers/folders.rs).
// In the server/web runtime the "open window" commands do not spawn OS windows
// — they return the navigation path the client routes to (SettingsNavigationResult).
// resolve_worktree_folder answers where a branch is checked out; open_in_code
// launches the user's editor on the host. Original Go reimplementation.

import (
	"database/sql"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"yoyocode/src-go/web"
)

func navResult(path string) map[string]any { return map[string]any{"path": path} }

var settingsSectionRoutes = map[string]string{
	"appearance":      "settings/appearance",
	"agents":          "settings/agents",
	"mcp":             "settings/mcp",
	"skills":          "settings/skills",
	"experts":         "settings/experts",
	"science":         "settings/science",
	"office-tools":    "settings/office-tools",
	"collaboration":   "settings/collaboration",
	"browser":         "settings/browser",
	"computer-use":    "settings/computer-use",
	"version-control": "settings/version-control",
	"shortcuts":       "settings/shortcuts",
	"system":          "settings/system",
}

func isAgentWire(s string) bool {
	if s == "" {
		return false
	}
	for _, ch := range s {
		if !((ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') || ch == '_') {
			return false
		}
	}
	return true
}

// RegisterOpenWindows wires the open_*_window navigation routes plus
// resolve_worktree_folder and open_in_code.
func RegisterOpenWindows(reg *Registry) {
	reg.Register("open_settings_window", func(c *web.Context) (any, error) {
		section := GetString(c, "section")
		route, ok := settingsSectionRoutes[section]
		if !ok {
			route = "settings/appearance"
		}
		path := "/" + route
		if route == "settings/agents" {
			agent := strings.TrimSpace(GetString(c, "agentType", "agent_type"))
			if isAgentWire(agent) {
				path = "/" + route + "?agent=" + agent
			}
		}
		return navResult(path), nil
	})

	reg.Register("open_commit_window", func(c *web.Context) (any, error) {
		return navResult("/commit?folderId=" + folderIDParam(c)), nil
	})

	reg.Register("open_stash_window", func(c *web.Context) (any, error) {
		return navResult("/stash?folderId=" + folderIDParam(c)), nil
	})

	reg.Register("open_merge_window", func(c *web.Context) (any, error) {
		path := "/merge?folderId=" + folderIDParam(c)
		if op := strings.TrimSpace(GetString(c, "operation")); op != "" {
			path += "&operation=" + op
		}
		if uc := strings.TrimSpace(GetString(c, "upstreamCommit", "upstream_commit")); uc != "" {
			path += "&upstreamCommit=" + uc
		}
		return navResult(path), nil
	})

	reg.Register("open_push_window", func(c *web.Context) (any, error) {
		path := "/push?folderId=" + folderIDParam(c)
		if br := strings.TrimSpace(GetString(c, "branch")); br != "" {
			path += "&branch=" + url.QueryEscape(br)
		}
		return navResult(path), nil
	})

	reg.Register("open_import_sessions_window", func(c *web.Context) (any, error) {
		path := "/import-sessions"
		if fp := strings.TrimSpace(GetString(c, "focusPath", "focus_path")); fp != "" {
			path += "?focusPath=" + url.QueryEscape(fp)
		}
		return navResult(path), nil
	})

	reg.Register("open_in_code", func(c *web.Context) (any, error) {
		path := strings.TrimSpace(GetString(c, "path"))
		if path == "" {
			return nil, web.ErrInvalidInput("path required")
		}
		if _, err := os.Stat(path); err != nil {
			return nil, web.ErrNotFound("path does not exist: " + path)
		}
		bin := "code"
		if runtime.GOOS == "windows" {
			bin = "code.cmd"
		}
		cmd := exec.Command(bin, path)
		if err := cmd.Start(); err != nil {
			return nil, web.ErrExternalCommand("failed to launch editor: " + err.Error())
		}
		// Do not wait; the editor runs independently.
		go func() { _ = cmd.Wait() }()
		return nil, nil
	})

	reg.Register("resolve_worktree_folder", func(c *web.Context) (any, error) {
		repoPath := strings.TrimSpace(GetString(c, "repoPath", "repo_path"))
		branch := strings.TrimSpace(GetString(c, "branch"))
		if repoPath == "" || branch == "" {
			return nil, web.ErrInvalidInput("repoPath and branch required")
		}
		if e := ensureGitRepo(repoPath); e != nil {
			return nil, e
		}
		stdout, stderr, ok := gitRun(repoPath, "worktree", "list", "--porcelain")
		if !ok {
			return nil, gitCommandError("worktree list", stderr)
		}
		var wtPath string
		for _, w := range parseWorktrees(stdout) {
			if w.branch == branch {
				wtPath = w.path
				break
			}
		}
		if wtPath == "" {
			return map[string]any{"path": nil, "folder_id": nil}, nil
		}
		canon := canonicalPath(wtPath)
		var folderID any = nil
		if d := stateDB(c); d != nil && d.Conn != nil {
			rows, qerr := d.Conn.Query(`SELECT id, path FROM folder WHERE deleted_at IS NULL`)
			if qerr == nil {
				defer rows.Close()
				for rows.Next() {
					var id int64
					var fpath string
					if rows.Scan(&id, &fpath) == nil && canonicalPath(fpath) == canon {
						folderID = id
						break
					}
				}
			}
		}
		return map[string]any{"path": canon, "folder_id": folderID}, nil
	})

	// open_worktree_folder registers a worktree checkout as a workspace folder,
	// parenting it under the source folder's root and seeding its alias from the
	// branch checked out there (never clobbering a user-chosen alias). Mirrors
	// commands/folders.rs open_worktree_folder_core.
	reg.Register("open_worktree_folder", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		path := strings.TrimSpace(GetString(c, "path"))
		if path == "" {
			return nil, web.ErrInvalidInput("path required")
		}
		sourceFolderID := GetInt(c, "sourceFolderId", "source_folder_id")
		var parentID any = nil
		if sourceFolderID > 0 {
			var pid sql.NullInt64
			if d.Conn.QueryRow(`SELECT parent_id FROM folder WHERE id = ?`, sourceFolderID).Scan(&pid) == nil {
				if pid.Valid {
					parentID = pid.Int64
				} else {
					parentID = int64(sourceFolderID)
				}
			}
		}
		abs, aerr := filepath.Abs(path)
		if aerr != nil {
			return nil, web.ErrInvalidInput(aerr.Error())
		}
		name := filepath.Base(abs)
		now := nowRFC3339()
		if _, e := d.Conn.Exec(
			`INSERT INTO folder (name, path, parent_id, last_opened_at, created_at, updated_at, is_open)
			 VALUES (?,?,?,?,?,?,1)
			 ON CONFLICT(path) DO UPDATE SET name=excluded.name, parent_id=excluded.parent_id, last_opened_at=excluded.last_opened_at, updated_at=excluded.updated_at, deleted_at=NULL`,
			name, abs, parentID, now, now, now); e != nil {
			return nil, web.ErrIO(e.Error())
		}
		var folderID int64
		if e := d.Conn.QueryRow(`SELECT id FROM folder WHERE path = ?`, abs).Scan(&folderID); e != nil {
			return nil, web.ErrDatabase(e.Error())
		}
		// Seed alias from the worktree's branch, best-effort, never clobbering.
		if head := resolveGitHead(abs); head != nil {
			if branch, ok := head["branch"].(string); ok && strings.TrimSpace(branch) != "" {
				_, _ = d.Conn.Exec(`UPDATE folder SET alias = ? WHERE id = ? AND (alias IS NULL OR alias = '')`, branch, folderID)
			}
		}
		Emit(c, "folders://changed", map[string]any{})
		return folderDetailByID(d, folderID)
	})
}

func folderIDParam(c *web.Context) string {
	return strconv.FormatInt(GetInt(c, "folderId", "folder_id"), 10)
}
