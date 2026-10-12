package handlers_test

// Work task board (B10) integration coverage: CRUD + lifecycle state machine
// + per-folder settings round-trip + templates upsert, through the real Router
// and a real on-disk SQLite DB. A column/shape drift fails here.

import "testing"

func TestWorkTaskBoard(t *testing.T) {
	srv := newTestServer(t)
	proj := t.TempDir()

	// A folder is the parent scope for tasks.
	code, det := call(t, srv, "open_folder", map[string]any{"path": proj})
	if code != 200 {
		t.Fatalf("open_folder status %d: %v", code, det)
	}
	fid := det["id"]

	// create → faithful WorkTaskInfo shape.
	code, task := call(t, srv, "work_task_create", map[string]any{
		"draft": map[string]any{
			"folderId": fid,
			"title":    "First task",
			"config":   map[string]any{"agent_type": "codex"},
		},
	})
	if code != 200 {
		t.Fatalf("work_task_create status %d: %v", code, task)
	}
	for _, k := range []string{"id", "folder_id", "title", "config", "status", "sort_order",
		"run_seq", "worktree_missing", "agent_type", "created_at", "updated_at",
		"merge_queued", "preflight", "compacting"} {
		if _, ok := task[k]; !ok {
			t.Fatalf("WorkTaskInfo missing %q: %v", k, task)
		}
	}
	if task["status"] != "todo" {
		t.Fatalf("new task status = %v, want todo", task["status"])
	}
	if task["agent_type"] != "codex" {
		t.Fatalf("agent_type from config = %v", task["agent_type"])
	}
	if task["worktree_missing"] != false {
		t.Fatalf("worktree_missing = %v, want false", task["worktree_missing"])
	}
	tid := task["id"]

	// list → one task.
	_, arr := callArr(t, srv, "work_task_list", map[string]any{"folderId": fid})
	if len(arr) != 1 {
		t.Fatalf("work_task_list len %d", len(arr))
	}

	// get → same id.
	_, got := call(t, srv, "work_task_get", map[string]any{"id": tid})
	if got["title"] != "First task" {
		t.Fatalf("work_task_get title = %v", got["title"])
	}

	// update title + config.
	_, upd := call(t, srv, "work_task_update", map[string]any{
		"id":    tid,
		"draft": map[string]any{"title": "Renamed"},
	})
	if upd["title"] != "Renamed" {
		t.Fatalf("work_task_update title = %v", upd["title"])
	}

	// events: creation logged.
	_, evs := callArr(t, srv, "work_task_events", map[string]any{"taskId": tid})
	if len(evs) == 0 {
		t.Fatalf("expected creation event, got none")
	}

	// lifecycle: start → running.
	if code, _ := call(t, srv, "work_task_start", map[string]any{"id": tid}); code != 200 {
		t.Fatalf("work_task_start status %d", code)
	}
	_, got = call(t, srv, "work_task_get", map[string]any{"id": tid})
	if got["status"] != "running" {
		t.Fatalf("after start status = %v, want running", got["status"])
	}
	if got["started_at"] == nil {
		t.Fatalf("after start started_at is nil")
	}
	if rs, ok := got["run_seq"].(float64); !ok || rs < 1 {
		t.Fatalf("after start run_seq = %v, want >=1", got["run_seq"])
	}

	// lifecycle: cancel → canceled.
	if code, _ := call(t, srv, "work_task_cancel", map[string]any{"id": tid}); code != 200 {
		t.Fatalf("work_task_cancel status %d", code)
	}
	_, got = call(t, srv, "work_task_get", map[string]any{"id": tid})
	if got["status"] != "canceled" {
		t.Fatalf("after cancel status = %v", got["status"])
	}

	// lifecycle: merge → done + completion_kind merged, returns true.
	code, mergeRaw := rawPostJSON(t, srv, "work_task_merge", map[string]any{"id": tid})
	if code != 200 {
		t.Fatalf("work_task_merge status %d: %s", code, mergeRaw)
	}
	_, got = call(t, srv, "work_task_get", map[string]any{"id": tid})
	if got["status"] != "done" || got["completion_kind"] != "merged" {
		t.Fatalf("after merge status=%v completion_kind=%v", got["status"], got["completion_kind"])
	}

	// attention_count: review/awaiting_input only — our done task is not counted.
	code, acRaw := rawPostJSON(t, srv, "work_task_attention_count", map[string]any{})
	if code != 200 {
		t.Fatalf("work_task_attention_count status %d", code)
	}
	if acRaw != "0" && acRaw != "0\n" {
		t.Fatalf("attention_count = %q, want 0", acRaw)
	}

	// settings: defaults → set → get_own → effective merge → delete.
	_, defSettings := call(t, srv, "work_task_settings_get", map[string]any{"folderId": fid})
	if defSettings["merge_strategy"] != "squash" {
		t.Fatalf("default merge_strategy = %v", defSettings["merge_strategy"])
	}
	if defSettings["auto_merge"] != false {
		t.Fatalf("default auto_merge = %v", defSettings["auto_merge"])
	}

	// get_own before any set → null.
	code, ownRaw := rawPostJSON(t, srv, "work_task_settings_get_own", map[string]any{"folderId": fid})
	if code != 200 || (ownRaw != "null" && ownRaw != "null\n") {
		t.Fatalf("get_own before set = %d %q", code, ownRaw)
	}

	if code, _ := call(t, srv, "work_task_settings_set", map[string]any{
		"folderId": fid,
		"settings": map[string]any{"merge_strategy": "rebase", "max_concurrent": 5},
	}); code != 200 {
		t.Fatalf("work_task_settings_set status %d", code)
	}
	_, own := call(t, srv, "work_task_settings_get_own", map[string]any{"folderId": fid})
	if own["merge_strategy"] != "rebase" {
		t.Fatalf("own merge_strategy = %v", own["merge_strategy"])
	}
	_, eff := call(t, srv, "work_task_settings_effective", map[string]any{"folderId": fid})
	if eff["merge_strategy"] != "rebase" {
		t.Fatalf("effective merge_strategy = %v (own override lost)", eff["merge_strategy"])
	}
	if eff["auto_merge"] != false {
		t.Fatalf("effective auto_merge = %v (default lost)", eff["auto_merge"])
	}

	if code, _ := call(t, srv, "work_task_settings_delete", map[string]any{"folderId": fid}); code != 200 {
		t.Fatalf("work_task_settings_delete status %d", code)
	}
	code, ownRaw = rawPostJSON(t, srv, "work_task_settings_get_own", map[string]any{"folderId": fid})
	if ownRaw != "null" && ownRaw != "null\n" {
		t.Fatalf("get_own after delete = %q", ownRaw)
	}

	// templates: save (insert) → list → save (upsert same name) → delete.
	_, tpl := call(t, srv, "work_task_template_save", map[string]any{
		"draft": map[string]any{"name": "nightly", "title": "Nightly", "config": map[string]any{"agent_type": "codex"}},
	})
	if tpl["name"] != "nightly" || tpl["title"] != "Nightly" {
		t.Fatalf("template_save = %v", tpl)
	}
	tplID := tpl["id"]
	_, tpls := callArr(t, srv, "work_task_template_list", map[string]any{})
	if len(tpls) != 1 {
		t.Fatalf("template_list len %d", len(tpls))
	}
	// upsert: same name, new title — must not create a second row.
	_, tpl2 := call(t, srv, "work_task_template_save", map[string]any{
		"draft": map[string]any{"name": "nightly", "title": "Nightly v2"},
	})
	if tpl2["id"] != tplID {
		t.Fatalf("template upsert created new row: %v vs %v", tpl2["id"], tplID)
	}
	if tpl2["title"] != "Nightly v2" {
		t.Fatalf("template upsert title = %v", tpl2["title"])
	}
	_, tpls = callArr(t, srv, "work_task_template_list", map[string]any{})
	if len(tpls) != 1 {
		t.Fatalf("template_list after upsert len %d", len(tpls))
	}
	if code, _ := call(t, srv, "work_task_template_delete", map[string]any{"name": "nightly"}); code != 200 {
		t.Fatalf("template_delete status %d", code)
	}
	_, tpls = callArr(t, srv, "work_task_template_list", map[string]any{})
	if len(tpls) != 0 {
		t.Fatalf("template_list after delete len %d", len(tpls))
	}

	// delete the task (soft) → list empty.
	if code, _ := call(t, srv, "work_task_delete", map[string]any{"id": tid}); code != 200 {
		t.Fatalf("work_task_delete status %d", code)
	}
	_, arr = callArr(t, srv, "work_task_list", map[string]any{"folderId": fid})
	if len(arr) != 0 {
		t.Fatalf("work_task_list after delete len %d", len(arr))
	}
}
