// Command yoyocode is the native-UI desktop build: a single Go binary that
// boots the upstream backend (src-go web server) in-process on a loopback port
// and renders the workspace in a mygo native-UI window — the Go analog of the
// upstream Tauri + Next.js shell (src-tauri + src/app).
//
// Run a live-reloading dev preview with:
//
//	go tool mygo dev ./cmd/yoyocode     # rebuilds + relaunches on change
//
// It opens a real desktop window (WebView2/GPU), so it needs a display; a
// headless CI box can build it but not show it.
package main

import (
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"yoyocode/src/app"

	appstate "yoyocode/src-go/app_state"
	"yoyocode/src-go/db"
	"yoyocode/src-go/web"
	"yoyocode/src-go/web/handlers"
)

// bootBackend starts the web server on 127.0.0.1:<random> and returns an
// app.Backend bound to it. It serves in a background goroutine for the app's
// lifetime, using the exact HTTP contract mcode-app speaks.
func bootBackend() (*app.Backend, error) {
	home, _ := os.UserHomeDir()
	dataDir := filepath.Join(home, ".yoyocode")
	if env := os.Getenv("YOYOCODE_DATA_DIR"); env != "" {
		dataDir = env
	}
	database, err := db.Open(dataDir)
	if err != nil {
		return nil, fmt.Errorf("db init: %w", err)
	}
	st := appstate.Init(database, dataDir)

	token := strings.ReplaceAll(fmt.Sprintf("yoyo-%d", time.Now().UnixNano()), "-", "")
	router := web.NewRouter(st)
	router.SetToken(token)
	handlers.RegisterAll(handlers.NewRegistry(router))

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("listen: %w", err)
	}
	addr := ln.Addr().(*net.TCPAddr)
	st.Server.MarkRunning(addr.IP.String(), uint16(addr.Port), token)
	go func() {
		srv := &http.Server{Handler: router}
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			log.Printf("[yoyocode] backend serve: %v", err)
		}
	}()
	return &app.Backend{URL: fmt.Sprintf("http://%s:%d", addr.IP, addr.Port), Token: token}, nil
}

func main() {
	b, err := bootBackend()
	if err != nil {
		log.Fatalf("[yoyocode] %v", err)
	}
	shell := app.New(b)

	mygo.App.WhenReady(func() {
		mygo.NewWindow(mygo.WindowOptions{
			Title:   "YoYoCode",
			Width:   1240,
			Height:  820,
			Content: ui.View(shell.View),
		})
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
