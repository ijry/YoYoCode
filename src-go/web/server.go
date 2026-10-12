package web

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

// DEFAULT_WEB_SERVICE_PORT mirrors upstream web::DEFAULT_WEB_SERVICE_PORT.
const DEFAULT_WEB_SERVICE_PORT = 3080

// Config keys mirror upstream WebService* keys stored in app_metadata.
const (
	WebServiceTokenKey     = "web_service_token"
	WebServicePortKey      = "web_service_port"
	WebServiceAutoStartKey = "web_service_auto_start"
)

// apiCommand extracts the command from /api/<command>. Returns "" if not an API path.
func apiCommand(path string) string {
	const prefix = "/api/"
	if !strings.HasPrefix(path, prefix) {
		return ""
	}
	cmd := strings.TrimPrefix(path, prefix)
	// Strip trailing slash if present
	return strings.TrimSuffix(cmd, "/")
}

// Router orchestrates a single request through auth + dispatch.
type Router struct {
	commands       map[string]HandlerFunc
	publicCommands map[string]bool
	token          string
	state          any
	logger         *log.Logger
	startTime      time.Time
	shutdown       *ShutdownSignal
	staticDir      string
	// rawRoutes are non-/api/ GET-style endpoints that write their own HTTP
	// response (download tickets, raw file/zip streams). Checked by prefix.
	rawRoutes []rawRoute
}

func NewRouter(state any) *Router {
	return &Router{
		commands:       map[string]HandlerFunc{},
		publicCommands: map[string]bool{},
		state:          state,
		logger:         log.New(io.Discard, "", 0),
		startTime:      time.Now(),
	}
}

func (r *Router) Register(command string, fn HandlerFunc) { r.commands[command] = fn }

// rawRoute is a prefix-matched handler that writes its own response.
type rawRoute struct {
	prefix string
	fn     func(w http.ResponseWriter, req *http.Request)
}

// RegisterRaw registers a non-/api/ endpoint matched by path prefix. Used for
// streamed downloads (e.g. /workspace_download/<ticket>). The handler writes
// the full HTTP response itself.
func (r *Router) RegisterRaw(prefix string, fn func(w http.ResponseWriter, req *http.Request)) {
	r.rawRoutes = append(r.rawRoutes, rawRoute{prefix: prefix, fn: fn})
}

func (r *Router) RegisterPublic(command string, fn HandlerFunc) {
	r.commands[command] = fn
	r.publicCommands[command] = true
}

func (r *Router) Unregister(command string) { delete(r.commands, command) }

func (r *Router) Has(command string) bool { _, ok := r.commands[command]; return ok }

func (r *Router) SetToken(token string) { r.token = token }

func (r *Router) SetLogger(l *log.Logger) { r.logger = l }

// SetShutdown wires the shutdown latch so live WS sessions emit a final
// `detached{reason:"server_shutdown"}` before the socket closes.
func (r *Router) SetShutdown(s *ShutdownSignal) { r.shutdown = s }

// SetStaticDir serves bundled web assets from dir (upstream ServeDir). Empty
// means no bundled frontend — the native mygo UI drives the app instead.
func (r *Router) SetStaticDir(dir string) { r.staticDir = dir }

// ServeHTTP is the top-level handler: CORS + auth + dispatch + WS + static.
func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	// Mirror upstream CorsLayer.
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, PATCH, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "*")
	if req.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	path := req.URL.Path

	// WebSocket endpoint.
	if path == "/ws/events" {
		r.serveWS(w, req)
		return
	}

	// API commands are POST under /api/.
	if path == "/api/health" || strings.HasPrefix(path, "/api/") {
		cmd := apiCommand(path)
		r.serveAPI(w, req, cmd)
		return
	}

	// Raw prefix-matched endpoints (streamed downloads) write their own response.
	for _, rr := range r.rawRoutes {
		if strings.HasPrefix(path, rr.prefix) {
			rr.fn(w, req)
			return
		}
	}

	r.serveStatic(w, req)
}

// serveAPI dispatches a single API command.
func (r *Router) serveAPI(w http.ResponseWriter, req *http.Request, cmd string) {
	fn, ok := r.commands[cmd]
	if !ok {
		// axum's default fallback for an unmatched route: bare 404 text.
		WritePlain(w, http.StatusNotFound, "Not Found")
		return
	}

	// Auth (upstream require_token middleware on protected router). The body
	// is bare text, matching `(StatusCode::UNAUTHORIZED, "Invalid or missing
	// token").into_response()`.
	if !r.publicCommands[cmd] && !Authorized(req, r.token) {
		WritePlain(w, http.StatusUnauthorized, "Invalid or missing token")
		return
	}

	ctx := &Context{Command: cmd, Body: map[string]any{}, W: w, R: req, State: r.state}
	if req.Body != nil {
		defer req.Body.Close()
		raw, err := io.ReadAll(io.LimitReader(req.Body, 64<<20))
		if err == nil {
			ctx.RawBody = raw
			if len(raw) > 0 {
				_ = json.Unmarshal(raw, &ctx.Body)
			}
		}
	}

	res, err := fn(ctx)
	if ctx.Handled {
		// The handler streamed its own response (file/zip/raw bytes).
		return
	}
	if err != nil {
		WriteErr(w, err)
		return
	}
	if res == nil {
		// Upstream handlers returning Option::None or Json(()) serialize to
		// JSON `null`, not `{}` — e.g. get_git_branch / get_account_token /
		// get_web_server_status, whose clients branch on a null value.
		WriteJSON(w, http.StatusOK, nil)
		return
	}
	WriteOK(w, res)
}

// serveStatic serves public/ assets; native mode has no bundled web yet.
func (r *Router) serveStatic(w http.ResponseWriter, req *http.Request) {
	http.NotFound(w, req)
}
