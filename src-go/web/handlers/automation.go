package handlers

// Automations (B11): saved, schedulable composer launches. CRUD + run history
// + cron next-run preview, mirroring commands/automation.rs +
// db/service/automation_service.rs. next_run_at is computed with a standard
// 5-field cron evaluated in the automation’s IANA timezone (robfig/cron).
// Firing/execution is the engine’s job (ACP) — a later enhancement; run_now
// records a running run row and cancel_run settles it, keeping board state and
// the wire contract faithful.

import (
	"database/sql"
	"encoding/json"
	"strings"
	"time"

	cron "github.com/robfig/cron/v3"

	"yoyocode/src-go/db"
	"yoyocode/src-go/web"
)

const automationCols = `SELECT id, name, enabled, trigger_kind, cron, timezone, next_run_at,
	agent_type, root_folder_id, isolation, branch, is_remote_branch, config, last_run_at,
	last_run_status, last_run_conversation_id, unseen_failures, created_at, updated_at FROM automation`

func scanAutomation(row interface{ Scan(...any) error }) (map[string]any, error) {
	var (
		id, unseenFailures                      int64
		name, triggerKind, timezone, agentType  string
		isolation, config, createdAt, updatedAt string
		enabled, isRemoteBranch                 int64
		cronExpr, nextRunAt, branch             sql.NullString
		rootFolderID, lastRunConvID             sql.NullInt64
		lastRunAt, lastRunStatus                sql.NullString
	)
	if err := row.Scan(&id, &name, &enabled, &triggerKind, &cronExpr, &timezone, &nextRunAt,
		&agentType, &rootFolderID, &isolation, &branch, &isRemoteBranch, &config, &lastRunAt,
		&lastRunStatus, &lastRunConvID, &unseenFailures, &createdAt, &updatedAt); err != nil {
		return nil, err
	}
	return map[string]any{
		"id": id, "name": name, "enabled": enabled != 0, "trigger_kind": triggerKind,
		"cron": nsAny(cronExpr), "timezone": timezone, "next_run_at": nsAny(nextRunAt),
		"agent_type": agentType, "root_folder_id": niAny(rootFolderID), "isolation": isolation,
		"branch": nsAny(branch), "is_remote_branch": isRemoteBranch != 0,
		"config":      jsonValueOr(config, map[string]any{}),
		"last_run_at": nsAny(lastRunAt), "last_run_status": nsAny(lastRunStatus),
		"last_run_conversation_id": niAny(lastRunConvID), "unseen_failures": unseenFailures,
		"created_at": createdAt, "updated_at": updatedAt,
	}, nil
}

func automationByID(d *db.Database, id int64) (map[string]any, error) {
	row := d.Conn.QueryRow(automationCols+` WHERE id = ? AND deleted_at IS NULL`, id)
	rec, err := scanAutomation(row)
	if err == sql.ErrNoRows {
		return nil, web.ErrNotFound("automation not found")
	}
	if err != nil {
		return nil, web.ErrDatabase(err.Error())
	}
	return rec, nil
}

// ── cron evaluation ──

var cronStandardParser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)
var cronSecondsParser = cron.NewParser(cron.Second | cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)

// computeNextRun evaluates a 5-field (or 6-field seconds) cron in the given
// IANA timezone, returning the next UTC fire strictly after `after`, or "".
func computeNextRun(cronExpr, timezone string, after time.Time) (string, error) {
	expr := strings.TrimSpace(cronExpr)
	if expr == "" {
		return "", web.ErrInvalidInput("cron is required")
	}
	fields := strings.Fields(expr)
	var sched cron.Schedule
	var perr error
	switch len(fields) {
	case 5:
		sched, perr = cronStandardParser.Parse(expr)
	case 6, 7:
		sched, perr = cronSecondsParser.Parse(strings.Join(fields[:6], " "))
	default:
		return "", web.ErrInvalidInput("cron must have 5 fields (min hour dom mon dow)")
	}
	if perr != nil {
		return "", web.ErrInvalidInput("invalid cron '" + cronExpr + "': " + perr.Error())
	}
	loc, lerr := time.LoadLocation(timezone)
	if lerr != nil {
		return "", web.ErrInvalidInput("invalid timezone '" + timezone + "'")
	}
	next := sched.Next(after.In(loc))
	if next.IsZero() {
		return "", nil
	}
	return next.UTC().Format(time.RFC3339), nil
}

// nextRunFor returns the next_run_at to persist: only scheduled + enabled
// automations have one.
func nextRunFor(enabled bool, triggerKind, cronExpr, timezone string) (any, error) {
	if !enabled || triggerKind != "schedule" {
		return nil, nil
	}
	next, err := computeNextRun(cronExpr, timezone, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	if next == "" {
		return nil, nil
	}
	return next, nil
}

// ── draft ──

type automationDraft struct {
	name           string
	enabled        bool
	triggerKind    string
	cron           any
	timezone       string
	agentType      string
	rootFolderID   any
	isolation      string
	branch         any
	isRemoteBranch bool
	config         any
}

func parseAutomationDraft(raw map[string]any) automationDraft {
	if raw == nil {
		raw = map[string]any{}
	}
	d := automationDraft{
		name:           strings.TrimSpace(asString(pick(raw, "name"))),
		enabled:        asBool(pick(raw, "enabled")),
		triggerKind:    firstNonEmpty(asString(pick(raw, "trigger_kind", "triggerKind")), "schedule"),
		timezone:       firstNonEmpty(asString(pick(raw, "timezone")), "UTC"),
		agentType:      asString(pick(raw, "agent_type", "agentType")),
		isolation:      firstNonEmpty(asString(pick(raw, "isolation")), "worktree_per_run"),
		isRemoteBranch: asBool(pick(raw, "is_remote_branch", "isRemoteBranch")),
		config:         pick(raw, "config"),
	}
	if v := pick(raw, "cron"); v != nil {
		if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
			d.cron = s
		}
	}
	if v := pick(raw, "root_folder_id", "rootFolderId"); v != nil {
		if n := asInt64(v); n != 0 {
			d.rootFolderID = n
		}
	}
	if v := pick(raw, "branch"); v != nil {
		if s, ok := v.(string); ok && s != "" {
			d.branch = s
		}
	}
	if d.config == nil {
		d.config = map[string]any{}
	}
	return d
}

func firstNonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}

func validateAutomationDraft(d automationDraft) error {
	if d.name == "" {
		return web.ErrInvalidInput("name is required")
	}
	cfg, _ := d.config.(map[string]any)
	display := strings.TrimSpace(asString(pick(cfg, "display_text", "displayText")))
	blocks, _ := pick(cfg, "prompt_blocks", "promptBlocks").([]any)
	if display == "" && len(blocks) == 0 {
		return web.ErrInvalidInput("prompt is required")
	}
	action := asString(pick(cfg, "action"))
	if action == "enqueue_task" && d.rootFolderID == nil {
		return web.ErrInvalidInput("a target folder is required to enqueue tasks")
	}
	if d.isolation == "shared_in_root" && d.isRemoteBranch {
		return web.ErrInvalidInput("a remote branch requires a per-run worktree; it can't be used with shared-in-root isolation")
	}
	if d.triggerKind == "schedule" {
		if s, _ := d.cron.(string); strings.TrimSpace(s) == "" {
			return web.ErrInvalidInput("a schedule trigger requires a cron expression")
		}
	}
	return nil
}

func automationConfigJSON(cfg any) string {
	if b, err := json.Marshal(cfg); err == nil {
		return string(b)
	}
	return "{}"
}

// ── run rows ──

const automationRunCols = `SELECT id, automation_id, status, trigger, scheduled_for, started_at,
	ended_at, conversation_id, worktree_folder_id, stop_reason, error, summary, created_at FROM automation_run`

func scanAutomationRun(row interface{ Scan(...any) error }) (map[string]any, error) {
	var id, automationID int64
	var status, trigger, createdAt string
	var scheduledFor, startedAt, endedAt sql.NullString
	var conversationID, worktreeFolderID sql.NullInt64
	var stopReason, errStr, summary sql.NullString
	if err := row.Scan(&id, &automationID, &status, &trigger, &scheduledFor, &startedAt, &endedAt,
		&conversationID, &worktreeFolderID, &stopReason, &errStr, &summary, &createdAt); err != nil {
		return nil, err
	}
	return map[string]any{
		"id": id, "automation_id": automationID, "status": status, "trigger": trigger,
		"scheduled_for": nsAny(scheduledFor), "started_at": nsAny(startedAt), "ended_at": nsAny(endedAt),
		"conversation_id": niAny(conversationID), "worktree_folder_id": niAny(worktreeFolderID),
		"stop_reason": nsAny(stopReason), "error": nsAny(errStr), "summary": nsAny(summary),
		"created_at": createdAt,
	}, nil
}

func RegisterAutomation(reg *Registry) {
	reg.Register("automation_list", func(c *web.Context) (any, error) {
		d := stateDB(c)
		out := []map[string]any{}
		if d == nil || d.Conn == nil {
			return out, nil
		}
		rows, err := d.Conn.Query(automationCols + ` WHERE deleted_at IS NULL ORDER BY updated_at DESC, id DESC`)
		if err != nil {
			return out, nil
		}
		defer rows.Close()
		for rows.Next() {
			if rec, e := scanAutomation(rows); e == nil {
				out = append(out, rec)
			}
		}
		return out, nil
	})

	reg.Register("automation_get", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		id, e := GetIntOrErr(c, "id")
		if e != nil {
			return nil, e
		}
		return automationByID(d, id)
	})

	reg.Register("automation_runs", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		automationID := GetInt(c, "automationId", "automation_id")
		limit := GetInt(c, "limit")
		if limit <= 0 {
			limit = 100
		}
		rows, err := d.Conn.Query(automationRunCols+` WHERE automation_id = ? ORDER BY created_at DESC, id DESC LIMIT ?`, automationID, limit)
		out := []map[string]any{}
		if err != nil {
			return out, nil
		}
		defer rows.Close()
		for rows.Next() {
			if rec, e := scanAutomationRun(rows); e == nil {
				out = append(out, rec)
			}
		}
		return out, nil
	})

	reg.Register("automation_create", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		dr := parseAutomationDraft(draftObject(c))
		if e := validateAutomationDraft(dr); e != nil {
			return nil, e
		}
		nextRun, e := nextRunFor(dr.enabled, dr.triggerKind, asString(dr.cron), dr.timezone)
		if e != nil {
			return nil, e
		}
		now := nowRFC3339()
		res, de := d.Conn.Exec(`INSERT INTO automation (name, enabled, trigger_kind, cron, timezone, next_run_at,
			agent_type, root_folder_id, isolation, branch, is_remote_branch, config, unseen_failures, created_at, updated_at)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?,0,?,?)`,
			dr.name, boolToInt(dr.enabled), dr.triggerKind, dr.cron, dr.timezone, nextRun,
			dr.agentType, dr.rootFolderID, dr.isolation, dr.branch, boolToInt(dr.isRemoteBranch),
			automationConfigJSON(dr.config), now, now)
		if de != nil {
			return nil, web.ErrDatabase(de.Error())
		}
		id, _ := res.LastInsertId()
		Emit(c, "automation://changed", map[string]any{"id": id})
		return automationByID(d, id)
	})

	reg.Register("automation_update", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		id, e := GetIntOrErr(c, "id")
		if e != nil {
			return nil, e
		}
		if _, err := automationByID(d, id); err != nil {
			return nil, err
		}
		dr := parseAutomationDraft(draftObject(c))
		if e := validateAutomationDraft(dr); e != nil {
			return nil, e
		}
		nextRun, e := nextRunFor(dr.enabled, dr.triggerKind, asString(dr.cron), dr.timezone)
		if e != nil {
			return nil, e
		}
		_, _ = d.Conn.Exec(`UPDATE automation SET name=?, enabled=?, trigger_kind=?, cron=?, timezone=?, next_run_at=?,
			agent_type=?, root_folder_id=?, isolation=?, branch=?, is_remote_branch=?, config=?, updated_at=? WHERE id=?`,
			dr.name, boolToInt(dr.enabled), dr.triggerKind, dr.cron, dr.timezone, nextRun,
			dr.agentType, dr.rootFolderID, dr.isolation, dr.branch, boolToInt(dr.isRemoteBranch),
			automationConfigJSON(dr.config), nowRFC3339(), id)
		Emit(c, "automation://changed", map[string]any{"id": id})
		return automationByID(d, id)
	})

	reg.Register("automation_set_enabled", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		id, e := GetIntOrErr(c, "id")
		if e != nil {
			return nil, e
		}
		rec, err := automationByID(d, id)
		if err != nil {
			return nil, err
		}
		enabled := GetBool(c, "enabled")
		var nextRun any = nil
		if enabled && rec["trigger_kind"] == "schedule" {
			nr, e2 := computeNextRun(asString(rec["cron"]), asString(rec["timezone"]), time.Now().UTC())
			if e2 == nil && nr != "" {
				nextRun = nr
			}
		}
		_, _ = d.Conn.Exec(`UPDATE automation SET enabled=?, next_run_at=?, updated_at=? WHERE id=?`, boolToInt(enabled), nextRun, nowRFC3339(), id)
		Emit(c, "automation://changed", map[string]any{"id": id})
		return automationByID(d, id)
	})

	reg.Register("automation_delete", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		id, e := GetIntOrErr(c, "id")
		if e != nil {
			return nil, e
		}
		_, _ = d.Conn.Exec(`UPDATE automation SET deleted_at=?, updated_at=? WHERE id=?`, nowRFC3339(), nowRFC3339(), id)
		Emit(c, "automation://changed", map[string]any{"id": id})
		return nil, nil
	})

	reg.Register("automation_mark_seen", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		id, e := GetIntOrErr(c, "id")
		if e != nil {
			return nil, e
		}
		if _, err := automationByID(d, id); err != nil {
			return nil, err
		}
		_, _ = d.Conn.Exec(`UPDATE automation SET unseen_failures=0, updated_at=? WHERE id=?`, nowRFC3339(), id)
		return automationByID(d, id)
	})

	reg.Register("automation_compute_next_run", func(c *web.Context) (any, error) {
		next, e := computeNextRun(GetString(c, "cron"), firstNonEmpty(GetString(c, "timezone"), "UTC"), time.Now().UTC())
		if e != nil {
			return nil, e
		}
		if next == "" {
			return nil, nil
		}
		return next, nil
	})

	reg.Register("automation_run_now", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		automationID := GetInt(c, "automationId", "automation_id")
		if _, err := automationByID(d, automationID); err != nil {
			return nil, err
		}
		// Record a running manual run. Actual execution is the engine’s job (ACP),
		// a later enhancement; the run settles via automation_cancel_run until then.
		now := nowRFC3339()
		res, de := d.Conn.Exec(`INSERT INTO automation_run (automation_id, status, trigger, started_at, created_at) VALUES (?,'running','manual',?,?)`, automationID, now, now)
		if de != nil {
			return nil, web.ErrDatabase(de.Error())
		}
		runID, _ := res.LastInsertId()
		_, _ = d.Conn.Exec(`UPDATE automation SET last_run_at=?, last_run_status='running', updated_at=? WHERE id=?`, now, now, automationID)
		Emit(c, "automation://changed", map[string]any{"id": automationID})
		return runID, nil
	})

	reg.Register("automation_cancel_run", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		runID := GetInt(c, "runId", "run_id")
		now := nowRFC3339()
		_, _ = d.Conn.Exec(`UPDATE automation_run SET status='cancelled', ended_at=?, stop_reason='cancelled' WHERE id=? AND status='running'`, now, runID)
		var autoID sql.NullInt64
		_ = d.Conn.QueryRow(`SELECT automation_id FROM automation_run WHERE id=?`, runID).Scan(&autoID)
		if autoID.Valid {
			_, _ = d.Conn.Exec(`UPDATE automation SET last_run_status='cancelled', updated_at=? WHERE id=?`, now, autoID.Int64)
			Emit(c, "automation://changed", map[string]any{"id": autoID.Int64})
		}
		return nil, nil
	})
}
