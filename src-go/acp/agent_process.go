package acp

// Agent subprocess spawn: launch an ACP agent binary with piped stdio and wrap
// its stdin/stdout in a Peer. Mirrors upstream acp::agent_process (minus the
// binary-cache/download resolution, which the registry+preflight layer feeds in
// as the resolved command).

import (
	"bufio"
	"io"
	"os"
	"os/exec"
	"sync"
)

// AgentProcess is a running agent child plus its JSON-RPC peer.
type AgentProcess struct {
	Cmd  *exec.Cmd
	Peer *Peer
	// CloseFn runs on Close for injected/in-memory agents (test fakes).
	CloseFn func()
	stderr  *stderrTail
	runErr  chan error
}

// SpawnConfig describes how to launch an agent.
type SpawnConfig struct {
	// Command is the executable (resolved by the registry/preflight layer:
	// an npx/uvx launcher or a cached binary path).
	Command string
	Args    []string
	// Env entries are layered over the parent environment (KEY=VALUE semantics
	// via the map; empty-string value still sets the var to empty).
	Env map[string]string
	Cwd string
}

// SpawnAgent starts the agent child, wires a Peer over its stdio, and begins
// the peer read loop in the background. The caller drives the ACP handshake on
// the returned Peer and must call Close when done.
func SpawnAgent(cfg SpawnConfig) (*AgentProcess, error) {
	cmd := exec.Command(cfg.Command, cfg.Args...)
	if cfg.Cwd != "" {
		cmd.Dir = cfg.Cwd
	}
	cmd.Env = mergeEnv(os.Environ(), cfg.Env)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	tail := newStderrTail(64)
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}

	if err := cmd.Start(); err != nil {
		return nil, err
	}

	go tail.consume(stderrPipe)

	ap := &AgentProcess{
		Cmd:    cmd,
		Peer:   NewPeer(stdout, stdin),
		stderr: tail,
		runErr: make(chan error, 1),
	}
	go func() { ap.runErr <- ap.Peer.Run() }()
	return ap, nil
}

// StderrTail returns the recent stderr lines (diagnostics for a failed launch).
func (ap *AgentProcess) StderrTail() []string { return ap.stderr.lines() }

// Wait blocks until the child exits.
func (ap *AgentProcess) Wait() error {
	if ap.Cmd == nil {
		return nil
	}
	return ap.Cmd.Wait()
}

// Close terminates the child and tears down the peer. A nil Cmd (an injected
// in-memory fake agent, used in tests) is a no-op beyond any CloseFn.
func (ap *AgentProcess) Close() error {
	if ap.CloseFn != nil {
		ap.CloseFn()
	}
	if ap.Cmd == nil {
		return nil
	}
	if ap.Cmd.Process != nil {
		_ = ap.Cmd.Process.Kill()
	}
	return ap.Cmd.Wait()
}

func mergeEnv(base []string, overrides map[string]string) []string {
	if len(overrides) == 0 {
		return base
	}
	out := make([]string, 0, len(base)+len(overrides))
	seen := map[string]bool{}
	for k := range overrides {
		seen[k] = true
	}
	for _, kv := range base {
		key := kv
		for i := 0; i < len(kv); i++ {
			if kv[i] == '=' {
				key = kv[:i]
				break
			}
		}
		if seen[key] {
			continue
		}
		out = append(out, kv)
	}
	for k, v := range overrides {
		out = append(out, k+"="+v)
	}
	return out
}

// stderrTail keeps the last N stderr lines for diagnostics without unbounded
// memory (a chatty agent must not grow this without bound).
type stderrTail struct {
	mu  sync.Mutex
	buf []string
	max int
}

func newStderrTail(max int) *stderrTail { return &stderrTail{max: max} }

func (t *stderrTail) consume(r io.Reader) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 8192), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		t.mu.Lock()
		t.buf = append(t.buf, line)
		if len(t.buf) > t.max {
			t.buf = t.buf[len(t.buf)-t.max:]
		}
		t.mu.Unlock()
	}
}

func (t *stderrTail) lines() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]string, len(t.buf))
	copy(out, t.buf)
	return out
}
