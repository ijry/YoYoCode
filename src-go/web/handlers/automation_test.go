package handlers_test

// Automation CRUD + cron next-run + run lifecycle, through the real Router.

import (
	"strings"
	"testing"
)

func automationDraft(name, cronExpr string, enabled bool) map[string]any {
	return map[string]any{
		"name": name, "enabled": enabled, "trigger_kind": "schedule",
		"cron": cronExpr, "timezone": "UTC", "agent_type": "codex",
		"isolation": "worktree_per_run", "is_remote_branch": false,
		"config": map[string]any{"display_text": "do the thing"},
	}
}

func TestAutomationCRUD(t *testing.T) {
	srv := newTestServer(t)

	// empty list.
	_, arr := callArr(t, srv, "automation_list", map[string]any{})
	if len(arr) != 0 {
		t.Fatalf("list should be empty, got %d", len(arr))
	}

	// create scheduled+enabled → next_run_at set.
	code, a := call(t, srv, "automation_create", map[string]any{"draft": automationDraft("nightly", "0 9 * * *", true)})
	if code != 200 {
		t.Fatalf("create %d: %v", code, a)
	}
	for _, k := range []string{"id", "name", "enabled", "trigger_kind", "cron", "timezone",
		"next_run_at", "agent_type", "isolation", "is_remote_branch", "config", "unseen_failures",
		"created_at", "updated_at"} {
		if _, ok := a[k]; !ok {
			t.Fatalf("AutomationInfo missing %q: %v", k, a)
		}
	}
	if a["enabled"] != true {
		t.Fatalf("enabled = %v", a["enabled"])
	}
	if a["next_run_at"] == nil {
		t.Fatalf("scheduled+enabled should have next_run_at")
	}
	id := a["id"]

	// validation: missing prompt.
	bad := automationDraft("bad", "0 9 * * *", true)
	bad["config"] = map[string]any{}
	if c2, out := call(t, srv, "automation_create", map[string]any{"draft": bad}); c2 != 400 || out["code"] != "invalid_input" {
		t.Fatalf("missing prompt: %d %v", c2, out)
	}

	// get.
	_, got := call(t, srv, "automation_get", map[string]any{"id": id})
	if got["name"] != "nightly" {
		t.Fatalf("get name = %v", got["name"])
	}

	// update name.
	upd := automationDraft("renamed", "0 9 * * *", true)
	_, u := call(t, srv, "automation_update", map[string]any{"id": id, "draft": upd})
	if u["name"] != "renamed" {
		t.Fatalf("update name = %v", u["name"])
	}

	// disable → next_run_at cleared; re-enable → recomputed.
	_, off := call(t, srv, "automation_set_enabled", map[string]any{"id": id, "enabled": false})
	if off["next_run_at"] != nil {
		t.Fatalf("disabled should clear next_run_at: %v", off["next_run_at"])
	}
	_, on := call(t, srv, "automation_set_enabled", map[string]any{"id": id, "enabled": true})
	if on["next_run_at"] == nil {
		t.Fatalf("re-enable should recompute next_run_at")
	}

	// run_now → run id; runs lists it as running; cancel settles it.
	_, runRaw := rawPostJSON(t, srv, "automation_run_now", map[string]any{"automationId": id})
	if strings.TrimSpace(runRaw) == "" || strings.TrimSpace(runRaw) == "null" {
		t.Fatalf("run_now returned %q", runRaw)
	}
	_, runs := callArr(t, srv, "automation_runs", map[string]any{"automationId": id})
	if len(runs) != 1 {
		t.Fatalf("runs len %d", len(runs))
	}
	run0 := runs[0].(map[string]any)
	if run0["status"] != "running" || run0["trigger"] != "manual" {
		t.Fatalf("run row = %v", run0)
	}
	runID := run0["id"]
	if code, _ := call(t, srv, "automation_cancel_run", map[string]any{"runId": runID}); code != 200 {
		t.Fatalf("cancel_run status %d", code)
	}
	_, runs2 := callArr(t, srv, "automation_runs", map[string]any{"automationId": id})
	if runs2[0].(map[string]any)["status"] != "cancelled" {
		t.Fatalf("after cancel status = %v", runs2[0].(map[string]any)["status"])
	}

	// mark_seen zeroes unseen_failures.
	_, seen := call(t, srv, "automation_mark_seen", map[string]any{"id": id})
	if seen["unseen_failures"].(float64) != 0 {
		t.Fatalf("unseen_failures = %v", seen["unseen_failures"])
	}

	// delete (soft) → list empty.
	if code, _ := call(t, srv, "automation_delete", map[string]any{"id": id}); code != 200 {
		t.Fatalf("delete status %d", code)
	}
	_, arr = callArr(t, srv, "automation_list", map[string]any{})
	if len(arr) != 0 {
		t.Fatalf("list after delete = %d", len(arr))
	}
}

func TestAutomationComputeNextRun(t *testing.T) {
	srv := newTestServer(t)
	// A weekly Monday 09:00 cron resolves to a Monday (UTC).
	_, raw := rawPostJSON(t, srv, "automation_compute_next_run", map[string]any{"cron": "0 9 * * 1", "timezone": "UTC"})
	next := strings.Trim(strings.TrimSpace(raw), "\"")
	if next == "null" || next == "" {
		t.Fatalf("compute_next_run returned %q", raw)
	}
	// RFC3339 with a date — must parse and land on Monday.
	if !strings.Contains(next, "T09:00:00") {
		t.Fatalf("next run not at 09:00: %q", next)
	}

	// invalid cron → invalid_input.
	if code, out := call(t, srv, "automation_compute_next_run", map[string]any{"cron": "not a cron", "timezone": "UTC"}); code != 400 || out["code"] != "invalid_input" {
		t.Fatalf("invalid cron: %d %v", code, out)
	}

	// invalid timezone → invalid_input.
	if code, out := call(t, srv, "automation_compute_next_run", map[string]any{"cron": "0 9 * * *", "timezone": "Nowhere/Nope"}); code != 400 || out["code"] != "invalid_input" {
		t.Fatalf("invalid tz: %d %v", code, out)
	}
}
