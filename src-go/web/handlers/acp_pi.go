package handlers

// acp_pi.go ports the pi-agent config/probe family. It starts with
// acp_validate_pi_command (BYO-pi binary validation): resolve the user-supplied
// command — a path, or a bare name looked up on PATH — and probe
// `<resolved> --version`.

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"yoyocode/src-go/web"
)

// RegisterACPPi wires the pi-agent config/probe commands.
func RegisterACPPi(reg *Registry) {
	// acp_validate_pi_command: `found=false` with resolvedPath=null is a normal
	// result (the panel shows "not found"), not an error.
	reg.Register("acp_validate_pi_command", func(c *web.Context) (any, error) {
		resolved, ok := resolvePiCommandPath(strVal(c.Body["command"]))
		if !ok {
			return map[string]any{"found": false, "resolvedPath": nil, "version": nil}, nil
		}
		var version any
		if v := probePiVersion(resolved); v != "" {
			version = v
		}
		return map[string]any{"found": true, "resolvedPath": resolved, "version": version}, nil
	})
}

// resolvePiCommandPath mirrors upstream resolve_pi_command_path: a command that
// looks like a path is checked directly (and made absolute); otherwise it is
// looked up on PATH.
func resolvePiCommandPath(command string) (string, bool) {
	trimmed := strings.TrimSpace(command)
	if trimmed == "" {
		return "", false
	}
	looksLikePath := strings.ContainsRune(trimmed, os.PathSeparator) ||
		strings.ContainsRune(trimmed, '/') ||
		filepath.IsAbs(trimmed)
	if looksLikePath {
		if !piPathIsExecutable(trimmed) {
			return "", false
		}
		if abs, err := filepath.Abs(trimmed); err == nil {
			return abs, true
		}
		return trimmed, true
	}
	p, err := exec.LookPath(trimmed)
	if err != nil {
		return "", false
	}
	return p, true
}

// piPathIsExecutable reports whether path exists as a file and, on unix, carries
// any execute bit; on Windows existence is enough.
func piPathIsExecutable(path string) bool {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	if runtime.GOOS == "windows" {
		return true
	}
	return info.Mode().Perm()&0o111 != 0
}

// probePiVersion runs `<resolved> --version` and returns the trimmed first
// non-empty stdout line, or "" on any failure.
func probePiVersion(resolved string) string {
	out, err := exec.Command(resolved, "--version").Output()
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		if t := strings.TrimSpace(line); t != "" {
			return t
		}
	}
	return ""
}
