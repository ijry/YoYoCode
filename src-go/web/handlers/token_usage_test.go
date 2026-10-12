package handlers_test

// Token-usage dashboard: seed token_usage_turn facts directly (the sync pass
// that would materialize them needs the transcript parser), then exercise the
// report aggregation, facets, and status over the real Router.

import (
	"testing"
)

func TestTokenUsageReport(t *testing.T) {
	srv, st := newTestServerState(t)
	proj := t.TempDir()
	_, det := call(t, srv, "open_folder", map[string]any{"path": proj})
	fid := int64(det["id"].(float64))
	_, conv := call(t, srv, "create_conversation", map[string]any{"folderId": fid, "agentType": "codex", "title": "Session A"})
	cid := int64(conv["id"].(float64))

	// Seed three usage facts on two days, two models.
	ins := func(occurred, model string, in, out, total int64) {
		_, err := st.DB.Conn.Exec(`INSERT INTO token_usage_turn (conversation_id, turn_key, occurred_at, model,
			input_tokens, output_tokens, cache_creation_tokens, cache_read_tokens, total_tokens, duration_ms)
			VALUES (?,?,?,?,?,?,0,0,?,0)`, cid, occurred+"-"+model, occurred, model, in, out, total)
		if err != nil {
			t.Fatalf("seed fact: %v", err)
		}
	}
	ins("2026-03-01T10:00:00Z", "sonnet", 100, 50, 150)
	ins("2026-03-01T12:00:00Z", "sonnet", 200, 80, 280)
	ins("2026-03-02T09:00:00Z", "haiku", 40, 20, 60)

	// facets.
	_, facets := call(t, srv, "token_usage_facets", map[string]any{})
	folders, _ := facets["folders"].([]any)
	if len(folders) != 1 {
		t.Fatalf("facet folders = %d, want 1", len(folders))
	}
	agents, _ := facets["agents"].([]any)
	if len(agents) != 1 || agents[0] != "codex" {
		t.Fatalf("facet agents = %v", facets["agents"])
	}
	models, _ := facets["models"].([]any)
	if len(models) != 2 {
		t.Fatalf("facet models = %v", facets["models"])
	}
	if facets["data_start"] == nil || facets["data_end"] == nil {
		t.Fatalf("facet extent missing: %v", facets)
	}

	// report (day buckets, UTC).
	_, rep := call(t, srv, "token_usage_report", map[string]any{"filter": map[string]any{"bucket": "day", "tzOffsetMinutes": 0}})
	totals, _ := rep["totals"].(map[string]any)
	if totals["total_tokens"].(float64) != 490 {
		t.Fatalf("total_tokens = %v, want 490", totals["total_tokens"])
	}
	if totals["turn_count"].(float64) != 3 {
		t.Fatalf("turn_count = %v, want 3", totals["turn_count"])
	}
	if totals["active_days"].(float64) != 2 {
		t.Fatalf("active_days = %v, want 2", totals["active_days"])
	}
	if totals["conversation_count"].(float64) != 1 {
		t.Fatalf("conversation_count = %v, want 1", totals["conversation_count"])
	}
	series, _ := rep["series"].([]any)
	if len(series) != 2 {
		t.Fatalf("series len = %d, want 2 days", len(series))
	}
	first := series[0].(map[string]any)
	if first["bucket_key"] != "2026-03-01" || first["total_tokens"].(float64) != 430 {
		t.Fatalf("series[0] = %v", first)
	}
	byModel, _ := rep["by_model"].([]any)
	if len(byModel) != 2 || byModel[0].(map[string]any)["key"] != "sonnet" {
		t.Fatalf("by_model = %v", rep["by_model"])
	}
	byFolder, _ := rep["by_folder"].([]any)
	if len(byFolder) != 1 {
		t.Fatalf("by_folder = %v", rep["by_folder"])
	}
	tops, _ := rep["top_conversations"].([]any)
	if len(tops) != 1 || tops[0].(map[string]any)["title"] != "Session A" {
		t.Fatalf("top_conversations = %v", rep["top_conversations"])
	}
	heat, _ := rep["heatmap"].([]any)
	if len(heat) == 0 {
		t.Fatalf("heatmap empty")
	}
	if rep["first_activity_at"] == nil || rep["last_activity_at"] == nil {
		t.Fatalf("activity bounds missing: %v", rep)
	}

	// status reflects the seeded facts.
	_, status := call(t, srv, "token_usage_status", map[string]any{})
	if status["fact_rows"].(float64) != 3 {
		t.Fatalf("status fact_rows = %v, want 3", status["fact_rows"])
	}
	if status["running"] != false {
		t.Fatalf("status running = %v", status["running"])
	}

	// sync is a no-op in this build.
	_, sync := call(t, srv, "token_usage_sync", map[string]any{})
	if sync["turns_written"].(float64) != 0 {
		t.Fatalf("sync turns_written = %v, want 0", sync["turns_written"])
	}
}

func TestTokenUsageEmpty(t *testing.T) {
	srv := newTestServer(t)
	_, rep := call(t, srv, "token_usage_report", map[string]any{"filter": map[string]any{"bucket": "day"}})
	totals, _ := rep["totals"].(map[string]any)
	if totals["total_tokens"].(float64) != 0 {
		t.Fatalf("empty total_tokens = %v", totals["total_tokens"])
	}
	if series, _ := rep["series"].([]any); len(series) != 0 {
		t.Fatalf("empty series len = %d", len(series))
	}
}
