package acp

// Probe subsystem: a transient, throwaway agent connection used to discover the
// modes + config options an agent advertises without a live user session.
// Mirrors upstream ConnectionManager::probe_agent_options /
// wait_for_session_options.

import (
	"context"
	"os"
	"sync"
	"time"
)

// ProbeTimedOutError is returned when the agent never signals selectors_ready
// within probeTimeout. Distinguishing that from a clean "ready with no options"
// snapshot lets the UI tell the user "the agent never published its options —
// retry" instead of silently claiming the agent has nothing to configure.
type ProbeTimedOutError struct{ AgentType string }

func (e *ProbeTimedOutError) Error() string {
	return "agent " + e.AgentType + " did not publish its session options within the timeout"
}

// ProbeAgentError wraps the agent's own error message captured during a probe
// handshake. Preferred over the generic ProbeTimedOut when the agent explained
// why it failed to start (e.g. died on Initialize).
type ProbeAgentError struct{ Message string }

func (e *ProbeAgentError) Error() string { return e.Message }

// probeTimeout bounds wait_for_session_options. Generous on purpose: some
// agents (Gemini in particular) take 8-10s just to answer Initialize before
// session/new can start; a tight cap would consistently return an empty
// snapshot and make the settings UI claim those agents have nothing to
// configure. Matches the per-step Initialize timeout in connection.go. It is a
// var (not const) so tests can shrink it without waiting 60s.
var probeTimeout = 60 * time.Second

const (
	// probeGrace is the small window after selectors_ready flips true before
	// snapshotting, letting a straggling ConfigOptionUpdate the agent emits in
	// the same tick land before we read.
	probeGrace = 500 * time.Millisecond
	// probePoll is the wait_for_session_options poll interval.
	probePoll = 50 * time.Millisecond
)

// ProbeAgentOptions spawns a transient agent, runs the ACP handshake, and waits
// for it to publish its initial session selectors (modes + config options),
// then returns a read-only snapshot of what the agent will accept. The process
// is torn down before returning — the probe never registers in the public
// connection registry, so WS clients never see probe traffic. Mirrors upstream
// ConnectionManager::probe_agent_options.
func (m *Manager) ProbeAgentOptions(agentType, workingDir string) (*AgentOptionsSnapshot, error) {
	m.mu.Lock()
	resolver := m.resolver
	spawner := m.spawner
	m.mu.Unlock()

	cwd := workingDir
	if cwd == "" {
		cwd = os.TempDir()
	}
	cfg, ok := resolver(agentType, cwd, nil)
	if !ok {
		return nil, &AgentUnavailableError{AgentType: agentType}
	}
	proc, err := spawner(cfg)
	if err != nil {
		return nil, &SpawnError{AgentType: agentType, Err: err}
	}

	// Serialize concurrent probes for the same agent type so rapid UI clicks
	// fan out to one real CLI process, not one per click (each up to 60s). The
	// per-agent mutex is held for the whole probe, including the handshake and
	// the wait, bounding it to one in-flight probe per agent type.
	lockVal, _ := m.probeLocks.LoadOrStore(agentType, &sync.Mutex{})
	lock := lockVal.(*sync.Mutex)
	lock.Lock()
	defer lock.Unlock()

	id := uuidV4()
	state := NewSessionState(id)
	sess := NewAgentSession(proc.Peer, state, agentType)

	ctx, cancel := context.WithTimeout(context.Background(), handshakeTimeout)
	if err := sess.Initialize(ctx); err != nil {
		cancel()
		_ = proc.Close()
		return nil, &SpawnError{AgentType: agentType, Err: err, Stderr: proc.StderrTail()}
	}
	if _, err := sess.NewSession(ctx, cwd, nil); err != nil {
		cancel()
		_ = proc.Close()
		return nil, &SpawnError{AgentType: agentType, Err: err, Stderr: proc.StderrTail()}
	}
	cancel()

	snap, waitErr := m.waitForSessionOptions(state, agentType)
	if waitErr != nil {
		// Prefer the agent's own captured error message over the generic
		// ProbeTimedOut / ConnectionNotFound — an agent that died on Initialize
		// already explained why.
		if le := state.LastErrorSnapshot(); le != nil {
			_ = proc.Close()
			return nil, &ProbeAgentError{Message: le.Message}
		}
		_ = proc.Close()
		return nil, waitErr
	}
	_ = proc.Close()
	return snap, nil
}

// waitForSessionOptions polls a connection's SessionState until the agent
// signals it has finished publishing its initial selectors (selectors_ready),
// then gives a small grace window for any tightly-following follow-up updates
// before snapshotting. Waiting on selectors_ready — not just config_options
// being present — matters because some agents emit an empty SessionConfigOptions
// first and then push the real options in a subsequent update; returning on the
// first empty vec would race ahead of those updates and report the agent as
// having nothing to configure. Returns ProbeTimedOutError when the timeout
// elapses without selectors_ready ever flipping true.
func (m *Manager) waitForSessionOptions(state *SessionState, agentType string) (*AgentOptionsSnapshot, error) {
	deadline := time.Now().Add(probeTimeout)
	var readyAt *time.Time
	for {
		if state.IsSelectorsReady() {
			if readyAt == nil {
				t := time.Now()
				readyAt = &t
			}
			if time.Since(*readyAt) >= probeGrace {
				// Commands ride along from the same probe session (the grace
				// window lets a late available_commands land before we read).
				return state.AgentOptions(), nil
			}
		}
		if time.Now().After(deadline) {
			return nil, &ProbeTimedOutError{AgentType: agentType}
		}
		time.Sleep(probePoll)
	}
}
