package handlers

// Mirror upstream git handlers (web/handlers/git.rs + commands/folders.rs git_*).
// Everything shells out to the system git binary, exactly as upstream does, so
// output shapes match the client parsers. Read + simple branch ops land here;
// push/commit/pull/fetch (credential handling) come in a later batch.

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"yoyocode/src-go/web"
)

// gitRun executes git in dir and returns (stdout, stderr, success).
func gitRun(dir string, args ...string) (string, string, bool) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.String(), stderr.String(), err == nil
}

func gitCommandError(op, stderr string) error {
	return web.ErrExternalCommand("git " + op + " failed").WithDetail(strings.TrimSpace(stderr))
}

// ensureGitRepo mirrors git_repo::ensure_git_repo — a strict, non-ancestor check
// that the exact path holds a .git entry, returning typed errors.
func ensureGitRepo(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return web.ErrNotFound("Workspace path does not exist: " + path)
		}
		if os.IsPermission(err) {
			return web.ErrPermissionDenied("Cannot access workspace path: " + path)
		}
		return web.ErrIO(err.Error())
	}
	if !info.IsDir() {
		return web.ErrInvalidInput("Workspace path is not a directory: " + path)
	}
	if _, err := os.Stat(filepath.Join(path, ".git")); err != nil {
		if os.IsNotExist(err) {
			return web.ErrNotAGitRepository("Not a Git repository: " + path)
		}
		if os.IsPermission(err) {
			return web.ErrPermissionDenied("Cannot access .git in: " + path)
		}
		return web.ErrIO(err.Error())
	}
	return nil
}

func unquoteGitPath(path string) string {
	t := strings.TrimSpace(path)
	if len(t) >= 2 && strings.HasPrefix(t, `"`) && strings.HasSuffix(t, `"`) {
		return t[1 : len(t)-1]
	}
	return t
}

func toGitLiteralPathspec(path string) string { return ":(literal)" + path }

func pathParam(c *web.Context) (string, error) {
	path := GetString(c, "path")
	if path == "" {
		return "", web.ErrInvalidInput("path required")
	}
	return path, nil
}

// RegisterGit wires the git read + simple branch endpoints.
func RegisterGit(reg *Registry) {
	reg.Register("get_git_head", func(c *web.Context) (any, error) {
		path, err := pathParam(c)
		if err != nil {
			return nil, err
		}
		return resolveGitHead(path), nil
	})
	reg.Register("get_git_branch", func(c *web.Context) (any, error) {
		path, err := pathParam(c)
		if err != nil {
			return nil, err
		}
		head := resolveGitHead(path)
		if head["branch"] == nil {
			return nil, nil
		}
		return head["branch"], nil
	})

	reg.Register("git_status", func(c *web.Context) (any, error) {
		path, err := pathParam(c)
		if err != nil {
			return nil, err
		}
		if err := ensureGitRepo(path); err != nil {
			return nil, err
		}
		untracked := "-unormal"
		if GetBool(c, "showAllUntracked", "show_all_untracked") {
			untracked = "-uall"
		}
		stdout, stderr, ok := gitRun(path, "--no-optional-locks", "-c", "core.quotePath=false", "status", "--porcelain=v1", untracked)
		if !ok {
			return nil, gitCommandError("status", stderr)
		}
		entries := []map[string]any{}
		for _, line := range strings.Split(stdout, "\n") {
			if len(line) < 3 {
				continue
			}
			entries = append(entries, map[string]any{
				"status": strings.TrimSpace(line[:2]),
				"file":   unquoteGitPath(line[3:]),
			})
		}
		return entries, nil
	})

	reg.Register("git_diff", func(c *web.Context) (any, error) {
		path, err := pathParam(c)
		if err != nil {
			return nil, err
		}
		if err := ensureGitRepo(path); err != nil {
			return nil, err
		}
		args := []string{"diff", "HEAD"}
		if f := GetString(c, "file"); f != "" {
			args = append(args, "--", toGitLiteralPathspec(f))
		}
		stdout, _, ok := gitRun(path, args...)
		if !ok {
			fb := []string{"diff", "--cached"}
			if f := GetString(c, "file"); f != "" {
				fb = append(fb, "--", toGitLiteralPathspec(f))
			}
			out, _, _ := gitRun(path, fb...)
			return out, nil
		}
		return stdout, nil
	})

	reg.Register("git_diff_with_branch", func(c *web.Context) (any, error) {
		path, err := pathParam(c)
		if err != nil {
			return nil, err
		}
		if err := ensureGitRepo(path); err != nil {
			return nil, err
		}
		branch := strings.TrimSpace(GetString(c, "branch"))
		if branch == "" {
			return nil, web.ErrInvalidInput("Branch name cannot be empty")
		}
		args := []string{"diff", "--no-color", branch}
		if f := GetString(c, "file"); f != "" {
			args = append(args, "--", toGitLiteralPathspec(f))
		}
		stdout, stderr, ok := gitRun(path, args...)
		if !ok {
			return nil, web.ErrExternalCommand("git diff failed").WithDetail("branch=" + branch + "; " + strings.TrimSpace(stderr))
		}
		return stdout, nil
	})

	reg.Register("git_show_diff", func(c *web.Context) (any, error) {
		path, err := pathParam(c)
		if err != nil {
			return nil, err
		}
		if err := ensureGitRepo(path); err != nil {
			return nil, err
		}
		commit := GetString(c, "commit")
		args := []string{"show", "--no-color", "--format=", commit}
		if f := GetString(c, "file"); f != "" {
			args = append(args, "--", toGitLiteralPathspec(f))
		}
		stdout, stderr, ok := gitRun(path, args...)
		if !ok {
			return nil, gitCommandError("show", stderr)
		}
		return stdout, nil
	})

	reg.Register("git_show_file", func(c *web.Context) (any, error) {
		path, err := pathParam(c)
		if err != nil {
			return nil, err
		}
		if err := ensureGitRepo(path); err != nil {
			return nil, err
		}
		file := GetString(c, "file")
		ref := GetString(c, "refName", "ref_name")
		if ref == "" {
			ref = "HEAD"
		}
		spec := ref + ":" + file
		stdout, _, ok := gitRun(path, "show", spec)
		if !ok {
			return "", nil
		}
		b := []byte(stdout)
		lim := len(b)
		if lim > 2048 {
			lim = 2048
		}
		for i := 0; i < lim; i++ {
			if b[i] == 0 {
				return nil, web.ErrInvalidInput("Binary files are not supported").WithDetail(spec)
			}
		}
		return stdout, nil
	})

	reg.Register("git_is_tracked", func(c *web.Context) (any, error) {
		path, err := pathParam(c)
		if err != nil {
			return nil, err
		}
		file := GetString(c, "file")
		_, _, ok := gitRun(path, "ls-files", "--error-unmatch", "--", toGitLiteralPathspec(file))
		return ok, nil
	})

	reg.Register("git_list_branches", func(c *web.Context) (any, error) {
		path, err := pathParam(c)
		if err != nil {
			return nil, err
		}
		if err := ensureGitRepo(path); err != nil {
			return nil, err
		}
		stdout, stderr, ok := gitRun(path, "branch", "--format=%(refname:short)")
		if !ok {
			return nil, gitCommandError("branch", stderr)
		}
		return nonEmptyTrimmedLines(stdout), nil
	})

	reg.Register("git_list_all_branches", func(c *web.Context) (any, error) {
		path, err := pathParam(c)
		if err != nil {
			return nil, err
		}
		if err := ensureGitRepo(path); err != nil {
			return nil, err
		}
		localOut, localErr, ok := gitRun(path, "branch", "--format=%(refname:short)")
		if !ok {
			return nil, gitCommandError("branch", localErr)
		}
		local := nonEmptyTrimmedLines(localOut)

		remote := []string{}
		if remoteOut, _, rok := gitRun(path, "branch", "-r", "--format=%(refname:short)"); rok {
			for _, l := range nonEmptyTrimmedLines(remoteOut) {
				if !strings.Contains(l, "HEAD") && strings.Contains(l, "/") {
					remote = append(remote, l)
				}
			}
		}

		worktreeBranches := []string{}
		var mainWorktreeBranch any = nil
		if wtOut, _, wok := gitRun(path, "worktree", "list", "--porcelain"); wok {
			canonical := canonicalPath(path)
			for index, pr := range parseWorktrees(wtOut) {
				if pr.branch == "" {
					continue
				}
				if canonicalPath(pr.path) == canonical {
					continue
				}
				if index == 0 {
					mainWorktreeBranch = pr.branch
				}
				worktreeBranches = append(worktreeBranches, pr.branch)
			}
		}

		return map[string]any{
			"local":                local,
			"remote":               remote,
			"worktree_branches":    worktreeBranches,
			"main_worktree_branch": mainWorktreeBranch,
		}, nil
	})

	reg.Register("git_commit_branches", func(c *web.Context) (any, error) {
		path, err := pathParam(c)
		if err != nil {
			return nil, err
		}
		if err := ensureGitRepo(path); err != nil {
			return nil, err
		}
		commit := GetString(c, "commit")
		stdout, stderr, ok := gitRun(path, "for-each-ref", "--contains="+commit, "--format=%(refname:short)", "refs/heads", "refs/remotes")
		if !ok {
			return nil, gitCommandError("for-each-ref", stderr)
		}
		seen := map[string]bool{}
		branches := []string{}
		for _, line := range strings.Split(stdout, "\n") {
			b := strings.TrimSpace(line)
			if b == "" || strings.HasSuffix(b, "/HEAD") {
				continue
			}
			if !seen[b] {
				seen[b] = true
				branches = append(branches, b)
			}
		}
		sort.Strings(branches)
		return branches, nil
	})

	reg.Register("git_list_remotes", func(c *web.Context) (any, error) {
		path, err := pathParam(c)
		if err != nil {
			return nil, err
		}
		return gitListRemotes(path)
	})

	reg.Register("git_push_info", func(c *web.Context) (any, error) {
		path, err := pathParam(c)
		if err != nil {
			return nil, err
		}
		if err := ensureGitRepo(path); err != nil {
			return nil, err
		}
		branch := strings.TrimSpace(GetString(c, "branch"))
		if branch != "" {
			if e := ensurePushableBranchName(branch); e != nil {
				return nil, e
			}
		} else {
			out, _, _ := gitRun(path, "rev-parse", "--abbrev-ref", "HEAD")
			branch = strings.TrimSpace(out)
		}
		var trackingRemote any = nil
		if out, _, ok := gitRun(path, "config", "--get", "branch."+branch+".remote"); ok {
			if v := strings.TrimSpace(out); v != "" {
				trackingRemote = v
			}
		}
		remotes, e := gitListRemotes(path)
		if e != nil {
			return nil, e
		}
		return map[string]any{
			"branch":          branch,
			"remotes":         remotes,
			"tracking_remote": trackingRemote,
		}, nil
	})

	reg.Register("git_log", func(c *web.Context) (any, error) {
		return gitLog(c)
	})

	reg.Register("git_new_branch", func(c *web.Context) (any, error) {
		path, err := pathParam(c)
		if err != nil {
			return nil, err
		}
		branchName := GetString(c, "branchName", "branch_name")
		args := []string{"checkout", "-b", branchName}
		if sp := strings.TrimSpace(GetString(c, "startPoint", "start_point")); sp != "" {
			args = append(args, sp)
		}
		_, stderr, ok := gitRun(path, args...)
		if !ok {
			return nil, gitCommandError("checkout -b", stderr)
		}
		return EmptyResult(), nil
	})

	reg.Register("git_checkout", func(c *web.Context) (any, error) {
		path, err := pathParam(c)
		if err != nil {
			return nil, err
		}
		branchName := GetString(c, "branchName", "branch_name")
		_, stderr, ok := gitRun(path, "checkout", branchName)
		if !ok {
			return nil, gitCommandError("checkout", stderr)
		}
		return EmptyResult(), nil
	})

	reg.Register("git_reset", func(c *web.Context) (any, error) {
		path, err := pathParam(c)
		if err != nil {
			return nil, err
		}
		mode := strings.ToLower(strings.TrimSpace(GetString(c, "mode")))
		switch mode {
		case "soft", "mixed", "hard", "keep":
		default:
			return nil, web.ErrInvalidInput("Reset mode must be one of: soft, mixed, hard, keep")
		}
		commit := GetString(c, "commit")
		_, stderr, ok := gitRun(path, "reset", "--"+mode, commit)
		if !ok {
			return nil, gitCommandError("reset", stderr)
		}
		return EmptyResult(), nil
	})
}

// ─── helpers ────────────────────────────────────────────────────────────────

func nonEmptyTrimmedLines(s string) []string {
	out := []string{}
	for _, l := range strings.Split(s, "\n") {
		t := strings.TrimSpace(l)
		if t != "" {
			out = append(out, t)
		}
	}
	return out
}

func canonicalPath(p string) string {
	if abs, err := filepath.Abs(p); err == nil {
		if resolved, err := filepath.EvalSymlinks(abs); err == nil {
			return resolved
		}
		return abs
	}
	return p
}

type worktreePair struct {
	path   string
	branch string
}

func parseWorktrees(stdout string) []worktreePair {
	entries := []worktreePair{}
	var curPath, curBranch string
	havePath := false
	flush := func() {
		if havePath {
			entries = append(entries, worktreePair{curPath, curBranch})
		}
		curPath, curBranch, havePath = "", "", false
	}
	for _, line := range strings.Split(stdout, "\n") {
		line = strings.TrimRight(line, "\r")
		if p, ok := strings.CutPrefix(line, "worktree "); ok {
			flush()
			curPath = strings.TrimSpace(p)
			havePath = true
		} else if b, ok := strings.CutPrefix(line, "branch refs/heads/"); ok {
			curBranch = strings.TrimSpace(b)
		} else if line == "" {
			flush()
		}
	}
	flush()
	return entries
}

func gitListRemotes(path string) ([]map[string]any, error) {
	stdout, stderr, ok := gitRun(path, "remote", "-v")
	if !ok {
		return nil, gitCommandError("remote", stderr)
	}
	seen := map[string]bool{}
	remotes := []map[string]any{}
	for _, line := range strings.Split(stdout, "\n") {
		if line == "" {
			continue
		}
		name, rest, found := strings.Cut(line, "\t")
		if !found {
			continue
		}
		url := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(rest), "(fetch)"))
		url = strings.TrimSpace(strings.TrimSuffix(url, "(push)"))
		if !seen[name] {
			seen[name] = true
			remotes = append(remotes, map[string]any{"name": name, "url": url})
		}
	}
	return remotes, nil
}

func ensurePushableBranchName(branch string) error {
	rejected := branch == "" || strings.HasPrefix(branch, "-") || strings.HasPrefix(branch, "+") || strings.Contains(branch, ":")
	if !rejected {
		for _, r := range branch {
			if r == ' ' || r == '\t' || r == '\n' || r == '\r' || r < 0x20 {
				rejected = true
				break
			}
		}
	}
	if rejected {
		return web.ErrInvalidInput("Invalid branch name").WithDetail("branch=" + branch)
	}
	return nil
}

func resolveGitHead(path string) map[string]any {
	notRepo := map[string]any{"is_repo": false, "branch": nil, "detached": false, "short_sha": nil}
	if out, _, ok := gitRun(path, "rev-parse", "--abbrev-ref", "HEAD"); ok {
		name := strings.TrimSpace(out)
		if name != "" && name != "HEAD" {
			return map[string]any{"is_repo": true, "branch": name, "detached": false, "short_sha": nil}
		}
		var shortSha any = nil
		if so, _, sok := gitRun(path, "rev-parse", "--short", "HEAD"); sok {
			if v := strings.TrimSpace(so); v != "" {
				shortSha = v
			}
		}
		return map[string]any{"is_repo": true, "branch": nil, "detached": true, "short_sha": shortSha}
	}
	if out, _, ok := gitRun(path, "symbolic-ref", "--short", "HEAD"); ok {
		if name := strings.TrimSpace(out); name != "" {
			return map[string]any{"is_repo": true, "branch": name, "detached": false, "short_sha": nil}
		}
	}
	if _, _, ok := gitRun(path, "rev-parse", "--is-inside-work-tree"); ok {
		return map[string]any{"is_repo": true, "branch": nil, "detached": false, "short_sha": nil}
	}
	return notRepo
}

// ─── git_log (format + parse + push status) ─────────────────────────────────

func gitLog(c *web.Context) (any, error) {
	path, err := pathParam(c)
	if err != nil {
		return nil, err
	}
	if err := ensureGitRepo(path); err != nil {
		return nil, err
	}
	const commitMetaPrefix = "__COMMIT__\x00"
	const messageEndMarker = "__COMMIT_MESSAGE_END__"

	allBranches := GetBool(c, "allBranches", "all_branches")
	withFiles := true
	if v, ok := c.Body["withFiles"]; ok {
		withFiles = asBool(v)
	} else if v, ok := c.Body["with_files"]; ok {
		withFiles = asBool(v)
	}
	skip := GetInt(c, "skip")
	limit := GetInt(c, "limit")
	if limit <= 0 {
		limit = 100
	}
	branch := GetString(c, "branch")
	remote := GetString(c, "remote")
	author := GetString(c, "author")

	args := []string{"-c", "core.quotePath=false", "log", "-" + strconv.FormatInt(limit, 10),
		"--format=__COMMIT__%x00%h%x00%H%x00%an%x00%aI%n%B%n" + messageEndMarker}
	if withFiles {
		args = append(args, "--raw", "--numstat", "--no-renames")
	}
	if skip > 0 {
		args = append(args, "--skip="+strconv.FormatInt(skip, 10))
	}
	if author != "" {
		args = append(args, "--basic-regexp", "--author="+gitAuthorMatchPattern(author))
	}
	if allBranches {
		args = append(args, "--all")
	} else if branch != "" {
		args = append(args, branch)
	}

	stdout, stderr, ok := gitRun(path, args...)
	if !ok {
		if strings.Contains(stderr, "does not have any commits yet") ||
			strings.Contains(stderr, "unknown revision or path not in the working tree") {
			return map[string]any{"entries": []any{}, "has_upstream": false}, nil
		}
		return nil, gitCommandError("log", stderr)
	}

	type fileChange struct {
		path      string
		status    string
		additions int64
		deletions int64
	}
	type entry struct {
		hash, fullHash, author, date, message string
		fileOrder                             []string
		files                                 map[string]*fileChange
	}
	entries := []*entry{}
	var cur *entry
	readingMsg := false
	getFile := func(e *entry, p string) *fileChange {
		if e.files == nil {
			e.files = map[string]*fileChange{}
		}
		if f, ok := e.files[p]; ok {
			return f
		}
		f := &fileChange{path: p}
		e.files[p] = f
		e.fileOrder = append(e.fileOrder, p)
		return f
	}

	for _, line := range strings.Split(stdout, "\n") {
		line = strings.TrimRight(line, "\r")
		if meta, ok := strings.CutPrefix(line, commitMetaPrefix); ok {
			if cur != nil {
				cur.message = strings.TrimRight(cur.message, "\n")
				entries = append(entries, cur)
			}
			parts := strings.SplitN(meta, "\x00", 4)
			if len(parts) == 4 {
				cur = &entry{hash: parts[0], fullHash: parts[1], author: parts[2], date: parts[3]}
				readingMsg = true
			} else {
				cur = nil
				readingMsg = false
			}
			continue
		}
		if cur == nil {
			continue
		}
		if readingMsg {
			if line == messageEndMarker {
				readingMsg = false
				cur.message = strings.TrimRight(cur.message, "\n")
			} else {
				if cur.message != "" {
					cur.message += "\n"
				}
				cur.message += line
			}
			continue
		}
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, ":") {
			if st, fp, ok := parseRawFileLine(line); ok {
				getFile(cur, fp).status = st
			}
			continue
		}
		if add, del, fp, ok := parseNumstatFileLine(line); ok {
			f := getFile(cur, fp)
			f.additions = add
			f.deletions = del
		}
	}
	if cur != nil {
		cur.message = strings.TrimRight(cur.message, "\n")
		entries = append(entries, cur)
	}

	logLimit := skip + limit
	unpushed, hasUpstream := getUnpushedHashes(path, logLimit, remote, branch, allBranches, author)

	out := make([]map[string]any, 0, len(entries))
	for _, e := range entries {
		files := make([]map[string]any, 0, len(e.fileOrder))
		for _, fp := range e.fileOrder {
			f := e.files[fp]
			st := f.status
			if st == "" {
				st = "M"
			}
			files = append(files, map[string]any{
				"path": f.path, "status": st, "additions": f.additions, "deletions": f.deletions,
			})
		}
		var pushed any = nil
		if unpushed != nil {
			pushed = !unpushed[e.fullHash]
		}
		out = append(out, map[string]any{
			"hash": e.hash, "full_hash": e.fullHash, "author": e.author,
			"date": e.date, "message": e.message, "files": files, "pushed": pushed,
		})
	}
	return map[string]any{"entries": out, "has_upstream": hasUpstream}, nil
}

func parseRawFileLine(line string) (string, string, bool) {
	parts := strings.Split(line, "\t")
	if len(parts) < 2 {
		return "", "", false
	}
	meta := parts[0]
	file := unquoteGitPath(parts[1])
	fields := strings.Fields(meta)
	status := "M"
	if len(fields) > 0 {
		last := fields[len(fields)-1]
		if len(last) > 0 {
			status = string([]rune(last)[0])
		}
	}
	return status, file, true
}

func parseNumstatFileLine(line string) (int64, int64, string, bool) {
	parts := strings.SplitN(line, "\t", 3)
	if len(parts) < 3 {
		return 0, 0, "", false
	}
	return parseNumstatCount(parts[0]), parseNumstatCount(parts[1]), unquoteGitPath(parts[2]), true
}

func parseNumstatCount(v string) int64 {
	if v == "-" {
		return 0
	}
	n, _ := strconv.ParseInt(v, 10, 64)
	return n
}

func gitAuthorMatchPattern(name string) string {
	var b strings.Builder
	for _, ch := range name {
		switch ch {
		case '\\', '.', '*', '[', ']', '^', '$':
			b.WriteRune('\\')
		}
		b.WriteRune(ch)
	}
	return "^" + b.String() + " <"
}

func getUnpushedHashes(path string, limit int64, remoteOverride, branch string, allBranches bool, author string) (map[string]bool, bool) {
	limitArg := "-" + strconv.FormatInt(limit, 10)
	if branch == "HEAD" {
		branch = ""
	}

	if allBranches {
		hasRemotes := false
		if out, _, ok := gitRun(path, "for-each-ref", "--count=1", "refs/remotes"); ok && strings.TrimSpace(out) != "" {
			hasRemotes = true
		}
		revArgs := []string{"rev-list", limitArg}
		if author != "" {
			revArgs = append(revArgs, "--basic-regexp", "--author="+gitAuthorMatchPattern(author))
		}
		revArgs = append(revArgs, "--all", "--not", "--remotes")
		out, _, ok := gitRun(path, revArgs...)
		if !ok {
			return nil, hasRemotes
		}
		return hashSet(out), hasRemotes
	}

	if branch != "" {
		if _, _, ok := gitRun(path, "rev-parse", "--verify", "--quiet", "refs/remotes/"+branch); ok {
			return map[string]bool{}, true
		}
	}

	localRef := branch
	if localRef == "" {
		localRef = "HEAD"
	}
	upstreamArg := "@{upstream}"
	if branch != "" {
		upstreamArg = localRef + "@{upstream}"
	}
	upOut, _, upOk := gitRun(path, "rev-parse", "--abbrev-ref", "--symbolic-full-name", upstreamArg)
	hasUpstream := upOk && strings.TrimSpace(upOut) != ""

	var revOut string
	var revOk bool
	if hasUpstream && remoteOverride == "" {
		upstream := strings.TrimSpace(upOut)
		revOut, _, revOk = gitRun(path, "rev-list", limitArg, upstream+".."+localRef)
	} else {
		branchName := branch
		if branchName == "" {
			bo, _, bok := gitRun(path, "rev-parse", "--abbrev-ref", "HEAD")
			if !bok {
				return nil, hasUpstream
			}
			branchName = strings.TrimSpace(bo)
			if branchName == "" || branchName == "HEAD" {
				return nil, hasUpstream
			}
		}
		rem := remoteOverride
		if rem == "" {
			if ro, _, rok := gitRun(path, "config", "--get", "branch."+branchName+".remote"); rok && strings.TrimSpace(ro) != "" {
				rem = strings.TrimSpace(ro)
			} else {
				rem = "origin"
			}
		}
		remoteBranchRef := "refs/remotes/" + rem + "/" + branchName
		if _, _, ok := gitRun(path, "rev-parse", "--verify", "--quiet", remoteBranchRef); ok {
			revOut, _, revOk = gitRun(path, "rev-list", limitArg, rem+"/"+branchName+".."+localRef)
		} else {
			if mb, _, mok := gitRun(path, "merge-base", localRef, rem+"/HEAD"); mok && strings.TrimSpace(mb) != "" {
				revOut, _, revOk = gitRun(path, "rev-list", limitArg, strings.TrimSpace(mb)+".."+localRef)
			} else {
				revOut, _, revOk = gitRun(path, "rev-list", limitArg, localRef, "--not", "--remotes="+rem)
			}
		}
	}

	if !revOk {
		return nil, hasUpstream
	}
	return hashSet(revOut), hasUpstream
}

func hashSet(s string) map[string]bool {
	set := map[string]bool{}
	for _, line := range strings.Split(s, "\n") {
		if line != "" {
			set[line] = true
		}
	}
	return set
}
