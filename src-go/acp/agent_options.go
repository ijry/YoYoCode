package acp

// Agent-option catalog types mirrored from upstream acp::types. These are the
// read-only snapshot of the modes + config options an agent advertises when it
// opens a session, surfaced by acp_describe_agent_options (and the probe that
// backs it). Field names follow upstream's serde(rename_all="camelCase") so
// the wire bytes match the contract the mobile client pins.

import "encoding/json"

// SessionModeInfo is one selectable session mode.
type SessionModeInfo struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Description *string `json:"description,omitempty"`
}

// SessionModeStateInfo is the current mode + the mode catalog the agent
// advertised during the probe.
type SessionModeStateInfo struct {
	CurrentModeID  string            `json:"currentModeId"`
	AvailableModes []SessionModeInfo `json:"availableModes"`
}

// SessionConfigSelectOptionInfo is one row in a select option list.
type SessionConfigSelectOptionInfo struct {
	Value       string  `json:"value"`
	Name        string  `json:"name"`
	Description *string `json:"description,omitempty"`
}

// SessionConfigSelectGroupInfo groups select options (e.g. by provider).
type SessionConfigSelectGroupInfo struct {
	Group   string                         `json:"group"`
	Name    string                         `json:"name"`
	Options []SessionConfigSelectOptionInfo `json:"options"`
}

// SessionConfigSelectInfo is the select variant of a config option's value.
type SessionConfigSelectInfo struct {
	CurrentValue string                         `json:"currentValue"`
	Options      []SessionConfigSelectOptionInfo `json:"options"`
	Groups       []SessionConfigSelectGroupInfo  `json:"groups"`
}

// SessionConfigBooleanInfo is the boolean variant of a config option's value.
type SessionConfigBooleanInfo struct {
	CurrentValue bool `json:"currentValue"`
}

// SessionConfigKindInfo is the tagged value of a config option
// (upstream serde tag="type" rename_all="snake_case"): "select" | "boolean".
// A Go struct cannot hold a shared `currentValue` of two different JSON types,
// so we keep the two variants behind pointers and render the inline variant
// shape on marshal / decode it on unmarshal.
type SessionConfigKindInfo struct {
	Type    string                     `json:"type"`
	Select  *SessionConfigSelectInfo   `json:"-"`
	Boolean *SessionConfigBooleanInfo  `json:"-"`
}

// MarshalJSON renders the inline tagged-union shape upstream produces:
//
//	{"type":"select","currentValue":<str>,"options":[...],"groups":[...]}
//	{"type":"boolean","currentValue":<bool>}
func (k SessionConfigKindInfo) MarshalJSON() ([]byte, error) {
	switch k.Type {
	case "boolean":
		b := k.Boolean
		if b == nil {
			b = &SessionConfigBooleanInfo{}
		}
		return json.Marshal(struct {
			Type         string `json:"type"`
			CurrentValue bool   `json:"currentValue"`
		}{Type: "boolean", CurrentValue: b.CurrentValue})
	default:
		s := k.Select
		if s == nil {
			s = &SessionConfigSelectInfo{}
		}
		return json.Marshal(struct {
			Type          string                        `json:"type"`
			CurrentValue  string                        `json:"currentValue"`
			Options       []SessionConfigSelectOptionInfo `json:"options"`
			Groups        []SessionConfigSelectGroupInfo  `json:"groups"`
		}{Type: "select", CurrentValue: s.CurrentValue, Options: s.Options, Groups: s.Groups})
	}
}

// UnmarshalJSON decodes the inline tagged-union shape. An absent/unknown type
// defaults to "select" so a future variant degrades gracefully instead of
// failing to deserialize (the same reason upstream uses plain string tags).
func (k *SessionConfigKindInfo) UnmarshalJSON(data []byte) error {
	var disc struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(data, &disc); err != nil {
		return err
	}
	switch disc.Type {
	case "boolean":
		var b SessionConfigBooleanInfo
		if err := json.Unmarshal(data, &b); err != nil {
			return err
		}
		k.Type = "boolean"
		k.Boolean = &b
	case "select", "":
		var s SessionConfigSelectInfo
		if err := json.Unmarshal(data, &s); err != nil {
			return err
		}
		k.Type = "select"
		k.Select = &s
	}
	return nil
}

// SessionConfigOptionInfo is one configurable option the agent advertises.
type SessionConfigOptionInfo struct {
	ID              string                `json:"id"`
	Name            string                `json:"name"`
	Description     *string               `json:"description,omitempty"`
	Category        *string               `json:"category,omitempty"`
	Kind            SessionConfigKindInfo `json:"kind"`
	RecommendedValue *string              `json:"recommendedValue,omitempty"`
}

// AvailableCommandInfo is one slash command the agent advertised.
type AvailableCommandInfo struct {
	Name        string  `json:"name"`
	Description string  `json:"description"`
	InputHint   *string `json:"inputHint,omitempty"`
}

// PromptCapabilitiesInfo is what the agent accepts in a prompt.
type PromptCapabilitiesInfo struct {
	Image           bool `json:"image"`
	Audio           bool `json:"audio"`
	EmbeddedContext bool `json:"embeddedContext"`
}

// SessionLastError is the agent's own error text captured during a probe
// handshake (or a live session). Mirrors the wire-relevant subset of
// AcpEvent::Error; surfaced by the probe path when the agent explained why it
// failed to start.
type SessionLastError struct {
	Message string `json:"message"`
	Code    string `json:"code,omitempty"`
	Details string `json:"details,omitempty"`
}

// AgentOptionsSnapshot is the read-only view of the modes + config options an
// agent advertises when it opens a session. Returned by acp_describe_agent_options.
// Mirrors upstream AgentOptionsSnapshot: modes / promptCapabilities are omitted
// when absent (Option<_>), configOptions / availableCommands are always arrays.
type AgentOptionsSnapshot struct {
	Modes             *SessionModeStateInfo    `json:"modes,omitempty"`
	ConfigOptions     []SessionConfigOptionInfo `json:"configOptions"`
	AvailableCommands []AvailableCommandInfo    `json:"availableCommands"`
	PromptCapabilities *PromptCapabilitiesInfo  `json:"promptCapabilities,omitempty"`
}
