package handlers

// Operator debug endpoint (B11, event_metrics module), mirroring
// web/handlers/event_metrics.rs: GET /api/debug/event_metrics returns the ACP
// event-bus metrics snapshot (EventBusMetricsSnapshot, snake_case u64 fields).
//
// Fidelity note: those counters belong to the upstream ACP internal event bus
// (ring buffer, replay, forwarder lag). The Go server's broadcaster is a plain
// fan-out that does not track them, so this reports an all-zero snapshot — the
// stable JSON shape a poller diffs between calls, honestly reflecting "no ACP
// event-bus instrumentation in this build". Registered under the command name
// "debug/event_metrics" so it answers at /api/debug/event_metrics; the router
// dispatches it like any other (token-protected) command regardless of method.

import "yoyocode/src-go/web"

func RegisterEventMetrics(reg *Registry) {
	reg.Register("debug/event_metrics", func(c *web.Context) (any, error) {
		return map[string]any{
			"emitted_count":           0,
			"lagged_count":            0,
			"ring_buffer_evict_count": 0,
			"replay_count":            0,
			"replay_event_total":      0,
			"snapshot_fallback_count": 0,
			"snapshot_cold_count":     0,
			"forwarder_lagged_count":  0,
			"worker_queue_full_count": 0,
		}, nil
	})
}
