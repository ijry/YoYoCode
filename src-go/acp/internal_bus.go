package acp

import "sync/atomic"

// EventBusMetrics mirrors upstream `acp::internal_bus::EventBusMetrics`.
// Counters are operationally significant: `SnapshotFallbackCount` and
// `ForwarderLaggedCount` are how an operator spots that REPLAY_BATCH_THRESHOLD
// or the ring-buffer caps need tuning (or that clients are stuck in a
// re-attach storm).
//
// Surfaced over HTTP by web/handlers/event_metrics.go (`/debug/event_metrics`).
type EventBusMetrics struct {
	EmitCount             atomic.Uint64
	SnapshotColdCount     atomic.Uint64
	SnapshotFallbackCount atomic.Uint64
	ReplayCount           atomic.Uint64
	ReplayEventTotal      atomic.Uint64
	ForwarderLaggedCount  atomic.Uint64
	RingBufferEvictCount  atomic.Uint64
}

func NewEventBusMetrics() *EventBusMetrics { return &EventBusMetrics{} }

// Snapshot renders the counters as the JSON object the debug endpoint returns.
// Key names are snake_case to match the upstream serde output.
func (m *EventBusMetrics) Snapshot() map[string]uint64 {
	return map[string]uint64{
		"emit_count":              m.EmitCount.Load(),
		"snapshot_cold_count":     m.SnapshotColdCount.Load(),
		"snapshot_fallback_count": m.SnapshotFallbackCount.Load(),
		"replay_count":            m.ReplayCount.Load(),
		"replay_event_total":      m.ReplayEventTotal.Load(),
		"forwarder_lagged_count":  m.ForwarderLaggedCount.Load(),
		"ring_buffer_evict_count": m.RingBufferEvictCount.Load(),
	}
}
