package handlers

// Local (credential-free) git branch/remote/merge operations, mirroring the
// matching handlers in upstream web/handlers/git.rs + commands/folders.rs. All
// shell out to the system git binary so output shapes match the client parsers.
// Credentialed/remote-network variants (delete_remote_branch, update_branch,
// remove_worktree) are deferred — they need the askpass + DB plumbing.

import (
	"strconv"
	"strings"

	"yoyocode/src-go/web"
)

// detectConflicts mirrors commands/folders.rs detect_conflicts: unmerged paths
// via diff --diff-filter=U. Returns an empty slice on any git failure.
func detectConflicts(path string) []string {
	stdout, _, ok := gitRun(path, "-c", "core.quotePath=false", "diff", "--name-only", "--diff-filter=U")
	out := []string{}
	if !ok {
		return out
	}
	for _, line := range strings.Split(stdout, "\n") {
		p := unquoteGitPath(line)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// gitConflictInfo builds the GitConflictInfo wire object (snake_case, no
// skipped keys — upstream_commit is emitted as null when absent).
func gitConflictInfo(files []string, operation string) map[string]any {
	return map[string]any{
		"has_conflicts":    true,
		"conflicted_files": files,
		"operation":        operation,
		"upstream_commit":  nil,
	}
}

// filterShortlogAuthors ports commands/folders.rs filter_shortlog_authors:
// de-duplicated, frequency-ordered author display names containing query
// (case-insensitive). Input is "<count>\t<Name> <email>" shortlog lines.
func filterShortlogAuthors(stdout, query string, limit int) []string {
	needle := strings.ToLower(strings.TrimSpace(query))
	out := []string{}
	if needle == "" {
		return out
	}
	seen := map[string]bool{}
	for _, line := range strings.Split(stdout, "\n") {
		tab := strings.IndexByte(line, '\t')
		if tab < 0 {
			continue
		}
		rest := strings.TrimSpace(line[tab+1:])
		name := rest
		if idx := strings.LastIndex(rest, " <"); idx >= 0 {
			name = strings.TrimSpace(rest[:idx])
		}
		if name == "" || !strings.Contains(strings.ToLower(name), needle) {
			continue
		}
		if !seen[name] {
			seen[name] = true
			out = append(out, name)
			if len(out) >= limit {
				break
			}
		}
	}
	return out
}

// RegisterGitBranchRemote wires the local branch/remote/merge endpoints.
func RegisterGitBranchRemote(reg *Registry) {
	// git_current_user → Option<String>: configured user.name, or null.
	reg.Register("git_current_user", func(c *web.Context) (any, error) {
		path, err := pathParam(c)
		if err != nil {
			return nil, err
		}
		if err := ensureGitRepo(path); err != nil {
			return nil, err
		}
		stdout, _, ok := gitRun(path, "config", "user.name")
		if !ok {
			return nil, nil
		}
		name := strings.TrimSpace(stdout)
		if name == "" {
			return nil, nil
		}
		return name, nil
	})

	// git_search_authors → Vec<String>: on-demand log-filter author search.
	reg.Register("git_search_authors", func(c *web.Context) (any, error) {
		path, err := pathParam(c)
		if err != nil {
			return nil, err
		}
		if err := ensureGitRepo(path); err != nil {
			return nil, err
		}
		query := GetString(c, "query")
		if strings.TrimSpace(query) == "" {
			return []string{}, nil
		}
		limit := GetInt(c, "limit")
		if limit == 0 {
			limit = 20
		}
		if limit < 1 {
			limit = 1
		}
		if limit > 100 {
			limit = 100
		}
		stdout, stderr, ok := gitRun(path, "shortlog", "-s", "-n", "-e", "--all")
		if !ok {
			return nil, gitCommandError("shortlog", stderr)
		}
		return filterShortlogAuthors(stdout, query, int(limit)), nil
	})

	// git_add_remote / git_remove_remote / git_set_remote_url → () / null.
	reg.Register("git_add_remote", func(c *web.Context) (any, error) {
		path, err := pathParam(c)
		if err != nil {
			return nil, err
		}
		name := GetString(c, "name")
		url := GetString(c, "url")
		if name == "" || url == "" {
			return nil, web.ErrInvalidInput("name and url required")
		}
		if _, stderr, ok := gitRun(path, "remote", "add", name, url); !ok {
			return nil, gitCommandError("remote add", stderr)
		}
		return EmptyResult(), nil
	})
	reg.Register("git_remove_remote", func(c *web.Context) (any, error) {
		path, err := pathParam(c)
		if err != nil {
			return nil, err
		}
		name := GetString(c, "name")
		if name == "" {
			return nil, web.ErrInvalidInput("name required")
		}
		if _, stderr, ok := gitRun(path, "remote", "remove", name); !ok {
			return nil, gitCommandError("remote remove", stderr)
		}
		return EmptyResult(), nil
	})
	reg.Register("git_set_remote_url", func(c *web.Context) (any, error) {
		path, err := pathParam(c)
		if err != nil {
			return nil, err
		}
		name := GetString(c, "name")
		url := GetString(c, "url")
		if name == "" || url == "" {
			return nil, web.ErrInvalidInput("name and url required")
		}
		if _, stderr, ok := gitRun(path, "remote", "set-url", name, url); !ok {
			return nil, gitCommandError("remote set-url", stderr)
		}
		return EmptyResult(), nil
	})

	// git_merge → {merged_commits, conflict}: counts HEAD..branch first, then
	// merges; a non-empty unmerged set is reported as a conflict (not an error).
	reg.Register("git_merge", func(c *web.Context) (any, error) {
		path, err := pathParam(c)
		if err != nil {
			return nil, err
		}
		branch := GetString(c, "branchName", "branch_name")
		if branch == "" {
			return nil, web.ErrInvalidInput("branchName required")
		}
		mergedCommits := 0
		if countOut, _, ok := gitRun(path, "rev-list", "--count", "HEAD.."+branch); ok {
			if n, perr := strconv.Atoi(strings.TrimSpace(countOut)); perr == nil {
				mergedCommits = n
			}
		}
		if _, stderr, ok := gitRun(path, "merge", branch); !ok {
			files := detectConflicts(path)
			if len(files) > 0 {
				return map[string]any{"merged_commits": mergedCommits, "conflict": gitConflictInfo(files, "merge")}, nil
			}
			return nil, gitCommandError("merge", stderr)
		}
		return map[string]any{"merged_commits": mergedCommits, "conflict": nil}, nil
	})

	// git_rebase → {message, conflict}.
	reg.Register("git_rebase", func(c *web.Context) (any, error) {
		path, err := pathParam(c)
		if err != nil {
			return nil, err
		}
		branch := GetString(c, "branchName", "branch_name")
		if branch == "" {
			return nil, web.ErrInvalidInput("branchName required")
		}
		stdout, stderr, ok := gitRun(path, "rebase", branch)
		if !ok {
			files := detectConflicts(path)
			if len(files) > 0 {
				return map[string]any{"message": strings.TrimSpace(stdout), "conflict": gitConflictInfo(files, "rebase")}, nil
			}
			return nil, gitCommandError("rebase", stderr)
		}
		return map[string]any{"message": strings.TrimSpace(stdout), "conflict": nil}, nil
	})

	// git_delete_branch → String (git stdout). force toggles -d vs -D.
	reg.Register("git_delete_branch", func(c *web.Context) (any, error) {
		path, err := pathParam(c)
		if err != nil {
			return nil, err
		}
		branch := GetString(c, "branchName", "branch_name")
		if branch == "" {
			return nil, web.ErrInvalidInput("branchName required")
		}
		flag := "-d"
		if GetBool(c, "force") {
			flag = "-D"
		}
		stdout, stderr, ok := gitRun(path, "branch", flag, branch)
		if !ok {
			return nil, gitCommandError("branch "+flag, stderr)
		}
		return strings.TrimSpace(stdout), nil
	})
}
