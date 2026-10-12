package acp

// Minimal JSON-RPC 2.0 peer over newline-delimited framing — the transport ACP
// agents speak on stdio (one JSON object per line). This is the backbone the
// connection layer builds on: it multiplexes our outgoing requests/notifications
// against the agent's responses, and dispatches the agent's own
// requests/notifications (session/update, fs/read_text_file, permission
// requests) back to registered handlers.

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
)

// rpcMessage is the on-wire JSON-RPC 2.0 envelope (request, response, or
// notification — distinguished by which fields are present).
type rpcMessage struct {
	Jsonrpc string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

// RPCError is a JSON-RPC 2.0 error object.
type RPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *RPCError) Error() string {
	if e == nil {
		return "<nil rpc error>"
	}
	return fmt.Sprintf("rpc error %d: %s", e.Code, e.Message)
}

// RequestHandler answers an incoming request from the peer. Returning an error
// sends a JSON-RPC error response; otherwise the (JSON-marshalable) result is
// sent back.
type RequestHandler func(params json.RawMessage) (any, error)

// NotificationHandler consumes an incoming notification (no response).
type NotificationHandler func(params json.RawMessage)

type pending struct {
	ch chan rpcMessage
}

// Peer is a JSON-RPC 2.0 endpoint over an io.Reader/io.Writer pair.
type Peer struct {
	reader  io.Reader
	w       io.Writer
	writeMu sync.Mutex

	nextID atomic.Int64

	mu       sync.Mutex
	pending  map[int64]*pending
	requests map[string]RequestHandler
	notifs   map[string]NotificationHandler
	closed   bool
	closeErr error
	done     chan struct{}
}

// NewPeer builds a peer that reads from r and writes to w. Call Run (usually in
// a goroutine) to drive the read loop.
func NewPeer(r io.Reader, w io.Writer) *Peer {
	p := &Peer{
		w:        w,
		pending:  map[int64]*pending{},
		requests: map[string]RequestHandler{},
		notifs:   map[string]NotificationHandler{},
		done:     make(chan struct{}),
	}
	p.reader = r
	return p
}

// HandleRequest registers a handler for an incoming request method.
func (p *Peer) HandleRequest(method string, h RequestHandler) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.requests[method] = h
}

// HandleNotification registers a handler for an incoming notification method.
func (p *Peer) HandleNotification(method string, h NotificationHandler) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.notifs[method] = h
}

// Run drives the read loop until EOF or a fatal read/parse error. It fails all
// pending calls on exit. Safe to call once.
func (p *Peer) Run() error {
	scanner := bufio.NewScanner(p.reader)
	// Agent messages can be large (a full turn's tool output in one update);
	// allow lines up to 16 MiB.
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var msg rpcMessage
		if err := json.Unmarshal(line, &msg); err != nil {
			// A non-JSON line (agent stderr bleed, banner) is skipped rather
			// than killing the session.
			continue
		}
		p.dispatch(msg)
	}
	err := scanner.Err()
	p.shutdown(err)
	return err
}

func (p *Peer) dispatch(msg rpcMessage) {
	// A response carries an id and (result or error) but no method.
	if msg.Method == "" && len(msg.ID) > 0 {
		if id, ok := parseID(msg.ID); ok {
			p.mu.Lock()
			pend := p.pending[id]
			delete(p.pending, id)
			p.mu.Unlock()
			if pend != nil {
				pend.ch <- msg
			}
		}
		return
	}
	// A request carries a method AND an id; a notification carries a method but
	// no id.
	if msg.Method != "" && len(msg.ID) > 0 {
		p.mu.Lock()
		h := p.requests[msg.Method]
		p.mu.Unlock()
		go p.serveRequest(msg, h)
		return
	}
	if msg.Method != "" {
		p.mu.Lock()
		h := p.notifs[msg.Method]
		p.mu.Unlock()
		if h != nil {
			go h(msg.Params)
		}
	}
}

func (p *Peer) serveRequest(msg rpcMessage, h RequestHandler) {
	if h == nil {
		p.writeMessage(rpcMessage{
			Jsonrpc: "2.0", ID: msg.ID,
			Error: &RPCError{Code: -32601, Message: "method not found: " + msg.Method},
		})
		return
	}
	result, err := h(msg.Params)
	if err != nil {
		// Preserve a handler-supplied *RPCError (code + message + data) verbatim;
		// only synthesize a generic -32000 for a plain error.
		rpcErr, ok := err.(*RPCError)
		if !ok {
			rpcErr = &RPCError{Code: -32000, Message: err.Error()}
		}
		p.writeMessage(rpcMessage{Jsonrpc: "2.0", ID: msg.ID, Error: rpcErr})
		return
	}
	raw, mErr := json.Marshal(result)
	if mErr != nil {
		p.writeMessage(rpcMessage{
			Jsonrpc: "2.0", ID: msg.ID,
			Error: &RPCError{Code: -32603, Message: "marshal result: " + mErr.Error()},
		})
		return
	}
	p.writeMessage(rpcMessage{Jsonrpc: "2.0", ID: msg.ID, Result: raw})
}

// Call sends a request and waits for the matching response (or ctx cancel).
func (p *Peer) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id := p.nextID.Add(1)
	raw, err := marshalParams(params)
	if err != nil {
		return nil, err
	}
	idRaw, _ := json.Marshal(id)
	pend := &pending{ch: make(chan rpcMessage, 1)}

	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil, p.closeErrOr()
	}
	p.pending[id] = pend
	p.mu.Unlock()

	if err := p.writeMessage(rpcMessage{Jsonrpc: "2.0", ID: idRaw, Method: method, Params: raw}); err != nil {
		p.mu.Lock()
		delete(p.pending, id)
		p.mu.Unlock()
		return nil, err
	}

	select {
	case <-ctx.Done():
		p.mu.Lock()
		delete(p.pending, id)
		p.mu.Unlock()
		return nil, ctx.Err()
	case resp := <-pend.ch:
		if resp.Error != nil {
			return nil, resp.Error
		}
		return resp.Result, nil
	case <-p.done:
		return nil, p.closeErrOr()
	}
}

// Notify sends a notification (no id, no response).
func (p *Peer) Notify(method string, params any) error {
	raw, err := marshalParams(params)
	if err != nil {
		return err
	}
	return p.writeMessage(rpcMessage{Jsonrpc: "2.0", Method: method, Params: raw})
}

func (p *Peer) writeMessage(msg rpcMessage) error {
	msg.Jsonrpc = "2.0"
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	_, err = p.w.Write(data)
	return err
}

func (p *Peer) shutdown(err error) {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	p.closed = true
	p.closeErr = err
	pend := p.pending
	p.pending = map[int64]*pending{}
	close(p.done)
	p.mu.Unlock()
	for _, pr := range pend {
		close(pr.ch)
	}
}

func (p *Peer) closeErrOr() error {
	if p.closeErr != nil {
		return p.closeErr
	}
	return io.ErrClosedPipe
}

func marshalParams(params any) (json.RawMessage, error) {
	if params == nil {
		return nil, nil
	}
	if raw, ok := params.(json.RawMessage); ok {
		return raw, nil
	}
	return json.Marshal(params)
}

func parseID(raw json.RawMessage) (int64, bool) {
	var n int64
	if err := json.Unmarshal(raw, &n); err == nil {
		return n, true
	}
	// Some peers echo string ids.
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		var sn int64
		if _, err := fmt.Sscanf(s, "%d", &sn); err == nil {
			return sn, true
		}
	}
	return 0, false
}
