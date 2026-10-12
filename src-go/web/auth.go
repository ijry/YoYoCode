package web

import (
	"encoding/base64"
	"net/http"
	"strings"
)

// Mirrors upstream src-tauri/src/web/auth.rs.
const (
	WSEventProtocol       = "codeg-events"
	WSTokenProtocolPrefix = "codeg-token."
	tokenQueryKey         = "token"
)

// tokenFromWSProtocols parses the Sec-WebSocket-Protocol header list for a
// `upstream-token.<base64url>` subprotocol carrying the auth token.
func tokenFromWSProtocols(value string) (string, bool) {
	for _, p := range strings.Split(value, ",") {
		p = strings.TrimSpace(p)
		if strings.HasPrefix(p, WSTokenProtocolPrefix) {
			encoded := strings.TrimPrefix(p, WSTokenProtocolPrefix)
			raw, err := base64.RawURLEncoding.DecodeString(encoded)
			if err != nil {
				return "", false
			}
			return string(raw), true
		}
	}
	return "", false
}

// Authorized reports whether a request carries a valid token via either
// Authorization: Bearer or a WS subprotocol (handshake headers only).
func Authorized(r *http.Request, token string) bool {
	if token == "" {
		return false
	}
	if h := r.Header.Get("authorization"); h != "" {
		if v, ok := strings.CutPrefix(h, "Bearer "); ok && v == token {
			return true
		}
	}
	if p := r.Header.Get("sec-websocket-protocol"); p != "" {
		if t, ok := tokenFromWSProtocols(p); ok && t == token {
			return true
		}
	}
	return false
}

// BuildWSProtocols returns the subprotocol list a upstream client sends.
func BuildWSProtocols(token string) []string {
	if strings.TrimSpace(token) == "" {
		return []string{WSEventProtocol}
	}
	return []string{
		WSEventProtocol,
		WSTokenProtocolPrefix + base64.RawURLEncoding.EncodeToString([]byte(token)),
	}
}
