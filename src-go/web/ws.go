package web

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"yoyocode/src-go/acp"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// Sessions is what the router needs from app state to serve the attach
// protocol. Implemented by appstate.AppState.
type Sessions interface {
	Broadcaster() *Broadcaster
	Sessions() *acp.SessionRegistry
}

// deadline bounds control-frame writes so a wedged peer can't block us forever.
func deadline() time.Time { return time.Now().Add(5 * time.Second) }

// serveWS handles GET /ws/events.
//
// Protocol (upstream src-tauri/src/web/ws.rs):
//   - on connect: legacy `{"channel":"__ready__","payload":null}` firehose frame
//   - client → server: {action: attach|detach|ping}
//   - server → client: {type: snapshot|replay|event|detached|pong}
//
// Every server→client frame except the legacy firehose and __ready__ goes
// through one outbound channel so the socket has a single writer.
func (r *Router) serveWS(w http.ResponseWriter, req *http.Request) {
	// Auth: Bearer header or `upstream-token.<base64url>` WS subprotocol.
	if !Authorized(req, r.token) {
		WritePlain(w, http.StatusUnauthorized, "Invalid or missing token")
		return
	}

	conn, err := upgrader.Upgrade(w, req, nil)
	if err != nil {
		r.logger.Printf("[ws] upgrade failed: %v", err)
		return
	}
	defer conn.Close()

	shutdown := r.shutdown
	if shutdown == nil {
		shutdown = NewShutdownSignal()
	}
	// Late handshake guard: if shutdown already fired before we subscribed to
	// anything, close immediately rather than handing out a doomed session.
	if shutdown.IsTriggered() {
		_ = conn.WriteControl(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), deadline())
		return
	}

	// Legacy global firehose subscriber (`acp://event`, `conversation://changed`,
	// …). Retired in upstream Phase 4; still required by current clients.
	globalCh, unsub := r.broadcaster().Subscribe()
	defer unsub()

	outbound := make(chan ServerMsg, OutboundCapacity)
	cleanup := make(chan cleanupSignal, OutboundCapacity)

	var (
		mu            sync.Mutex
		writeMu       sync.Mutex
		subscriptions = map[string]*activeSubscription{}
		nextEpoch     uint64
	)

	// Legacy __ready__ frame. Payload MUST be null — the client's handshake
	// gate reads this channel before issuing acp_connect.
	writeJSON(conn, &writeMu, ReadyFrame())

	done := make(chan struct{})
	var doneOnce sync.Once
	closeDone := func() { doneOnce.Do(func() { close(done) }) }
	defer closeDone()

	// ── outbound writer: the socket's single owner ──────────────────────────
	go func() {
		defer closeDone()
		for {
			select {
			case msg := <-outbound:
				if !writeJSON(conn, &writeMu, msg) {
					return
				}
			case evt := <-globalCh:
				if !writeJSON(conn, &writeMu, evt) {
					return
				}
			case sig := <-cleanup:
				// Epoch guard: only drop the handle the signal was actually
				// for. A re-attach between the dead forwarder's exit and this
				// receive stamped a NEW epoch, so a stale signal is a no-op —
				// otherwise we'd orphan the fresh forwarder.
				mu.Lock()
				if sub, ok := subscriptions[sig.subscriptionID]; ok && sub.epoch == sig.epoch {
					delete(subscriptions, sig.subscriptionID)
				}
				mu.Unlock()
			case <-shutdown.Wait():
				// Notify every active subscription before closing so clients
				// know to re-attach after the next handshake.
				mu.Lock()
				for subID := range subscriptions {
					writeJSON(conn, &writeMu, ServerMsg{
						Type:           FrameTypeDetached,
						SubscriptionID: subID,
						Reason:         DetachServerShutdown,
					})
				}
				subscriptions = map[string]*activeSubscription{}
				mu.Unlock()
				_ = conn.WriteControl(websocket.CloseMessage,
					websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), deadline())
				return
			case <-done:
				return
			}
		}
	}()

	// ── client reader ──────────────────────────────────────────────────────
	for {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			return
		}
		var cmsg ClientMsg
		if err := json.Unmarshal(msg, &cmsg); err != nil {
			r.logger.Printf("[ws] malformed client message: %v", err)
			continue
		}
		switch cmsg.Action {
		case "attach":
			r.handleAttach(&mu, subscriptions, &nextEpoch, outbound, cleanup,
				cmsg.SubscriptionID, cmsg.ConnectionID, cmsg.SinceSeq)
		case "detach":
			mu.Lock()
			if sub, ok := subscriptions[cmsg.SubscriptionID]; ok {
				sub.stop()
				delete(subscriptions, cmsg.SubscriptionID)
			}
			mu.Unlock()
		case "ping":
			select {
			case outbound <- ServerMsg{Type: FrameTypePong}:
			default:
			}
		}
	}
}

// activeSubscription is one live attach forwarder. `epoch` is a per-WS-session
// monotonic id assigned at spawn time; it threads through the cleanup channel so
// a stale exit signal from a replaced handle can't remove the fresh one.
//
// The subscription is registered in the map BEFORE the forwarder goroutine
// starts, and `stop` is idempotent-and-sticky. Together these close the race
// where a client pipelines `detach` immediately behind `attach`: without a
// sticky flag, a detach processed before the goroutine was spawned would find
// nothing to stop and the forwarder would run forever, leaking a broadcast slot
// and streaming events the client already unsubscribed from.
type activeSubscription struct {
	epoch   uint64
	mu      sync.Mutex
	stopped bool
	stopCh  chan struct{}
}

func newActiveSubscription(epoch uint64) *activeSubscription {
	return &activeSubscription{epoch: epoch, stopCh: make(chan struct{})}
}

// stop is idempotent and sticky: a detach that lands before the forwarder
// starts is still honoured, because the forwarder re-checks IsStopped().
func (a *activeSubscription) stop() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.stopped {
		a.stopped = true
		close(a.stopCh)
	}
}

func (a *activeSubscription) IsStopped() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.stopped
}

func (a *activeSubscription) done() <-chan struct{} { return a.stopCh }

type cleanupSignal struct {
	subscriptionID string
	epoch          uint64
}

// handleAttach is the decide-and-subscribe step of upstream
// `ws_attach::handle_attach` plus the forwarder spawn from `ws::handle_client_msg`.
func (r *Router) handleAttach(
	mu *sync.Mutex,
	subscriptions map[string]*activeSubscription,
	nextEpoch *uint64,
	outbound chan ServerMsg,
	cleanup chan cleanupSignal,
	subscriptionID, connectionID string,
	sinceSeq *uint64,
) {
	// Re-attach with the same subscription_id replaces the prior forwarder;
	// stop the old one first so its receiver drops and we don't leak a slot.
	mu.Lock()
	if old, ok := subscriptions[subscriptionID]; ok {
		old.stop()
		delete(subscriptions, subscriptionID)
	}
	*nextEpoch++
	sub := newActiveSubscription(*nextEpoch)
	mu.Unlock()

	detach := func(reason DetachReason) {
		select {
		case outbound <- ServerMsg{
			Type:           FrameTypeDetached,
			SubscriptionID: subscriptionID,
			Reason:         reason,
		}:
		default:
		}
	}

	registry := r.sessions()
	if registry == nil {
		detach(DetachConnectionGone)
		return
	}

	state := registry.GetState(connectionID)
	if state == nil {
		// Unknown (GC'd or never existed) — terminal for this conversation; the
		// client must not retry against the same connection id.
		detach(DetachConnectionGone)
		return
	}

	metrics := registry.Metrics()
	head := state.EventSeqNow()

	snapshotMsg := func() ServerMsg {
		return ServerMsg{
			Type:           FrameTypeSnapshot,
			SubscriptionID: subscriptionID,
			ConnectionID:   connectionID,
			Snapshot:       state.Snapshot(),
			EventSeq:       head,
		}
	}

	// Decide response shape. Order matters and mirrors upstream exactly:
	//   - no cursor           → snapshot (cold start)
	//   - cursor >= head      → snapshot (defensive: client advanced past us)
	//   - small gap in buffer → replay
	//   - large gap / buffer doesn't cover cursor → snapshot (cheaper)
	var initial ServerMsg
	switch {
	case sinceSeq == nil || *sinceSeq >= head:
		metrics.SnapshotColdCount.Add(1)
		initial = snapshotMsg()
	default:
		events, covered := state.RecentEventsAfter(*sinceSeq)
		if covered && len(events) > 0 && len(events) <= ReplayBatchThreshold {
			metrics.ReplayCount.Add(1)
			metrics.ReplayEventTotal.Add(uint64(len(events)))
			initial = ServerMsg{
				Type:           FrameTypeReplay,
				SubscriptionID: subscriptionID,
				ConnectionID:   connectionID,
				Events:         events,
				HighWaterSeq:   events[len(events)-1].Seq,
			}
		} else {
			metrics.SnapshotFallbackCount.Add(1)
			initial = snapshotMsg()
		}
	}

	// Register BEFORE sending the initial frame. A client that pipelines
	// `detach` right behind `attach` would otherwise hit an empty map and the
	// forwarder would never learn it was cancelled.
	mu.Lock()
	subscriptions[subscriptionID] = sub
	mu.Unlock()

	// Send the initial frame BEFORE spawning the forwarder so the client sees
	// state before the first live event.
	select {
	case outbound <- initial:
	default:
		sub.stop()
		return
	}

	// Forwarder: drains the per-connection broadcast receiver and pushes
	// `event` frames into the shared outbound queue. Exits on stop (detach /
	// socket close) or Lagged (slow consumer); on lag it emits
	// `detached{reason:"lagged"}` so the client re-attaches with its cursor.
	go func() {
		// Honour a detach that landed while we were still deciding.
		if sub.IsStopped() {
			return
		}
		receiver := state.Subscribe()
		defer receiver.Close()

		signalCleanup := func() {
			select {
			case cleanup <- cleanupSignal{subscriptionID: subscriptionID, epoch: sub.epoch}:
			default:
			}
		}
		for {
			select {
			case <-sub.done():
				return
			case env, ok := <-receiver.Events():
				if !ok {
					detach(DetachConnectionGone)
					signalCleanup()
					return
				}
				// Upstream checks lag before delivering: a receiver that
				// dropped events must not resume mid-stream.
				if receiver.Lagged() > 0 {
					metrics.ForwarderLaggedCount.Add(1)
					detach(DetachLagged)
					signalCleanup()
					return
				}
				select {
				case outbound <- ServerMsg{
					Type:           FrameTypeEvent,
					SubscriptionID: subscriptionID,
					Envelope:       env,
				}:
				case <-sub.done():
					return
				}
			}
		}
	}()
}

// sessions resolves the ACP session registry from router state.
func (r *Router) sessions() *acp.SessionRegistry {
	if s, ok := r.state.(Sessions); ok && s != nil {
		return s.Sessions()
	}
	return nil
}

// broadcaster returns the shared Broadcaster if the router's state provides one.
func (r *Router) broadcaster() *Broadcaster {
	if b, ok := r.state.(interface{ Broadcaster() *Broadcaster }); ok && b != nil {
		return b.Broadcaster()
	}
	return NewBroadcaster()
}

// writeJSON serializes any server frame under the socket write lock.
func writeJSON(conn *websocket.Conn, mu *sync.Mutex, v any) bool {
	data, err := json.Marshal(v)
	if err != nil {
		return false
	}
	mu.Lock()
	defer mu.Unlock()
	if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
		return false
	}
	return true
}
