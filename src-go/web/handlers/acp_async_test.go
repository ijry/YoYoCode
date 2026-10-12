package handlers_test

import "testing"

func TestAcpStopAsyncTaskAndPlanApproval(t *testing.T) {
	srv := newTestServer(t)
	// stop_async_task → false (no async tasks tracked), 200.
	code, body := rawPostJSON(t, srv, "acp_stop_async_task", map[string]any{"connectionId": "x", "taskId": "t"})
	if code != 200 || trimWS(body) != "false" {
		t.Fatalf("stop_async_task should be false, got %d %q", code, body)
	}
	// answer_plan_approval → null no-op, 200.
	code2, _ := call(t, srv, "acp_answer_plan_approval", map[string]any{
		"connectionId": "x", "approvalId": "a", "answer": map[string]any{"decision": "approve"},
	})
	if code2 != 200 {
		t.Fatalf("answer_plan_approval should be 200, got %d", code2)
	}
}
