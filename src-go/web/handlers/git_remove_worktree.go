package handlers

// git_remove_worktree (upstream commands/folders.rs: git_remove_worktree_core).
// Removes the worktree checkout hosting a branch, and — when deleteBranch is
// set — reparents that worktree folder's conversations onto the project folder,
// closes its tabs, soft-deletes the folder row, detaches any work_task pointing
// at it, and deletes the branch. Tightly coupled to the folder + work_task
// tables; mirrors upstream's refusal rules (main tree / in-use tree / a to-do
// task still working inside it).

import (
	"strings"

	"yoyocode/src-go/db"
	"yoyocode/src-go/web"
)

// RegisterGitRemoveWorktree wires git_remove_worktree.
func RegisterGitRemoveWorktree(reg *Registry) {
	reg.Register("git_remove_worktree", func(c *web.Context) (any, error) {
		path, err := pathParam(c)
		if err != nil {
			return nil, err
		}
		branchName := strings.TrimSpace(GetString(c, "branchName", "branch_name"))
		if branchName == "" {
			return nil, web.ErrInvalidInput("branchName required")
		}
		sourceFolderID := int64(GetInt(c, "sourceFolderId", "source_folder_id"))
		deleteBranch := GetBool(c, "deleteBranch", "delete_branch")
		force := GetBool(c, "force")
		return gitRemoveWorktree(c, path, branchName, sourceFolderID, deleteBranch, force)
	})
}

func gitRemoveWorktree(c *web.Context, repoPath, branchName string, sourceFolderID int64, deleteBranch, force bool) (any, error) {
	if e := ensureGitRepo(repoPath); e != nil {
		return nil, e
	}

	listed, stderr, ok := gitRun(repoPath, "worktree", "list", "--porcelain")
	if !ok {
		return nil, gitCommandError("worktree list", stderr)
	}
	entries := parseWorktrees(listed)
	// `git worktree list` always reports the main working tree first.
	mainPath := ""
	if len(entries) > 0 {
		mainPath = entries[0].path
	}
	hosting := ""
	for _, e := range entries {
		if e.branch == branchName {
			hosting = e.path
			break
		}
	}

	removal := map[string]any{
		"worktree_path":  nil,
		"branch_deleted": false,
		"folder_id":      nil,
		"reparented":     0,
	}

	d := stateDB(c)

	if hosting != "" {
		canonical := canonicalPath(hosting)
		if mainPath == hosting {
			return nil, web.ErrInvalidInput("The main working tree cannot be removed").WithDetail(hosting)
		}
		// Removing the tree the caller is working in would pull the ground out
		// from under the open session; git's own refusal is less legible.
		if canonical == canonicalPath(repoPath) {
			return nil, web.ErrInvalidInput("The worktree currently in use cannot be removed").WithDetail(hosting)
		}

		// Resolve the folder while the directory still exists — canonicalizing
		// its path afterwards would no longer match anything.
		var wtFolderID int64
		if d != nil {
			wtFolderID = folderIDAtCanonicalPath(d, canonical)
			// A to-do task mid-run is working inside this directory: removing it
			// (and `--force` would) yanks the tree out from under a live agent.
			if wtFolderID > 0 {
				if busy := tasksBlockingWorktreeRemoval(d, wtFolderID); len(busy) > 0 {
					return nil, web.ErrInvalidInput("A to-do task is still working in this worktree — cancel or finish it first").WithDetail(strings.Join(busy, ", "))
				}
			}
		}

		args := []string{"worktree", "remove"}
		if force {
			args = append(args, "--force")
		}
		args = append(args, hosting)
		if _, rmErr, rmOk := gitRun(repoPath, args...); !rmOk {
			if fileExists(hosting) {
				return nil, gitCommandError("worktree remove", rmErr)
			}
			// The directory was deleted behind git's back — drop the stale
			// registration so it can't block the branch delete below.
			if _, prErr, prOk := gitRun(repoPath, "worktree", "prune"); !prOk {
				return nil, gitCommandError("worktree prune", prErr)
			}
		}
		removal["worktree_path"] = hosting

		// Only the "delete everything" variant drops the folder: removing just
		// the checkout frees a branch while keeping the workspace entry (and its
		// sessions) for a worktree you intend to recreate.
		if deleteBranch && d != nil && wtFolderID > 0 {
			if projectFolderID := reparentTarget(d, wtFolderID, sourceFolderID); projectFolderID > 0 {
				moved := convergeRemovedWorktreeFolder(c, d, wtFolderID, projectFolderID)
				removal["reparented"] = moved
				removal["folder_id"] = wtFolderID
			}
		}
	}

	if deleteBranch {
		flag := "-d"
		if force {
			flag = "-D"
		}
		if _, delErr, delOk := gitRun(repoPath, "branch", flag, branchName); !delOk {
			// "not found" means an earlier attempt already got it.
			if !strings.Contains(strings.ToLower(delErr), "not found") {
				return nil, gitCommandError("branch "+flag, delErr)
			}
		}
		removal["branch_deleted"] = true
	}

	return removal, nil
}

// folderIDAtCanonicalPath finds the non-deleted folder whose canonicalized path
// equals the given canonical path (mirrors folder_at_path). 0 when none.
func folderIDAtCanonicalPath(d *db.Database, canonical string) int64 {
	if d == nil || d.Conn == nil {
		return 0
	}
	rows, err := d.Conn.Query(`SELECT id, path FROM folder WHERE deleted_at IS NULL`)
	if err != nil {
		return 0
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var path string
		if rows.Scan(&id, &path) != nil {
			continue
		}
		if canonicalPath(path) == canonical {
			return id
		}
	}
	return 0
}

// worktreeBusyStatuses mirrors WORKTREE_BUSY_STATUSES.
var worktreeBusyStatuses = []string{"queued", "preparing", "running", "awaiting_input", "merging"}

// tasksBlockingWorktreeRemoval returns the titles of live tasks working inside
// the folder's worktree (mirrors tasks_blocking_worktree_removal).
func tasksBlockingWorktreeRemoval(d *db.Database, folderID int64) []string {
	if d == nil || d.Conn == nil {
		return nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(worktreeBusyStatuses)), ",")
	q := `SELECT title FROM work_task WHERE deleted_at IS NULL AND worktree_folder_id = ? AND status IN (` + placeholders + `)`
	argv := make([]any, 0, len(worktreeBusyStatuses)+1)
	argv = append(argv, folderID)
	for _, s := range worktreeBusyStatuses {
		argv = append(argv, s)
	}
	rows, err := d.Conn.Query(q, argv...)
	if err != nil {
		return nil
	}
	defer rows.Close()
	titles := []string{}
	for rows.Next() {
		var t string
		if rows.Scan(&t) == nil {
			titles = append(titles, t)
		}
	}
	return titles
}

// reparentTarget mirrors reparent_target: a removed worktree folder's
// conversations belong to its recorded parent, falling back to the caller's own
// root. 0 means nowhere safe to move them.
func reparentTarget(d *db.Database, worktreeFolderID, sourceFolderID int64) int64 {
	if d == nil || d.Conn == nil {
		return 0
	}
	recorded := folderParentID(d, worktreeFolderID)
	var target int64
	if recorded > 0 {
		target = recorded
	} else if sourceFolderID > 0 {
		if p := folderParentID(d, sourceFolderID); p > 0 {
			target = p
		} else {
			target = sourceFolderID
		}
	}
	if target == worktreeFolderID {
		return 0
	}
	return target
}

// convergeRemovedWorktreeFolder converges DB + clients once a worktree is off
// disk: conversations move to projectFolderID, tabs close, the folder row is
// soft-deleted, any task pointing at it is detached, and clients are told.
// Returns the number of conversations moved (mirrors
// converge_removed_worktree_folder; Go has no origin_cwd column to stamp).
func convergeRemovedWorktreeFolder(c *web.Context, d *db.Database, worktreeFolderID, projectFolderID int64) int {
	now := nowRFC3339()

	moved := 0
	if res, err := d.Conn.Exec(`UPDATE conversation SET folder_id = ?, updated_at = ? WHERE folder_id = ? AND deleted_at IS NULL`, projectFolderID, now, worktreeFolderID); err == nil {
		if n, e := res.RowsAffected(); e == nil {
			moved = int(n)
		}
	}

	_, _ = d.Conn.Exec(`DELETE FROM opened_tab WHERE folder_id = ?`, worktreeFolderID)
	_, _ = d.Conn.Exec(`UPDATE folder SET deleted_at = ?, is_open = 0, updated_at = ? WHERE id = ?`, now, now, worktreeFolderID)

	// Detach any task pointing at the removed folder (mirrors
	// clear_worktree_by_folder) so its card stops offering actions that can
	// only fail.
	detached := []int64{}
	if rows, err := d.Conn.Query(`SELECT id FROM work_task WHERE worktree_folder_id = ?`, worktreeFolderID); err == nil {
		for rows.Next() {
			var id int64
			if rows.Scan(&id) == nil {
				detached = append(detached, id)
			}
		}
		rows.Close()
	}
	if len(detached) > 0 {
		_, _ = d.Conn.Exec(`UPDATE work_task SET worktree_folder_id = NULL, cleanup_state = NULL, updated_at = ? WHERE worktree_folder_id = ?`, now, worktreeFolderID)
	}

	// Tell every client: conversations moved, the folder is gone, cards changed.
	Emit(c, "conversation://changed", map[string]any{"folder_ids": []any{projectFolderID}, "updated": moved})
	Emit(c, "folders://changed", map[string]any{})
	for _, id := range detached {
		Emit(c, "work_task://changed", map[string]any{"task_id": id})
	}
	return moved
}
