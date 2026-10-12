// yoyocode-server is the standalone server binary — the Go analog of upstream
// src-tauri/src/bin/upstream_server.rs. It serves /api/* and /ws/events on
// 0.0.0.0:3080 by default, using the same protocol as upstream so that
// mcode-app can connect without changes.
package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"yoyocode/src-go/app_state"
	"yoyocode/src-go/db"
	"yoyocode/src-go/web"
	"yoyocode/src-go/web/handlers"
)

func main() {
	var dataDir string
	var token string
	flag.StringVar(&dataDir, "data-dir", "", "data directory (default: ~/.yoyocode)")
	flag.StringVar(&token, "token", "", "access token (defaults to env YOYOCODE_TOKEN)")
	flag.Parse()

	host := os.Getenv("YOYOCODE_HOST")
	if host == "" {
		host = "0.0.0.0"
	}
	port := uint16(web.DEFAULT_WEB_SERVICE_PORT)
	if p := os.Getenv("YOYOCODE_PORT"); p != "" {
		var parsed int
		if _, err := fmt.Sscanf(p, "%d", &parsed); err == nil && parsed > 0 {
			port = uint16(parsed)
		}
	}

	// Resolve data dir.
	if dataDir == "" {
		dataDir = filepath.Join(homeDir(), ".yoyocode")
	}
	if env := os.Getenv("YOYOCODE_DATA_DIR"); env != "" {
		dataDir = env
	}

	// Open DB.
	database, err := db.Open(dataDir)
	if err != nil {
		log.Fatalf("[SERVER] db init failed: %v", err)
	}
	defer database.Conn.Close()

	// Resolve token: env → persisted → generated.
	if token == "" {
		token = os.Getenv("YOYOCODE_TOKEN")
	}
	if token == "" {
		token = strings.TrimSpace(resolvePersistedToken(database))
	}
	if token == "" {
		token = generateToken()
		persistToken(database, token)
		fmt.Fprintf(os.Stderr, "[SERVER] generated access token (persisted): %s\n", token)
	}

	st := appstate.Init(database, dataDir)

	// Register handlers.
	shutdown := web.NewShutdownSignal()
	router := web.NewRouter(st)
	router.SetToken(token)
	router.SetShutdown(shutdown)
	rec := handlers.NewRegistry(router)
	handlers.RegisterAll(rec)

	// Bind.
	addr := fmt.Sprintf("%s:%d", host, port)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("[SERVER] failed to bind %s: %v", addr, err)
	}
	localAddr := listener.Addr().(*net.TCPAddr)
	actualPort := uint16(localAddr.Port)
	st.Server.MarkRunning(localAddr.IP.String(), actualPort, token)
	fmt.Fprintf(os.Stderr, "[SERVER] yoyocode-server v0.1.0\n")
	fmt.Fprintf(os.Stderr, "[SERVER] Data directory: %s\n", dataDir)
	fmt.Fprintf(os.Stderr, "[SERVER] Token: %s\n", token)
	fmt.Fprintf(os.Stderr, "[SERVER] Listening on http://%s:%d\n", localAddr.IP, actualPort)

	srv := &http.Server{Handler: router}

	// Graceful shutdown: latch the signal FIRST so live WS sessions emit
	// `detached{reason:"server_shutdown"}` while the socket is still writable,
	// then give them a moment before the listener tears down.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigCh
		fmt.Fprintf(os.Stderr, "[SERVER] shutting down\n")
		shutdown.Trigger()
		time.Sleep(250 * time.Millisecond)
		_ = srv.Close()
	}()

	if err := srv.Serve(listener); err != nil && err != http.ErrServerClosed {
		log.Fatalf("[SERVER] serve: %v", err)
	}
}

func homeDir() string {
	home, _ := os.UserHomeDir()
	return home
}

func resolvePersistedToken(d *db.Database) string {
	if d == nil || d.Conn == nil {
		return ""
	}
	var v string
	_ = d.Conn.QueryRow(`SELECT value FROM app_metadata WHERE key = ? AND deleted_at IS NULL`, web.WebServiceTokenKey).Scan(&v)
	return v
}

func persistToken(d *db.Database, token string) {
	if d == nil || d.Conn == nil {
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	_, _ = d.Conn.Exec(
		`INSERT INTO app_metadata (key, value, created_at, updated_at) VALUES (?,?,?,?)
		 ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at`,
		web.WebServiceTokenKey, token, now, now)
}

func generateToken() string {
	return strings.ReplaceAll(uuidV4(), "-", "")
}
