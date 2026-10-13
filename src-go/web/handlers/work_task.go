package handlers

// Work task board (B10): data plane (CRUD/reorder/archive/events), per-folder
// settings, templates, and the lifecycle state machine (start/retry/requeue/
// schedule/return/cancel/merge/deliver_pr/complete). Mirrors
// commands/work_task.rs + models/work_task.rs. The lifecycle transitions move
// board state and append timeline events + a work_task://changed firehose;
// wiring the actual agent run (ACP) / worktree / forge delivery onto start/
// merge/deliver is a later enhancement — the row shapes and transitions here
// are the faithful board contract clients render.

import (
	"database/sql"
	"encoding/json"
	"strconv"
	"strings"

	"yoyocode/src-go/db"
	"yoyocode/src-go/web"
)

const workTaskCols = `SELECT id, folder_id, title, config, status, failure_reason, last_error, run_seq,
	sort_order, worktree_folder_id, conversation_id, connection_id, base_branch, base_sha,
	work_branch, merge_state, pending_merge, cleanup_state, verdict, result_summary,
	files_changed, additions, deletions, merge_commit, completion_kind, preflight,
	archived_at, scheduled_at, source_kind, source_key, source_meta,
	created_at, updated_at, started_at, settled_at, finished_at FROM work_task`

func scanWorkTask(d *db.Database, row interface{ Scan(...any) error }) (map[string]any, error) {
	var (
		id, folderID, runSeq, sortOrder                       int64
		title, config, status, createdAt, updatedAt           string
		failureReason, lastError                              sql.NullString
		worktreeFolderID, conversationID                      sql.NullInt64
		connectionID, baseBranch, baseSHA, workBranch         sql.NullString
		mergeState, pendingMerge, cleanupState, verdict       sql.NullString
		resultSummary, mergeCommit, completionKind, preflight sql.NullString
		filesChanged, additions, deletions                    sql.NullInt64
		archivedAt, scheduledAt                               sql.NullString
		sourceKind, sourceKey, sourceMeta                     sql.NullString
		startedAt, settledAt, finishedAt                      sql.NullString
	)
	if err := row.Scan(&id, &folderID, &title, &config, &status, &failureReason, &lastError, &runSeq,
		&sortOrder, &worktreeFolderID, &conversationID, &connectionID, &baseBranch, &baseSHA,
		&workBranch, &mergeState, &pendingMerge, &cleanupState, &verdict, &resultSummary,
		&filesChanged, &additions, &deletions, &mergeCommit, &completionKind, &preflight,
		&archivedAt, &scheduledAt, &sourceKind, &sourceKey, &sourceMeta,
		&createdAt, &updatedAt, &startedAt, &settledAt, &finishedAt); err != nil {
		return nil, err
	}
	rec := map[string]any{
		"id": id, "folder_id": folderID, "title": title,
		"config":             jsonValueOr(config, map[string]any{}),
		"status":             status,
		"failure_reason":     nsAny(failureReason),
		"last_error":         nsAny(lastError),
		"run_seq":            runSeq,
		"sort_order":         sortOrder,
		"worktree_folder_id": niAny(worktreeFolderID),
		"worktree_missing":   false,
		"agent_type":         deriveAgentType(d, config, folderID),
		"conversation_id":    niAny(conversationID),
		"connection_id":      nsAny(connectionID),
		"base_branch":        nsAny(baseBranch),
		"base_sha":           nsAny(baseSHA),
		"work_branch":        nsAny(workBranch),
		"cleanup_state":      nsAny(cleanupState),
		"verdict":            nsAny(verdict),
		"result_summary":     nsAny(resultSummary),
		"files_changed":      niAny(filesChanged),
		"additions":          niAny(additions),
		"deletions":          niAny(deletions),
		"merge_commit":       nsAny(mergeCommit),
		"preflight":          jsonValueOrNull(preflight),
		"merge_queued":       jsonValueOrNull(pendingMerge),
		"archived_at":        nsAny(archivedAt),
		"scheduled_at":       nsAny(scheduledAt),
		"compacting":         false,
		"created_at":         createdAt,
		"updated_at":         updatedAt,
		"started_at":         nsAny(startedAt),
		"settled_at":         nsAny(settledAt),
		"finished_at":        nsAny(finishedAt),
	}
	if completionKind.Valid && completionKind.String != "" {
		rec["completion_kind"] = completionKind.String
	}
	if sourceKind.Valid && sourceKind.String != "" {
		rec["source_kind"] = sourceKind.String
	}
	if sourceKey.Valid && sourceKey.String != "" {
		rec["source_key"] = sourceKey.String
	}
	if sourceMeta.Valid && sourceMeta.String != "" {
		rec["source_meta"] = jsonValueOrNull(sourceMeta)
	}
	return rec, nil
}

func workTaskByID(d *db.Database, id int64) (map[string]any, error) {
	row := d.Conn.QueryRow(workTaskCols+` WHERE id = ? AND deleted_at IS NULL`, id)
	rec, err := scanWorkTask(d, row)
	if err == sql.ErrNoRows {
		return nil, web.ErrNotFound("work task not found")
	}
	if err != nil {
		return nil, web.ErrDatabase(err.Error())
	}
	return rec, nil
}

func deriveAgentType(d *db.Database, config string, folderID int64) any {
	var cfg map[string]any
	if json.Unmarshal([]byte(config), &cfg) == nil {
		if at, ok := cfg["agent_type"].(string); ok && at != "" {
			return at
		}
	}
	// Inherit the folder default.
	if d != nil && d.Conn != nil {
		var da sql.NullString
		_ = d.Conn.QueryRow(`SELECT default_agent_type FROM folder WHERE id = ?`, folderID).Scan(&da)
		if da.Valid && da.String != "" {
			return da.String
		}
	}
	return nil
}

// RegisterWorkTask wires the board endpoints.
func RegisterWorkTask(reg *Registry) {
	reg.Register("work_task_list", func(c *web.Context) (any, error) {
		d := stateDB(c)
		if d == nil || d.Conn == nil {
			return []map[string]any{}, nil
		}
		folderID := int64Num(c.Body["folderId"])
		if folderID == 0 {
			folderID = int64Num(c.Body["folder_id"])
		}
		q := workTaskCols + ` WHERE deleted_at IS NULL`
		var args []any
		if folderID > 0 {
			q += ` AND folder_id = ?`
			args = append(args, folderID)
		}
		q += ` ORDER BY sort_order ASC, id ASC`
		rows, err := d.Conn.Query(q, args...)
		if err != nil {
			return []map[string]any{}, nil
		}
		defer rows.Close()
		out := []map[string]any{}
		for rows.Next() {
			if rec, err := scanWorkTask(d, rows); err == nil {
				out = append(out, rec)
			}
		}
		return out, nil
	})

	reg.Register("work_task_get", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		id, e := GetIntOrErr(c, "id")
		if e != nil {
			return nil, e
		}
		return workTaskByID(d, id)
	})

	reg.Register("work_task_events", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		taskID := GetInt(c, "taskId", "task_id")
		limit := GetInt(c, "limit")
		if limit <= 0 {
			limit = 500
		}
		rows, err := d.Conn.Query(
			`SELECT id, task_id, kind, actor, payload, created_at FROM work_task_event WHERE task_id = ? ORDER BY id DESC LIMIT ?`,
			taskID, limit)
		if err != nil {
			return []map[string]any{}, nil
		}
		defer rows.Close()
		out := []map[string]any{}
		for rows.Next() {
			var id, tid int64
			var kind, actor, createdAt string
			var payload sql.NullString
			if rows.Scan(&id, &tid, &kind, &actor, &payload, &createdAt) != nil {
				continue
			}
			out = append(out, map[string]any{
				"id": id, "task_id": tid, "kind": kind, "actor": actor,
				"payload": jsonValueOrNull(payload), "created_at": createdAt,
			})
		}
		return out, nil
	})

	reg.Register("work_task_attention_count", func(c *web.Context) (any, error) {
		d := stateDB(c)
		if d == nil || d.Conn == nil {
			return 0, nil
		}
		var n int64
		_ = d.Conn.QueryRow(`SELECT COUNT(*) FROM work_task WHERE deleted_at IS NULL AND archived_at IS NULL AND status IN ('review','awaiting_input')`).Scan(&n)
		return n, nil
	})

	reg.Register("work_task_create", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		draft := draftObject(c)
		folderID := asInt64(pick(draft, "folderId", "folder_id"))
		title := asString(pick(draft, "title"))
		if folderID == 0 {
			return nil, web.ErrInvalidInput("draft.folderId required")
		}
		configRaw := "{}"
		if cfg := pick(draft, "config"); cfg != nil {
			if b, e := json.Marshal(cfg); e == nil {
				configRaw = string(b)
			}
		}
		var maxOrder sql.NullInt64
		_ = d.Conn.QueryRow(`SELECT MAX(sort_order) FROM work_task WHERE folder_id = ?`, folderID).Scan(&maxOrder)
		next := int64(0)
		if maxOrder.Valid {
			next = maxOrder.Int64 + 1
		}
		now := nowRFC3339()
		res, err := d.Conn.Exec(
			`INSERT INTO work_task (folder_id, title, config, status, sort_order, created_at, updated_at) VALUES (?,?,?,'todo',?,?,?)`,
			folderID, title, configRaw, next, now, now)
		if err != nil {
			return nil, web.ErrDatabase(err.Error())
		}
		id, _ := res.LastInsertId()
		appendWorkTaskEvent(d, id, "created", "user", nil)
		rec, e := workTaskByID(d, id)
		if e == nil {
			Emit(c, "work_task://changed", map[string]any{"task_id": id})
		}
		return rec, e
	})

	reg.Register("work_task_update", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		id, e := GetIntOrErr(c, "id")
		if e != nil {
			return nil, e
		}
		if _, err := workTaskByID(d, id); err != nil {
			return nil, err
		}
		draft := draftObject(c)
		now := nowRFC3339()
		if t := pick(draft, "title"); t != nil {
			_, _ = d.Conn.Exec(`UPDATE work_task SET title = ?, updated_at = ? WHERE id = ?`, asString(t), now, id)
		}
		if cfg := pick(draft, "config"); cfg != nil {
			if b, e := json.Marshal(cfg); e == nil {
				_, _ = d.Conn.Exec(`UPDATE work_task SET config = ?, updated_at = ? WHERE id = ?`, string(b), now, id)
			}
		}
		rec, err2 := workTaskByID(d, id)
		if err2 == nil {
			Emit(c, "work_task://changed", map[string]any{"task_id": id})
		}
		return rec, err2
	})

	reg.Register("work_task_reorder", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		ids := GetIntSlice(c, "orderedIds", "ordered_ids")
		now := nowRFC3339()
		for pos, id := range ids {
			_, _ = d.Conn.Exec(`UPDATE work_task SET sort_order = ?, updated_at = ? WHERE id = ?`, pos, now, id)
		}
		Emit(c, "work_task://changed", map[string]any{})
		return nil, nil
	})

	reg.Register("work_task_delete", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		id, e := GetIntOrErr(c, "id")
		if e != nil {
			return nil, e
		}
		// Upstream DeleteParams: delete_worktree. Record the intent (git teardown
		// subsystem not ported) so it isn't silently dropped.
		p := map[string]any{}
		if v, ok := c.Body["deleteWorktree"]; ok {
			p["delete_worktree"] = v
		}
		_, _ = d.Conn.Exec(`UPDATE work_task SET deleted_at = ? WHERE id = ?`, nowRFC3339(), id)
		appendWorkTaskEvent(d, id, "deleted", "user", eventPayload(p))
		Emit(c, "work_task://changed", map[string]any{"task_id": id})
		return nil, nil
	})

	reg.Register("work_task_archive", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		id, e := GetIntOrErr(c, "id")
		if e != nil {
			return nil, e
		}
		var archivedAt any = nil
		if GetBool(c, "archived") {
			archivedAt = nowRFC3339()
		}
		_, _ = d.Conn.Exec(`UPDATE work_task SET archived_at = ?, updated_at = ? WHERE id = ?`, archivedAt, nowRFC3339(), id)
		Emit(c, "work_task://changed", map[string]any{"task_id": id})
		return nil, nil
	})

	reg.Register("work_task_cleanup", func(c *web.Context) (any, error) {
		// Hard-remove archived, finished tasks (board tidy). Non-destructive to
		// live work: only archived + done/failed/canceled rows go.
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		_, _ = d.Conn.Exec(`UPDATE work_task SET deleted_at = ? WHERE archived_at IS NOT NULL AND status IN ('done','failed','canceled') AND deleted_at IS NULL`, nowRFC3339())
		Emit(c, "work_task://changed", map[string]any{})
		return nil, nil
	})

	// ── lifecycle transitions (board state machine) ──
	registerWorkTaskTransition(reg, "work_task_start", func(d *db.Database, id int64, _ *web.Context) {
		bumpRun(d, id)
		setWorkTaskStatus(d, id, "running", map[string]any{"started_at": nowRFC3339(), "failure_reason": nil, "last_error": nil})
		appendWorkTaskEvent(d, id, "started", "user", nil)
	}, nil)
	registerWorkTaskTransition(reg, "work_task_retry", func(d *db.Database, id int64, c *web.Context) {
		bumpRun(d, id)
		setWorkTaskStatus(d, id, "running", map[string]any{"failure_reason": nil, "last_error": nil, "finished_at": nil, "settled_at": nil})
		// Upstream RestartParams: note / blocks / allow_duplicate_source. Captured on
		// the event so the data the client sent is never silently dropped (upstream
		// feeds them to the worktree spawn / forge-resurrection guard).
		p := map[string]any{}
		if v := strVal(c.Body["note"]); v != "" {
			p["note"] = v
		}
		if v, ok := c.Body["blocks"]; ok {
			p["blocks"] = v
		}
		if GetBool(c, "allowDuplicateSource", "allow_duplicate_source") {
			p["allow_duplicate_source"] = true
		}
		appendWorkTaskEvent(d, id, "retried", "user", eventPayload(p))
	}, nil)
	registerWorkTaskTransition(reg, "work_task_requeue", func(d *db.Database, id int64, _ *web.Context) {
		setWorkTaskStatus(d, id, "queued", nil)
		appendWorkTaskEvent(d, id, "requeued", "user", nil)
	}, nil)
	registerWorkTaskTransition(reg, "work_task_return", func(d *db.Database, id int64, c *web.Context) {
		bumpRun(d, id)
		setWorkTaskStatus(d, id, "running", map[string]any{"settled_at": nil})
		// Upstream ReturnParams: feedback (required), intent?, blocks?.
		p := map[string]any{"feedback": strVal(c.Body["feedback"])}
		if v, ok := c.Body["intent"].(string); ok && v != "" {
			p["intent"] = v
		}
		if v, ok := c.Body["blocks"]; ok {
			p["blocks"] = v
		}
		appendWorkTaskEvent(d, id, "returned", "user", p)
	}, nil)
	registerWorkTaskTransition(reg, "work_task_cancel", func(d *db.Database, id int64, c *web.Context) {
		setWorkTaskStatus(d, id, "canceled", map[string]any{"finished_at": nowRFC3339(), "settled_at": nowRFC3339()})
		// Upstream CancelParams: reason?, delete_worktree. The worktree teardown is a
		// git subsystem not yet ported; record the intent so it isn't dropped.
		p := map[string]any{}
		if v := strVal(c.Body["reason"]); v != "" {
			p["reason"] = v
		}
		if v, ok := c.Body["deleteWorktree"]; ok {
			p["delete_worktree"] = v
		}
		appendWorkTaskEvent(d, id, "canceled", "user", eventPayload(p))
	}, nil)
	registerWorkTaskTransition(reg, "work_task_complete", func(d *db.Database, id int64, c *web.Context) {
		setWorkTaskStatus(d, id, "done", map[string]any{"completion_kind": "accepted_without_merge", "finished_at": nowRFC3339(), "settled_at": nowRFC3339()})
		// Upstream CompleteParams: delete_worktree (git teardown deferred).
		p := map[string]any{}
		if v, ok := c.Body["deleteWorktree"]; ok {
			p["delete_worktree"] = v
		}
		appendWorkTaskEvent(d, id, "completed", "user", eventPayload(p))
	}, nil)
	registerWorkTaskTransition(reg, "work_task_merge_unqueue", func(d *db.Database, id int64, _ *web.Context) {
		_, _ = d.Conn.Exec(`UPDATE work_task SET pending_merge = NULL, updated_at = ? WHERE id = ?`, nowRFC3339(), id)
		appendWorkTaskEvent(d, id, "merge_unqueued", "user", nil)
	}, nil)

	reg.Register("work_task_schedule", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		id, e := GetIntOrErr(c, "id")
		if e != nil {
			return nil, e
		}
		var sched any = nil
		if v := GetString(c, "scheduledAt", "scheduled_at"); v != "" {
			sched = v
		}
		_, _ = d.Conn.Exec(`UPDATE work_task SET scheduled_at = ?, updated_at = ? WHERE id = ?`, sched, nowRFC3339(), id)
		appendWorkTaskEvent(d, id, "scheduled", "user", nil)
		Emit(c, "work_task://changed", map[string]any{"task_id": id})
		return nil, nil
	})

	reg.Register("work_task_merge", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		id, e := GetIntOrErr(c, "id")
		if e != nil {
			return nil, e
		}
		// Upstream MergeParams: message?, delete_worktree, instructions?.
		p := map[string]any{}
		if v := strVal(c.Body["message"]); v != "" {
			p["message"] = v
		}
		if v, ok := c.Body["deleteWorktree"]; ok {
			p["delete_worktree"] = v
		}
		if v := strVal(c.Body["instructions"]); v != "" {
			p["instructions"] = v
		}
		setWorkTaskStatus(d, id, "done", map[string]any{"completion_kind": "merged", "finished_at": nowRFC3339(), "settled_at": nowRFC3339()})
		appendWorkTaskEvent(d, id, "merged", "user", eventPayload(p))
		Emit(c, "work_task://changed", map[string]any{"task_id": id})
		return true, nil
	})

	reg.Register("work_task_deliver_pr", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		id, e := GetIntOrErr(c, "id")
		if e != nil {
			return nil, e
		}
		// Upstream DeliverPrParams: pr_title?, draft, delete_worktree?.
		p := map[string]any{}
		if v := strVal(c.Body["prTitle"], c.Body["pr_title"]); v != "" {
			p["pr_title"] = v
		}
		if GetBool(c, "draft") {
			p["draft"] = true
		}
		if v, ok := c.Body["deleteWorktree"]; ok {
			p["delete_worktree"] = v
		}
		setWorkTaskStatus(d, id, "done", map[string]any{"completion_kind": "delivered_pr", "finished_at": nowRFC3339(), "settled_at": nowRFC3339()})
		appendWorkTaskEvent(d, id, "delivered_pr", "user", eventPayload(p))
		Emit(c, "work_task://changed", map[string]any{"task_id": id})
		return "", nil
	})

	reg.Register("work_task_start_all", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		folderID := GetInt(c, "folderId", "folder_id")
		q := `SELECT id FROM work_task WHERE deleted_at IS NULL AND status = 'todo'`
		var args []any
		if folderID > 0 {
			q += ` AND folder_id = ?`
			args = append(args, folderID)
		}
		rows, err := d.Conn.Query(q, args...)
		if err != nil {
			return 0, nil
		}
		ids := []int64{}
		for rows.Next() {
			var id int64
			if rows.Scan(&id) == nil {
				ids = append(ids, id)
			}
		}
		rows.Close()
		now := nowRFC3339()
		for _, id := range ids {
			bumpRun(d, id)
			setWorkTaskStatus(d, id, "running", map[string]any{"started_at": now})
			appendWorkTaskEvent(d, id, "started", "user", nil)
		}
		Emit(c, "work_task://changed", map[string]any{})
		return len(ids), nil
	})

	reg.Register("work_task_diff", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		id, e := GetIntOrErr(c, "id")
		if e != nil {
			return nil, e
		}
		return workTaskDiff(d, id, GetString(c, "file")), nil
	})
	reg.Register("work_task_changed_files", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		id, e := GetIntOrErr(c, "id")
		if e != nil {
			return nil, e
		}
		return workTaskChangedFiles(d, id), nil
	})

	registerWorkTaskSettings(reg)
	registerWorkTaskTemplates(reg)
}

// registerWorkTaskTransition wires a {id}→status handler returning null. The
// transition closure also receives the request context so it can read the
// optional params the client may send (note / reason / blocks / delete_worktree…).
func registerWorkTaskTransition(reg *Registry, cmd string, fn func(d *db.Database, id int64, c *web.Context), _ any) {
	reg.Register(cmd, func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		id, e := GetIntOrErr(c, "id")
		if e != nil {
			return nil, e
		}
		if _, err := workTaskByID(d, id); err != nil {
			return nil, err
		}
		fn(d, id, c)
		Emit(c, "work_task://changed", map[string]any{"task_id": id})
		return nil, nil
	})
}

// eventPayload returns nil when the map is empty, so appendWorkTaskEvent stores a
// NULL payload — matching upstream, which omits the payload for param-less calls.
func eventPayload(m map[string]any) any {
	if len(m) == 0 {
		return nil
	}
	return m
}

func setWorkTaskStatus(d *db.Database, id int64, status string, extra map[string]any) {
	now := nowRFC3339()
	_, _ = d.Conn.Exec(`UPDATE work_task SET status = ?, updated_at = ? WHERE id = ?`, status, now, id)
	for col, val := range extra {
		_, _ = d.Conn.Exec(`UPDATE work_task SET `+col+` = ? WHERE id = ?`, val, id)
	}
}

func bumpRun(d *db.Database, id int64) {
	_, _ = d.Conn.Exec(`UPDATE work_task SET run_seq = run_seq + 1 WHERE id = ?`, id)
}

func appendWorkTaskEvent(d *db.Database, taskID int64, kind, actor string, payload any) {
	var p any = nil
	if payload != nil {
		if b, err := json.Marshal(payload); err == nil {
			p = string(b)
		}
	}
	_, _ = d.Conn.Exec(
		`INSERT INTO work_task_event (task_id, kind, actor, payload, created_at) VALUES (?,?,?,?,?)`,
		taskID, kind, actor, p, nowRFC3339())
}

func draftObject(c *web.Context) map[string]any {
	if raw, ok := c.Body["draft"].(map[string]any); ok {
		return raw
	}
	return map[string]any{}
}

// ── diff / changed files (git over the task's worktree vs base) ──

func workTaskDiff(d *db.Database, id int64, file string) string {
	path, baseSHA, workBranch := workTaskGitCoords(d, id)
	if path == "" || (baseSHA == "" && workBranch == "") {
		return ""
	}
	rangeArg := diffRange(baseSHA, workBranch)
	args := []string{"diff", "--no-color", rangeArg}
	if file != "" {
		args = append(args, "--", toGitLiteralPathspec(file))
	}
	out, _, _ := gitRun(path, args...)
	return out
}

func workTaskChangedFiles(d *db.Database, id int64) []map[string]any {
	out := []map[string]any{}
	path, baseSHA, workBranch := workTaskGitCoords(d, id)
	if path == "" || (baseSHA == "" && workBranch == "") {
		return out
	}
	stdout, _, ok := gitRun(path, "diff", "--numstat", diffRange(baseSHA, workBranch))
	if !ok {
		return out
	}
	for _, line := range strings.Split(stdout, "\n") {
		parts := strings.SplitN(strings.TrimRight(line, "\r"), "\t", 3)
		if len(parts) < 3 {
			continue
		}
		add, _ := strconv.ParseInt(parts[0], 10, 64)
		del, _ := strconv.ParseInt(parts[1], 10, 64)
		out = append(out, map[string]any{"file": unquoteGitPath(parts[2]), "additions": add, "deletions": del})
	}
	return out
}

func diffRange(baseSHA, workBranch string) string {
	base := baseSHA
	if base == "" {
		base = "HEAD"
	}
	target := workBranch
	if target == "" {
		target = "HEAD"
	}
	return base + "..." + target
}

// workTaskGitCoords resolves the worktree path (folder path) + base/work refs.
func workTaskGitCoords(d *db.Database, id int64) (path, baseSHA, workBranch string) {
	var folderID sql.NullInt64
	var wtFolderID sql.NullInt64
	var base, work sql.NullString
	_ = d.Conn.QueryRow(`SELECT folder_id, worktree_folder_id, base_sha, work_branch FROM work_task WHERE id = ?`, id).
		Scan(&folderID, &wtFolderID, &base, &work)
	target := folderID.Int64
	if wtFolderID.Valid {
		target = wtFolderID.Int64
	}
	path = folderPath(d, target)
	return path, base.String, work.String
}

// ── settings ──

func defaultWorkTaskSettings() map[string]any {
	return map[string]any{
		"default_agent_type":      nil,
		"mode_id":                 nil,
		"config_values":           map[string]any{},
		"label_snapshot":          nil,
		"auto_process":            false,
		"max_concurrent":          3,
		"merge_strategy":          "squash",
		"auto_merge":              false,
		"delete_worktree_default": false,
		"init_command":            nil,
		"stage_prompts":           map[string]any{},
	}
}

func loadWorkTaskSettingsOwn(d *db.Database, folderID int64) map[string]any {
	if d == nil || d.Conn == nil {
		return nil
	}
	var config string
	err := d.Conn.QueryRow(`SELECT config FROM work_task_settings WHERE folder_id = ?`, folderID).Scan(&config)
	if err != nil {
		return nil
	}
	var m map[string]any
	if json.Unmarshal([]byte(config), &m) != nil {
		return nil
	}
	return m
}

func effectiveWorkTaskSettings(d *db.Database, folderID int64) map[string]any {
	out := defaultWorkTaskSettings()
	own := loadWorkTaskSettingsOwn(d, folderID)
	for k, v := range own {
		out[k] = v
	}
	return out
}

func registerWorkTaskSettings(reg *Registry) {
	reg.Register("work_task_settings_get", func(c *web.Context) (any, error) {
		return effectiveWorkTaskSettings(stateDB(c), GetInt(c, "folderId", "folder_id")), nil
	})
	reg.Register("work_task_settings_effective", func(c *web.Context) (any, error) {
		return effectiveWorkTaskSettings(stateDB(c), GetInt(c, "folderId", "folder_id")), nil
	})
	reg.Register("work_task_settings_get_own", func(c *web.Context) (any, error) {
		own := loadWorkTaskSettingsOwn(stateDB(c), GetInt(c, "folderId", "folder_id"))
		if own == nil {
			return nil, nil
		}
		return own, nil
	})
	reg.Register("work_task_settings_set", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		folderID := GetInt(c, "folderId", "folder_id")
		if folderID == 0 {
			return nil, web.ErrInvalidInput("folderId required")
		}
		settings, _ := c.Body["settings"].(map[string]any)
		raw, _ := json.Marshal(settings)
		now := nowRFC3339()
		_, err = d.Conn.Exec(
			`INSERT INTO work_task_settings (folder_id, config, created_at, updated_at) VALUES (?,?,?,?)
			 ON CONFLICT(folder_id) DO UPDATE SET config=excluded.config, updated_at=excluded.updated_at`,
			folderID, string(raw), now, now)
		if err != nil {
			return nil, web.ErrDatabase(err.Error())
		}
		Emit(c, "work_task_settings://changed", map[string]any{"folder_id": folderID})
		return nil, nil
	})
	reg.Register("work_task_settings_delete", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		folderID := GetInt(c, "folderId", "folder_id")
		_, _ = d.Conn.Exec(`DELETE FROM work_task_settings WHERE folder_id = ?`, folderID)
		Emit(c, "work_task_settings://changed", map[string]any{"folder_id": folderID})
		return nil, nil
	})
}

// ── templates ──

func registerWorkTaskTemplates(reg *Registry) {
	reg.Register("work_task_template_list", func(c *web.Context) (any, error) {
		d := stateDB(c)
		out := []map[string]any{}
		if d == nil || d.Conn == nil {
			return out, nil
		}
		rows, err := d.Conn.Query(`SELECT id, name, title, config, created_at, updated_at FROM work_task_template ORDER BY name ASC`)
		if err != nil {
			return out, nil
		}
		defer rows.Close()
		for rows.Next() {
			if rec := scanWorkTaskTemplate(rows); rec != nil {
				out = append(out, rec)
			}
		}
		return out, nil
	})
	reg.Register("work_task_template_save", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		draft := draftObject(c)
		name := asString(pick(draft, "name"))
		title := asString(pick(draft, "title"))
		if name == "" {
			return nil, web.ErrInvalidInput("draft.name required")
		}
		configRaw := "{}"
		if cfg := pick(draft, "config"); cfg != nil {
			if b, e := json.Marshal(cfg); e == nil {
				configRaw = string(b)
			}
		}
		now := nowRFC3339()
		_, err = d.Conn.Exec(
			`INSERT INTO work_task_template (name, title, config, created_at, updated_at) VALUES (?,?,?,?,?)
			 ON CONFLICT(name) DO UPDATE SET title=excluded.title, config=excluded.config, updated_at=excluded.updated_at`,
			name, title, configRaw, now, now)
		if err != nil {
			return nil, web.ErrDatabase(err.Error())
		}
		row := d.Conn.QueryRow(`SELECT id, name, title, config, created_at, updated_at FROM work_task_template WHERE name = ?`, name)
		rec := scanWorkTaskTemplate(row)
		Emit(c, "work_task_templates://changed", map[string]any{})
		return rec, nil
	})
	reg.Register("work_task_template_delete", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		if id := GetInt(c, "id"); id > 0 {
			_, _ = d.Conn.Exec(`DELETE FROM work_task_template WHERE id = ?`, id)
		} else if name := GetString(c, "name"); name != "" {
			_, _ = d.Conn.Exec(`DELETE FROM work_task_template WHERE name = ?`, name)
		}
		Emit(c, "work_task_templates://changed", map[string]any{})
		return nil, nil
	})
}

func scanWorkTaskTemplate(row interface{ Scan(...any) error }) map[string]any {
	var id int64
	var name, title, config, createdAt, updatedAt string
	if row.Scan(&id, &name, &title, &config, &createdAt, &updatedAt) != nil {
		return nil
	}
	return map[string]any{
		"id": id, "name": name, "title": title,
		"config":     jsonValueOr(config, map[string]any{}),
		"created_at": createdAt, "updated_at": updatedAt,
	}
}

// ── small helpers ──

func nsAny(v sql.NullString) any {
	if v.Valid {
		return v.String
	}
	return nil
}
func niAny(v sql.NullInt64) any {
	if v.Valid {
		return v.Int64
	}
	return nil
}
func jsonValueOr(raw string, fallback any) any {
	if raw == "" {
		return fallback
	}
	var v any
	if json.Unmarshal([]byte(raw), &v) != nil {
		return fallback
	}
	return v
}
func jsonValueOrNull(v sql.NullString) any {
	if !v.Valid || v.String == "" {
		return nil
	}
	var out any
	if json.Unmarshal([]byte(v.String), &out) != nil {
		return nil
	}
	return out
}
