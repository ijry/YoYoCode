package handlers

// Git write operations (B8): staging/commit, init, rollback, the stash family,
// and remote push/pull/fetch with credential injection. Non-remote ops shell
// out to git directly; remote ops run with GIT_TERMINAL_PROMPT=0 and, when the
// request carries credentials, a generated askpass helper. Mirrors
// commands/folders.rs git_* write cores.

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"yoyocode/src-go/web"
)

func RegisterGitWrite(reg *Registry) {
	reg.Register("git_add_files", func(c *web.Context) (any, error) {
		path, err := pathParam(c)
		if err != nil {
			return nil, err
		}
		files := GetStringSlice(c, "files")
		args := []string{"add", "--"}
		for _, f := range files {
			args = append(args, toGitLiteralPathspec(f))
		}
		if _, stderr, ok := gitRun(path, args...); !ok {
			return nil, gitCommandError("add", stderr)
		}
		return EmptyResult(), nil
	})

	reg.Register("git_commit", func(c *web.Context) (any, error) {
		path, err := pathParam(c)
		if err != nil {
			return nil, err
		}
		message := GetString(c, "message")
		files := GetStringSlice(c, "files")
		// Stage requested files, skipping ones already staged for deletion.
		staged := gitStagedDeletions(path)
		addArgs := []string{"add", "--"}
		added := 0
		for _, f := range files {
			if staged[f] {
				continue
			}
			addArgs = append(addArgs, toGitLiteralPathspec(f))
			added++
		}
		if added > 0 {
			if _, stderr, ok := gitRun(path, addArgs...); !ok {
				return nil, gitCommandError("add", stderr)
			}
		}
		if _, stderr, ok := gitRun(path, "commit", "-m", message); !ok {
			return nil, gitCommandError("commit", stderr)
		}
		committed := gitCommitFileCount(path)
		if committed == 0 {
			committed = len(files)
		}
		folderID := int64Num(c.Body["folderId"])
		if folderID == 0 {
			folderID = int64Num(c.Body["folder_id"])
		}
		if folderID > 0 {
			Emit(c, "folder://git-commit-succeeded", map[string]any{"folder_id": folderID, "committed_files": committed})
		}
		return map[string]any{"committed_files": committed}, nil
	})

	reg.Register("git_init", func(c *web.Context) (any, error) {
		path, err := pathParam(c)
		if err != nil {
			return nil, err
		}
		if _, stderr, ok := gitRun(path, "init"); !ok {
			return nil, gitCommandError("init", stderr)
		}
		return EmptyResult(), nil
	})

	reg.Register("git_rollback_file", func(c *web.Context) (any, error) {
		path, err := pathParam(c)
		if err != nil {
			return nil, err
		}
		file := strings.TrimSpace(GetString(c, "file"))
		if file == "" {
			return nil, web.ErrInvalidInput("File path cannot be empty")
		}
		lit := toGitLiteralPathspec(file)
		// Restore staged + worktree from HEAD; if the file is untracked, remove it.
		if _, _, ok := gitRun(path, "restore", "--source=HEAD", "--staged", "--worktree", "--", lit); !ok {
			_ = os.Remove(filepath.Join(path, file))
		}
		return EmptyResult(), nil
	})

	// ── stash family ──
	reg.Register("git_stash_push", func(c *web.Context) (any, error) {
		path, err := pathParam(c)
		if err != nil {
			return nil, err
		}
		args := []string{"stash", "push"}
		if GetBool(c, "keepIndex", "keep_index") {
			args = append(args, "--keep-index")
		}
		if msg := GetString(c, "message"); msg != "" {
			args = append(args, "-m", msg)
		}
		if _, stderr, ok := gitRun(path, args...); !ok {
			return nil, gitCommandError("stash push", stderr)
		}
		return EmptyResult(), nil
	})
	reg.Register("git_stash_pop", func(c *web.Context) (any, error) {
		return stashRefOp(c, "pop")
	})
	reg.Register("git_stash_apply", func(c *web.Context) (any, error) {
		return stashRefOp(c, "apply")
	})
	reg.Register("git_stash_drop", func(c *web.Context) (any, error) {
		return stashRefOp(c, "drop")
	})
	reg.Register("git_stash_clear", func(c *web.Context) (any, error) {
		path, err := pathParam(c)
		if err != nil {
			return nil, err
		}
		if _, stderr, ok := gitRun(path, "stash", "clear"); !ok {
			return nil, gitCommandError("stash clear", stderr)
		}
		return EmptyResult(), nil
	})
	reg.Register("git_stash_show", func(c *web.Context) (any, error) {
		path, err := pathParam(c)
		if err != nil {
			return nil, err
		}
		ref := GetString(c, "stashRef", "stash_ref")
		args := []string{"stash", "show", "-p"}
		if ref != "" {
			args = append(args, ref)
		}
		stdout, stderr, ok := gitRun(path, args...)
		if !ok {
			return nil, gitCommandError("stash show", stderr)
		}
		return stdout, nil
	})
	reg.Register("git_stash_list", func(c *web.Context) (any, error) {
		path, err := pathParam(c)
		if err != nil {
			return nil, err
		}
		stdout, stderr, ok := gitRun(path, "stash", "list", "--format=%gd%x00%s%x00%cI")
		if !ok {
			return nil, gitCommandError("stash list", stderr)
		}
		out := []map[string]any{}
		for i, line := range strings.Split(stdout, "\n") {
			if strings.TrimSpace(line) == "" {
				continue
			}
			parts := strings.Split(line, "\x00")
			ref, msg, date := "", "", ""
			if len(parts) > 0 {
				ref = parts[0]
			}
			if len(parts) > 1 {
				msg = parts[1]
			}
			if len(parts) > 2 {
				date = parts[2]
			}
			out = append(out, map[string]any{
				"index": i, "message": msg, "branch": stashBranch(msg), "date": date, "ref_name": ref,
			})
		}
		return out, nil
	})

	// ── remote ops ──
	reg.Register("git_fetch", func(c *web.Context) (any, error) {
		path, err := pathParam(c)
		if err != nil {
			return nil, err
		}
		env := credentialEnv(c)
		if _, stderr, ok := gitRunEnv(path, env, "fetch", "--all", "--prune"); !ok {
			return nil, classifyRemoteError("fetch", stderr)
		}
		return EmptyResult(), nil
	})
	reg.Register("git_fetch_remote", func(c *web.Context) (any, error) {
		path, err := pathParam(c)
		if err != nil {
			return nil, err
		}
		name := GetString(c, "name")
		if name == "" {
			return nil, web.ErrInvalidInput("name required")
		}
		stdout, stderr, ok := gitRunEnv(path, credentialEnv(c), "fetch", name)
		if !ok {
			return nil, classifyRemoteError("fetch", stderr)
		}
		return stdout, nil
	})
	reg.Register("git_pull", func(c *web.Context) (any, error) {
		path, err := pathParam(c)
		if err != nil {
			return nil, err
		}
		before := gitHeadSHA(path)
		_, stderr, ok := gitRunEnv(path, credentialEnv(c), "pull", "--ff")
		if !ok {
			return nil, classifyRemoteError("pull", stderr)
		}
		updated := 0
		if after := gitHeadSHA(path); after != "" && after != before && before != "" {
			updated = gitDiffFileCount(path, before, after)
		}
		return map[string]any{"updated_files": updated, "conflict": nil}, nil
	})
	reg.Register("git_push", func(c *web.Context) (any, error) {
		path, err := pathParam(c)
		if err != nil {
			return nil, err
		}
		remote := GetString(c, "remote")
		if remote == "" {
			remote = "origin"
		}
		branch := strings.TrimSpace(GetString(c, "branch"))
		if branch == "" {
			out, _, _ := gitRun(path, "rev-parse", "--abbrev-ref", "HEAD")
			branch = strings.TrimSpace(out)
		}
		if e := ensurePushableBranchName(branch); e != nil {
			return nil, e
		}
		// Determine whether upstream exists; set it when missing.
		_, _, hasUpstream := gitRun(path, "rev-parse", "--abbrev-ref", "--symbolic-full-name", branch+"@{upstream}")
		pushed := gitUnpushedCount(path, branch)
		args := []string{"push"}
		upstreamSet := false
		if !hasUpstream {
			args = append(args, "-u")
			upstreamSet = true
		}
		args = append(args, remote, "refs/heads/"+branch+":refs/heads/"+branch)
		if _, stderr, ok := gitRunEnv(path, credentialEnv(c), args...); !ok {
			return nil, classifyRemoteError("push", stderr)
		}
		folderID := int64Num(c.Body["folderId"])
		if folderID == 0 {
			folderID = int64Num(c.Body["folder_id"])
		}
		if folderID > 0 {
			Emit(c, "folder://git-push-succeeded", map[string]any{"folder_id": folderID, "pushed_commits": pushed, "upstream_set": upstreamSet})
		}
		return map[string]any{"pushed_commits": pushed, "upstream_set": upstreamSet}, nil
	})
}

func stashRefOp(c *web.Context, op string) (any, error) {
	path, err := pathParam(c)
	if err != nil {
		return nil, err
	}
	args := []string{"stash", op}
	if ref := GetString(c, "stashRef", "stash_ref"); ref != "" {
		args = append(args, ref)
	}
	if _, stderr, ok := gitRun(path, args...); !ok {
		return nil, gitCommandError("stash "+op, stderr)
	}
	return EmptyResult(), nil
}

func gitStagedDeletions(path string) map[string]bool {
	out := map[string]bool{}
	stdout, _, ok := gitRun(path, "diff", "--cached", "--name-only", "--diff-filter=D")
	if !ok {
		return out
	}
	for _, f := range nonEmptyTrimmedLines(stdout) {
		out[f] = true
	}
	return out
}

func gitCommitFileCount(path string) int {
	stdout, _, ok := gitRun(path, "show", "--name-only", "--pretty=format:", "HEAD")
	if !ok {
		return 0
	}
	return len(nonEmptyTrimmedLines(stdout))
}

func gitHeadSHA(path string) string {
	out, _, ok := gitRun(path, "rev-parse", "HEAD")
	if !ok {
		return ""
	}
	return strings.TrimSpace(out)
}

func gitDiffFileCount(path, a, b string) int {
	stdout, _, ok := gitRun(path, "diff", "--name-only", a, b)
	if !ok {
		return 0
	}
	return len(nonEmptyTrimmedLines(stdout))
}

func gitUnpushedCount(path, branch string) int {
	stdout, _, ok := gitRun(path, "rev-list", "--count", branch+"@{upstream}.."+branch)
	if !ok {
		return 0
	}
	n, _ := strconv.Atoi(strings.TrimSpace(stdout))
	return n
}

func stashBranch(message string) string {
	// "WIP on <branch>: ..." or "On <branch>: ...".
	m := message
	for _, prefix := range []string{"WIP on ", "On "} {
		if strings.HasPrefix(m, prefix) {
			rest := m[len(prefix):]
			if i := strings.Index(rest, ":"); i >= 0 {
				return rest[:i]
			}
		}
	}
	return ""
}

// ── credential injection ──

// credentialEnv builds the askpass env when the request carries credentials.
func credentialEnv(c *web.Context) []string {
	env := []string{"GIT_TERMINAL_PROMPT=0"}
	creds, ok := c.Body["credentials"].(map[string]any)
	if !ok {
		return env
	}
	user, _ := creds["username"].(string)
	pass, _ := creds["password"].(string)
	if pass == "" {
		return env
	}
	dataDir := appDataDir(c)
	script, err := ensureAskpass(dataDir)
	if err != nil {
		return env
	}
	env = append(env,
		"GIT_ASKPASS="+script,
		"GIT_CRED_USER="+user,
		"GIT_CRED_PASS="+pass,
	)
	return env
}

func appDataDir(c *web.Context) string {
	if st, ok := appState(c); ok && st != nil && st.DataDir != "" {
		return st.DataDir
	}
	return os.TempDir()
}

// ensureAskpass writes a platform askpass helper that echoes the credential
// from env based on the prompt (Username vs Password) and returns its path.
func ensureAskpass(dataDir string) (string, error) {
	if runtime.GOOS == "windows" {
		p := filepath.Join(dataDir, "yoyocode-askpass.bat")
		body := "@echo off\r\necho %* | findstr /C:\"Username\" >nul\r\nif %errorlevel%==0 (echo %GIT_CRED_USER%) else (echo %GIT_CRED_PASS%)\r\n"
		if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
			return "", err
		}
		return p, nil
	}
	p := filepath.Join(dataDir, "yoyocode-askpass.sh")
	body := "#!/bin/sh\ncase \"$1\" in\n  *Username*) printf '%s\\n' \"$GIT_CRED_USER\" ;;\n  *) printf '%s\\n' \"$GIT_CRED_PASS\" ;;\nesac\n"
	if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
		return "", err
	}
	return p, nil
}

func gitRunEnv(dir string, env []string, args ...string) (string, string, bool) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.String(), stderr.String(), err == nil
}

// classifyRemoteError maps auth-ish failures to authentication_failed, else
// external_command (mirrors classify_remote_git_error).
func classifyRemoteError(op, stderr string) error {
	low := strings.ToLower(stderr)
	if strings.Contains(low, "authentication failed") || strings.Contains(low, "could not read username") ||
		strings.Contains(low, "permission denied") || strings.Contains(low, "invalid username or password") ||
		strings.Contains(low, "403") {
		return web.ErrAuthFailed("git " + op + " authentication failed").WithDetail(strings.TrimSpace(stderr))
	}
	return web.ErrExternalCommand("git " + op + " failed").WithDetail(strings.TrimSpace(stderr))
}
