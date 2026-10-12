package handlers

// Settings → Logs viewer backend (B11, logging module), mirroring
// commands/logging.rs + web/handlers/logging.rs: persisted capture settings,
// the on-disk rotated log files list, and a bounded tail read.
//
// Fidelity note: get_recent_logs reads an in-memory tracing ring buffer that
// only the desktop/Rust subscriber populates. The Go server has no such hub, so
// it returns an empty slice (an honest "no buffered records") rather than a
// fabricated one; the on-disk files remain fully readable, which is what a
// forensic download needs. set_log_settings persists the choice and broadcasts
// the change event; there is no live reload handle to re-point here.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"yoyocode/src-go/db"
	"yoyocode/src-go/web"
)

const loggingLevelKey = "logging.level"
const logSettingsChangedEvent = "log-settings://changed"
const readLogMaxBytes = 16 * 1024 * 1024

var validLogLevels = map[string]bool{
	"off": true, "error": true, "warn": true, "info": true, "debug": true, "trace": true,
}

func RegisterLogging(reg *Registry) {
	reg.Register("get_log_settings", func(c *web.Context) (any, error) {
		level, targets := readLogSettings(stateDB(c))
		return map[string]any{
			"level":      level,
			"targets":    targets,
			"env_locked": envLevelIsSet(),
		}, nil
	})
	reg.Register("set_log_settings", setLogSettings)
	reg.Register("get_recent_logs", func(c *web.Context) (any, error) {
		// No in-memory hub in the Go server; the live tail is desktop-only.
		return []any{}, nil
	})
	reg.Register("list_log_files", func(c *web.Context) (any, error) {
		return listLogFiles(logsRoot(c)), nil
	})
	reg.Register("read_log_file", func(c *web.Context) (any, error) {
		name, err := GetStringOrErr(c, "name")
		if err != nil {
			return nil, err
		}
		var maxBytes int
		if m := GetInt(c, "maxBytes", "max_bytes"); m > 0 {
			maxBytes = int(m)
		}
		return readLogFile(logsRoot(c), name, maxBytes)
	})
}

// logsRoot resolves the logs dir, mirroring paths.rs yoyocode_logs_root:
// YOYOCODE_HOME/logs, then YOYOCODE_DATA_DIR/logs, then AppState.DataDir/logs, then
// ~/.yoyocode/logs.
func logsRoot(c *web.Context) string {
	if custom := os.Getenv("YOYOCODE_HOME"); custom != "" {
		return filepath.Join(custom, "logs")
	}
	if data := os.Getenv("YOYOCODE_DATA_DIR"); data != "" {
		return filepath.Join(data, "logs")
	}
	if st, ok := appState(c); ok && st != nil && st.DataDir != "" {
		return filepath.Join(st.DataDir, "logs")
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".yoyocode", "logs")
	}
	return filepath.Join(".yoyocode", "logs")
}

func envLevelIsSet() bool {
	return strings.TrimSpace(os.Getenv("YOYOCODE_LOG")) != "" || strings.TrimSpace(os.Getenv("RUST_LOG")) != ""
}

// readLogSettings returns the persisted level (default "info") and target
// overrides (default empty) from app_metadata.
func readLogSettings(d *db.Database) (string, []any) {
	level := "info"
	targets := []any{}
	if d == nil || d.Conn == nil {
		return level, targets
	}
	value := ""
	_ = d.Conn.QueryRow(
		`SELECT value FROM app_metadata WHERE key = ? AND deleted_at IS NULL`, loggingLevelKey,
	).Scan(&value)
	if value == "" {
		return level, targets
	}
	parsed := map[string]any{}
	if err := JSONUnmarshalFirst(value, &parsed); err != nil {
		return level, targets
	}
	if l, ok := parsed["level"].(string); ok && validLogLevels[l] {
		level = l
	}
	if arr, ok := parsed["targets"].([]any); ok {
		targets = arr
	}
	return level, targets
}

func setLogSettings(c *web.Context) (any, error) {
	raw, ok := c.Body["settings"].(map[string]any)
	if !ok {
		return nil, web.ErrInvalidInput("settings object is required")
	}
	level, _ := raw["level"].(string)
	if !validLogLevels[level] {
		return nil, web.ErrInvalidInput("invalid log level '" + level + "'")
	}
	// Validate and normalize targets: [{target, level}] with valid levels.
	normTargets := []any{}
	if arr, ok := raw["targets"].([]any); ok {
		for _, t := range arr {
			obj, ok := t.(map[string]any)
			if !ok {
				continue
			}
			tgt, _ := obj["target"].(string)
			lvl, _ := obj["level"].(string)
			if strings.TrimSpace(tgt) == "" || !validLogLevels[lvl] {
				return nil, web.ErrInvalidInput("invalid target directive")
			}
			normTargets = append(normTargets, map[string]any{"target": tgt, "level": lvl})
		}
	}
	// Build the stored/returned LogSettings (targets omitted when empty, like
	// upstream's skip_serializing_if).
	settings := map[string]any{"level": level}
	if len(normTargets) > 0 {
		settings["targets"] = normTargets
	}
	d := stateDB(c)
	if d != nil && d.Conn != nil {
		serializedBytes, _ := json.Marshal(settings)
		serialized := string(serializedBytes)
		now := nowRFC3339()
		if _, err := d.Conn.Exec(
			`INSERT INTO app_metadata (key, value, created_at, updated_at) VALUES (?,?,?,?)
			 ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at`,
			loggingLevelKey, serialized, now, now,
		); err != nil {
			return nil, web.ErrIO(err.Error())
		}
	}
	Emit(c, logSettingsChangedEvent, settings)
	return settings, nil
}

// listLogFiles returns metadata for every .log file in the dir, newest first.
func listLogFiles(dir string) []map[string]any {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return []map[string]any{}
	}
	out := []map[string]any{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".log") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, map[string]any{
			"name":        e.Name(),
			"size_bytes":  info.Size(),
			"modified_ms": info.ModTime().UnixMilli(),
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i]["modified_ms"].(int64) > out[j]["modified_ms"].(int64)
	})
	return out
}

// readLogFile reads the newest max_bytes of a bare .log file in the logs dir,
// rejecting traversal and non-log names. Logs are append-only so the tail is
// the most recent slice.
func readLogFile(dir, name string, maxBytes int) (any, error) {
	if name == "" || strings.ContainsAny(name, "/\\\x00") || strings.Contains(name, "..") {
		return nil, web.ErrInvalidInput("Invalid log file name")
	}
	if !strings.HasSuffix(name, ".log") {
		return nil, web.ErrInvalidInput("Not a log file")
	}
	path := filepath.Join(dir, name)
	if filepath.Dir(path) != filepath.Clean(dir) {
		return nil, web.ErrInvalidInput("Log file escapes the log directory")
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, web.ErrNotFound("Log file not found")
	}
	limit := readLogMaxBytes
	if maxBytes > 0 && maxBytes < limit {
		limit = maxBytes
	}
	size := int(info.Size())
	start := 0
	if size > limit {
		start = size - limit
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, web.ErrIO("Failed to read log file")
	}
	defer f.Close()
	if start > 0 {
		if _, err := f.Seek(int64(start), 0); err != nil {
			return nil, web.ErrIO("Failed to seek log file")
		}
	}
	buf := make([]byte, size-start)
	n, _ := f.Read(buf)
	return string(buf[:n]), nil
}
