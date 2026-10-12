package handlers

// clone_repository (upstream commands/folders.rs: clone_repository_core). Shells
// out to `git clone <url> <target>`, injecting the askpass credential helper
// when the request carries credentials, and maps git's stderr onto typed errors.

import (
	"os"
	"path/filepath"
	"strings"

	"yoyocode/src-go/web"
)

// RegisterGitClone wires clone_repository.
func RegisterGitClone(reg *Registry) {
	reg.Register("clone_repository", func(c *web.Context) (any, error) {
		url := strings.TrimSpace(GetString(c, "url"))
		target := strings.TrimSpace(GetString(c, "targetDir", "target_dir"))
		if url == "" || target == "" {
			return nil, web.ErrInvalidInput("Repository URL and target directory are required")
		}
		// Run from an existing directory so the git subprocess has a valid cwd;
		// the absolute target drives where the clone lands.
		workdir := filepath.Dir(target)
		if fi, err := os.Stat(workdir); err != nil || !fi.IsDir() {
			workdir = os.TempDir()
		}
		_, stderr, ok := gitRunEnv(workdir, credentialEnv(c), "clone", url, target)
		if !ok {
			return nil, classifyGitCloneError(stderr)
		}
		return EmptyResult(), nil
	})
}

// classifyGitCloneError mirrors classify_git_clone_error.
func classifyGitCloneError(stderr string) error {
	s := strings.TrimSpace(stderr)
	n := strings.ToLower(s)
	switch {
	case strings.Contains(n, "already exists and is not an empty directory"):
		return web.ErrAlreadyExists("Target directory already exists and is not empty").WithDetail(s)
	case strings.Contains(n, "repository not found"):
		return web.ErrNotFound("Repository not found. Check URL and access permissions.").WithDetail(s)
	case strings.Contains(n, "could not resolve host"),
		strings.Contains(n, "network is unreachable"),
		strings.Contains(n, "connection timed out"),
		strings.Contains(n, "failed to connect"):
		return web.ErrNetwork("Network is unavailable while cloning repository").WithDetail(s)
	case strings.Contains(n, "authentication failed"),
		strings.Contains(n, "could not read username"),
		strings.Contains(n, "could not read password"),
		strings.Contains(n, "logon failed"),
		strings.Contains(n, "terminal prompts disabled"),
		strings.Contains(n, "the requested url returned error: 401"),
		strings.Contains(n, "the requested url returned error: 403"),
		strings.Contains(n, "http basic: access denied"),
		strings.Contains(n, "permission denied (publickey)"):
		return web.ErrAuthFailed("Authentication failed while cloning repository").WithDetail(s)
	case strings.Contains(n, "permission denied"):
		return web.ErrPermissionDenied("Permission denied while cloning repository").WithDetail(s)
	default:
		return web.ErrExternalCommand("Git clone failed").WithDetail(s)
	}
}
