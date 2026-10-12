package terminal

// Cross-platform terminal (PTY) manager mirroring src-tauri/src/terminal/
// manager.rs. Each terminal gets a pseudo-console, a 128KB scrollback, and a
// reader goroutine that broadcasts terminal://output/{id} chunks (and a final
// terminal://exit/{id}) on the web firehose — the exact channels + payloads
// mcode-app subscribes to. The platform PTY backend is startPTY (ConPTY on
// Windows, unix98 pty elsewhere).

import (
	"crypto/rand"
	"fmt"
	"strings"
	"sync"
)

const scrollbackMaxChars = 128 * 1024

// Emitter is the narrow firehose contract (satisfied by *web.Broadcaster),
// kept here so this package does not import web.
type Emitter interface {
	Send(channel string, payload any)
}

// ptyProc is the platform PTY handle the manager drives.
type ptyProc interface {
	Read(p []byte) (int, error)
	Write(p []byte) (int, error)
	Resize(cols, rows uint16) error
	Terminate() error
	Exited() bool
	Close() error
}

type scrollback struct {
	mu     sync.Mutex
	chunks []string
	chars  int
	seq    uint64
}

func (s *scrollback) record(data string) uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq++
	s.chunks = append(s.chunks, data)
	s.chars += len([]rune(data))
	for s.chars > scrollbackMaxChars && len(s.chunks) > 1 {
		s.chars -= len([]rune(s.chunks[0]))
		s.chunks = s.chunks[1:]
	}
	return s.seq
}

func (s *scrollback) read() (string, uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return strings.Join(s.chunks, ""), s.seq
}

type instance struct {
	pty    ptyProc
	title  string
	scroll *scrollback
	once   sync.Once
}

// SpawnOptions mirrors terminal::manager::SpawnOptions (web-relevant fields).
type SpawnOptions struct {
	TerminalID     string
	WorkingDir     string
	Shell          string
	InitialCommand string
	ExtraEnv       []string
}

type Manager struct {
	mu        sync.Mutex
	terminals map[string]*instance
	emitter   Emitter
}

func NewManager(e Emitter) *Manager {
	return &Manager{terminals: map[string]*instance{}, emitter: e}
}

// Spawn starts a PTY and returns its id. A caller-supplied TerminalID is kept
// (reject duplicates to avoid orphaning a live child); otherwise a random id is
// minted.
func (m *Manager) Spawn(opts SpawnOptions) (string, error) {
	id := opts.TerminalID
	if id == "" || len(id) > 256 {
		id = newTerminalID()
	}
	m.mu.Lock()
	if _, exists := m.terminals[id]; exists {
		m.mu.Unlock()
		return "", fmt.Errorf("terminal id %q already exists", id)
	}
	m.mu.Unlock()

	p, err := startPTY(opts)
	if err != nil {
		return "", err
	}
	inst := &instance{pty: p, title: "Terminal", scroll: &scrollback{}}
	m.mu.Lock()
	m.terminals[id] = inst
	m.mu.Unlock()
	go m.readLoop(id, inst)
	return id, nil
}

func (m *Manager) readLoop(id string, inst *instance) {
	event := "terminal://output/" + id
	buf := make([]byte, 8192)
	for {
		n, err := inst.pty.Read(buf)
		if n > 0 {
			data := string(buf[:n])
			seq := inst.scroll.record(data)
			m.emit(event, TerminalEvent{TerminalID: id, Data: data, Seq: seq})
		}
		if err != nil {
			break
		}
	}
	m.finalize(id, inst)
}

// finalize removes a terminal, closes its PTY and emits the exit event exactly
// once — whether it is reached by the reader hitting EOF, a reap in List/
// Snapshot, or a Kill.
func (m *Manager) finalize(id string, inst *instance) {
	inst.once.Do(func() {
		m.mu.Lock()
		delete(m.terminals, id)
		m.mu.Unlock()
		_ = inst.pty.Close()
		m.emitExit(id)
	})
}

func (m *Manager) get(id string) *instance {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.terminals[id]
}

// Write feeds bytes to the terminal stdin.
func (m *Manager) Write(id string, data []byte) error {
	inst := m.get(id)
	if inst == nil {
		return fmt.Errorf("terminal %q not found", id)
	}
	_, err := inst.pty.Write(data)
	return err
}

// Resize changes the pseudo-console dimensions.
func (m *Manager) Resize(id string, cols, rows uint16) error {
	inst := m.get(id)
	if inst == nil {
		return fmt.Errorf("terminal %q not found", id)
	}
	return inst.pty.Resize(cols, rows)
}

// Kill terminates the child process; the reader goroutine then sees EOF, reaps
// the entry, closes the PTY and emits the exit event. Terminating (rather than
// closing the PTY here) avoids closing the output handle while the reader is
// blocked in Read on it — which corrupts the heap on Windows ConPTY.
func (m *Manager) Kill(id string) error {
	inst := m.get(id)
	if inst == nil {
		return fmt.Errorf("terminal %q not found", id)
	}
	return inst.pty.Terminate()
}

// List returns the live terminals, reaping any whose child has exited first
// (ConPTY can be slow to surface EOF on the read side).
func (m *Manager) List() []TerminalInfo {
	m.reapExited()
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []TerminalInfo{}
	for id, inst := range m.terminals {
		out = append(out, TerminalInfo{ID: id, Title: inst.title})
	}
	return out
}

// reapExited finalizes every terminal whose child process has exited.
func (m *Manager) reapExited() {
	m.mu.Lock()
	type pair struct {
		id   string
		inst *instance
	}
	var dead []pair
	for id, inst := range m.terminals {
		if inst.pty.Exited() {
			dead = append(dead, pair{id, inst})
		}
	}
	m.mu.Unlock()
	for _, d := range dead {
		m.finalize(d.id, d.inst)
	}
}

// Snapshot returns recent output + cursor, or {alive:false} if gone. A terminal
// whose child has exited is reaped and reported not-alive.
func (m *Manager) Snapshot(id string) TerminalSnapshot {
	inst := m.get(id)
	if inst == nil {
		return TerminalSnapshot{Alive: false, Data: "", Seq: 0}
	}
	if inst.pty.Exited() {
		m.finalize(id, inst)
		return TerminalSnapshot{Alive: false, Data: "", Seq: 0}
	}
	data, seq := inst.scroll.read()
	return TerminalSnapshot{Alive: true, Data: data, Seq: seq}
}

func (m *Manager) emit(channel string, ev TerminalEvent) {
	if m.emitter != nil {
		m.emitter.Send(channel, ev)
	}
}

func (m *Manager) emitExit(id string) {
	m.emit("terminal://exit/"+id, TerminalEvent{TerminalID: id, Data: "", Seq: 0})
}

func newTerminalID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
