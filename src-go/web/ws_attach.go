package web

import (
	"encoding/json"

	"yoyocode/src-go/acp"
)

// ─── Client→Server messages (upstream ws_attach.rs ClientMsg) ───
// Encoded with serde(tag="action", rename_all="snake_case").

// ClientMsg mirrors upstream `ClientMsg`.
type ClientMsg struct {
	Action         string  `json:"action"` // attach | detach | ping
	SubscriptionID string  `json:"subscription_id,omitempty"`
	ConnectionID   string  `json:"connection_id,omitempty"`
	SinceSeq       *uint64 `json:"since_seq,omitempty"`
}

// ─── Server→Client messages (upstream ws_attach.rs ServerMsg) ───
// Encoded with serde(tag="type", rename_all="snake_case").

// ServerMsg is the tagged union of every attach-protocol frame. Only the fields
// belonging to the active `type` are serialized — a serde tagged enum emits
// exactly its own variant's fields and nothing else.
//
// Note that `event_seq` and `high_water_seq` are NOT omitempty: upstream
// serializes them unconditionally (no skip_serializing_if), and a value of 0 is
// meaningful — it tells the client "no events yet, expect seq > 0 next".
// Dropping the key would leave the client without a cursor to resume from.
type ServerMsg struct {
	Type           string                   `json:"type"`
	SubscriptionID string                   `json:"subscription_id,omitempty"`
	ConnectionID   string                   `json:"connection_id,omitempty"`
	Snapshot       *acp.LiveSessionSnapshot `json:"snapshot,omitempty"`
	EventSeq       uint64                   `json:"event_seq,omitempty"`
	Events         []*acp.EventEnvelope     `json:"events,omitempty"`
	HighWaterSeq   uint64                   `json:"high_water_seq,omitempty"`
	Envelope       *acp.EventEnvelope       `json:"envelope,omitempty"`
	Reason         DetachReason             `json:"reason,omitempty"`
}

// MarshalJSON emits only the fields belonging to the active variant.
func (m ServerMsg) MarshalJSON() ([]byte, error) {
	switch m.Type {
	case FrameTypeSnapshot:
		return json.Marshal(struct {
			Type           string                   `json:"type"`
			SubscriptionID string                   `json:"subscription_id"`
			ConnectionID   string                   `json:"connection_id"`
			Snapshot       *acp.LiveSessionSnapshot `json:"snapshot"`
			EventSeq       uint64                   `json:"event_seq"`
		}{m.Type, m.SubscriptionID, m.ConnectionID, m.Snapshot, m.EventSeq})
	case FrameTypeReplay:
		events := m.Events
		if events == nil {
			events = []*acp.EventEnvelope{}
		}
		return json.Marshal(struct {
			Type           string               `json:"type"`
			SubscriptionID string               `json:"subscription_id"`
			ConnectionID   string               `json:"connection_id"`
			Events         []*acp.EventEnvelope `json:"events"`
			HighWaterSeq   uint64               `json:"high_water_seq"`
		}{m.Type, m.SubscriptionID, m.ConnectionID, events, m.HighWaterSeq})
	case FrameTypeEvent:
		return json.Marshal(struct {
			Type           string             `json:"type"`
			SubscriptionID string             `json:"subscription_id"`
			Envelope       *acp.EventEnvelope `json:"envelope"`
		}{m.Type, m.SubscriptionID, m.Envelope})
	case FrameTypeDetached:
		return json.Marshal(struct {
			Type           string       `json:"type"`
			SubscriptionID string       `json:"subscription_id"`
			Reason         DetachReason `json:"reason"`
		}{m.Type, m.SubscriptionID, m.Reason})
	default: // pong
		return json.Marshal(struct {
			Type string `json:"type"`
		}{m.Type})
	}
}

// DetachReason mirrors upstream DetachReason (snake_case enum values).
type DetachReason string

const (
	// DetachConnectionGone: connection_id is unknown to the manager (possibly
	// GC'd, possibly never existed). Terminal for the conversation.
	DetachConnectionGone DetachReason = "connection_gone"
	// DetachLagged: the per-connection broadcast channel dropped events because
	// this subscriber couldn't keep up. Client must re-attach with its
	// lastAppliedSeq to resync.
	DetachLagged DetachReason = "lagged"
	// DetachServerShutdown: server is shutting down; reconnect after the next
	// handshake.
	DetachServerShutdown DetachReason = "server_shutdown"
)

const (
	FrameTypeSnapshot = "snapshot"
	FrameTypeReplay   = "replay"
	FrameTypeEvent    = "event"
	FrameTypeDetached = "detached"
	FrameTypePong     = "pong"
)

// ReadyChannel MUST match `WS_READY_CHANNEL` in upstream
// src/lib/transport/constants.ts — drift silently breaks the handshake (the
// client keeps waiting and falls back to its 5s timeout warning path).
const ReadyChannel = "__ready__"

// ReplayBatchThreshold mirrors upstream REPLAY_BATCH_THRESHOLD: past this many
// events a snapshot is comparable in size and avoids forcing the client to
// apply events one-by-one.
const ReplayBatchThreshold = 32

// OutboundCapacity mirrors upstream OUTBOUND_CAPACITY: backpressure from a slow
// WS write naturally throttles per-subscription forwarders.
const OutboundCapacity = 64

// ReadyFrame is the legacy handshake frame sent immediately on connect.
// Payload is null — NOT an object — because the client gates `acp_connect` on
// receiving this exact envelope.
func ReadyFrame() WebEvent {
	return WebEvent{Channel: ReadyChannel, Payload: json.RawMessage("null")}
}
