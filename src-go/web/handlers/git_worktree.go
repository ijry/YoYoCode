package handlers

// Git worktree + branch-update write operations (upstream commands/folders.rs:
// git_worktree_add / git_update_branch / git_delete_remote_branch). Original
// Go orchestration of the git subprocess; wire shapes mirror upstream.
//
// git_remove_worktree lives in git_remove_worktree.go (it is tightly coupled to
// the folder + work_task tables).

import (
	"os"
	"strings"

	"yoyocode/src-go/web"
)

// RegisterGitWorktree wires the worktree/branch-update write endpoints.
func RegisterGitWorktree(reg *Registry) {
	reg.Register("git_worktree_add", func(c *web.Context) (any, error) {
		path, err := pathParam(c)
		if err != nil {
			return nil, err
		}
		branch := strings.TrimSpace(GetString(c, "branchName", "branch_name"))
		if branch == "" {
			return nil, web.ErrInvalidInput("branchName required")
		}
		worktreePath := GetString(c, "worktreePath", "worktree_path")
		if worktreePath == "" {
			return nil, web.ErrInvalidInput("worktreePath required")
		}
		// Branch must not already exist.
		if _, _, ok := gitRun(path, "rev-parse", "--verify", "refs/heads/"+branch); ok {
			return nil, web.ErrAlreadyExists("Branch already exists")
		}
		// Target directory must not already exist.
		if _, statErr := os.Stat(worktreePath); statErr == nil {
			return nil, web.ErrAlreadyExists("Worktree directory already exists")
		}
		args := []string{"worktree", "add", "-b", branch, worktreePath}
		if base := strings.TrimSpace(GetString(c, "base")); base != "" {
			args = append(args, base)
		}
		if _, stderr, ok := gitRun(path, args...); !ok {
			return nil, gitCommandError("worktree add", stderr)
		}
		return EmptyResult(), nil
	})

	reg.Register("git_delete_remote_branch", func(c *web.Context) (any, error) {
		path, err := pathParam(c)
		if err != nil {
			return nil, err
		}
		remote := strings.TrimSpace(GetString(c, "remote"))
		if remote == "" {
			return nil, web.ErrInvalidInput("remote required")
		}
		branch := strings.TrimSpace(GetString(c, "branch"))
		if branch == "" {
			return nil, web.ErrInvalidInput("branch required")
		}
		_, stderr, ok := gitRunEnv(path, credentialEnv(c), "push", remote, "--delete", branch)
		if !ok {
			return nil, classifyRemoteError("push --delete", stderr)
		}
		return strings.TrimSpace(stderr), nil
	})

	reg.Register("git_update_branch", func(c *web.Context) (any, error) {
		path, err := pathParam(c)
		if err != nil {
			return nil, err
		}
		if e := ensureGitRepo(path); e != nil {
			return nil, e
		}
		branch := strings.TrimSpace(GetString(c, "branch"))
		if branch == "" {
			return nil, web.ErrInvalidInput("branch required")
		}
		isRemote := GetBool(c, "isRemote", "is_remote")
		return gitUpdateBranch(c, path, branch, isRemote)
	})
}

// gitUpdateBranch mirrors git_update_branch_core: a remote-tracking ref is
// mirrored with a force refspec; a checked-out local branch is reconciled with
// a real pull; any other local branch is fast-forwarded from its configured
// upstream. Returns the GitPullResult shape {updated_files, conflict}.
func gitUpdateBranch(c *web.Context, path, branch string, isRemote bool) (any, error) {
	env := credentialEnv(c)
	if isRemote {
		remote, remoteBranch, ok := strings.Cut(branch, "/")
		if !ok || remote == "" || remoteBranch == "" {
			return nil, web.ErrInvalidInput("Malformed remote branch ref: " + branch)
		}
		trackingRef := "refs/remotes/" + branch
		before := gitRevParseRef(path, trackingRef)
		refspec := "+refs/heads/" + remoteBranch + ":" + trackingRef
		if _, stderr, ok := gitRunEnv(path, env, "fetch", remote, refspec); !ok {
			return nil, classifyRemoteError("fetch", stderr)
		}
		after := gitRevParseRef(path, trackingRef)
		return map[string]any{"updated_files": gitCountRefAdvance(path, before, after), "conflict": nil}, nil
	}

	// Checked-out branch: only a real pull can reconcile the working tree.
	if head := resolveGitHead(path); head["branch"] == branch {
		before := gitHeadSHA(path)
		if _, stderr, ok := gitRunEnv(path, env, "pull", "--ff"); !ok {
			return nil, classifyRemoteError("pull", stderr)
		}
		updated := 0
		if after := gitHeadSHA(path); after != "" && after != before && before != "" {
			updated = gitDiffFileCount(path, before, after)
		}
		return map[string]any{"updated_files": updated, "conflict": nil}, nil
	}

	remote := gitConfigValue(path, "branch."+branch+".remote")
	if remote == "" {
		return nil, web.ErrInvalidInput("Branch '" + branch + "' has no upstream remote configured")
	}
	upstreamRef := gitConfigValue(path, "branch."+branch+".merge")
	if upstreamRef == "" {
		return nil, web.ErrInvalidInput("Branch '" + branch + "' has no upstream branch configured")
	}
	localRef := "refs/heads/" + branch
	before := gitRevParseRef(path, localRef)
	refspecs := []string{upstreamRef + ":" + localRef}
	if remote != "." {
		if ub, ok := strings.CutPrefix(upstreamRef, "refs/heads/"); ok {
			refspecs = append(refspecs, "+"+upstreamRef+":refs/remotes/"+remote+"/"+ub)
		}
	}
	args := append([]string{"fetch", remote}, refspecs...)
	if _, stderr, ok := gitRunEnv(path, env, args...); !ok {
		return nil, classifyRemoteError("fetch", stderr)
	}
	after := gitRevParseRef(path, localRef)
	return map[string]any{"updated_files": gitCountRefAdvance(path, before, after), "conflict": nil}, nil
}

// gitRevParseRef returns the resolved SHA of a ref, or "" when it does not
// resolve (e.g. a tracking ref that does not exist yet).
func gitRevParseRef(path, ref string) string {
	out, _, ok := gitRun(path, "rev-parse", "--verify", "--quiet", ref)
	if !ok {
		return ""
	}
	return strings.TrimSpace(out)
}

// gitConfigValue reads a single git config value, "" when unset.
func gitConfigValue(path, key string) string {
	out, _, ok := gitRun(path, "config", "--get", key)
	if !ok {
		return ""
	}
	return strings.TrimSpace(out)
}

// gitCountRefAdvance counts commits (as file-change count) that a ref advanced
// over, mirroring count_ref_advance: the number of files changed between the
// before/after SHAs. A fresh ref (before == "") counts the files at after.
func gitCountRefAdvance(path, before, after string) int {
	if after == "" || after == before {
		return 0
	}
	if before == "" {
		// New ref — count files introduced by its tip commit.
		out, _, ok := gitRun(path, "show", "--name-only", "--pretty=format:", after)
		if !ok {
			return 0
		}
		return len(nonEmptyTrimmedLines(out))
	}
	return gitDiffFileCount(path, before, after)
}
