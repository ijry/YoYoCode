// Package handlers mirrors upstream src-tauri/src/web/handlers.
// Register wires every API command to the router.
package handlers

import (
	"net/http"
	"yoyocode/src-go/web"
)

// Registry owns the command→handler map shared with the router.
type Registry struct {
	web *web.Router
}

func NewRegistry(r *web.Router) *Registry {
	return &Registry{web: r}
}

func (reg *Registry) Register(command string, fn web.HandlerFunc) { reg.web.Register(command, fn) }

// RegisterRaw registers a prefix-matched endpoint that writes its own HTTP
// response (streamed downloads).
func (reg *Registry) RegisterRaw(prefix string, fn func(w http.ResponseWriter, req *http.Request)) {
	reg.web.RegisterRaw(prefix, fn)
}
func (reg *Registry) RegisterPublic(command string, fn web.HandlerFunc) {
	reg.web.RegisterPublic(command, fn)
}
