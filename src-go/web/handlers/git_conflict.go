package handlers

// Merge/rebase conflict-workspace operations, mirroring the matching handlers
// in upstream web/handlers/git.rs + commands/folders.rs. All are local (no
// credentials) and shell out to the system git binary.

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"yoyocode/src-go/web"
)

// gitRunBytes is gitRun but preserves raw stdout bytes (NUL etc), needed for
// binary detection and base64 of blob contents.
func gitRunBytes(dir string, args ...string) ([]byte, string, bool) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.Bytes(), stderr.String(), err == nil
}

// RegisterGitConflict wires the conflict-workspace endpoints.
func RegisterGitConflict(reg *Registry) {
	// git_start_pull_merge → (): kicks off a no-commit merge so stage entries
	// exist; a conflict outcome is success (the point), a clean/empty one errors.
	reg.Register("git_start_pull_merge", func(c *web.Context) (any, error) {
		path, err := pathParam(c)
		if err != nil {
			return nil, err
		}
		target := GetString(c, "upstreamCommit", "upstream_commit")
		if target == "" {
			target = "@{u}"
		}
		if _, stderr, ok := gitRun(path, "merge", "--no-commit", target); !ok {
			if len(detectConflicts(path)) > 0 {
				return EmptyResult(), nil
			}
			return nil, gitCommandError("merge", stderr)
		}
		return EmptyResult(), nil
	})

	// git_has_merge_head → bool: whether a merge is in progress (MERGE_HEAD).
	reg.Register("git_has_merge_head", func(c *web.Context) (any, error) {
		path, err := pathParam(c)
		if err != nil {
			return nil, err
		}
		_, _, ok := gitRun(path, "rev-parse", "--verify", "MERGE_HEAD")
		return ok, nil
	})

	// git_list_conflicts → Vec<String>: unmerged paths.
	reg.Register("git_list_conflicts", func(c *web.Context) (any, error) {
		path, err := pathParam(c)
		if err != nil {
			return nil, err
		}
		return detectConflicts(path), nil
	})

	// git_conflict_file_versions → {base, ours, theirs, merged}: the three index
	// stages (:1:/:2:/:3:) plus the working-tree file with conflict markers.
	reg.Register("git_conflict_file_versions", func(c *web.Context) (any, error) {
		path, err := pathParam(c)
		if err != nil {
			return nil, err
		}
		file := GetString(c, "file")
		if file == "" {
			return nil, web.ErrInvalidInput("file required")
		}
		versions := make([]string, 0, 3)
		for _, stage := range []string{"1", "2", "3"} {
			spec := ":" + stage + ":" + file
			out, _, ok := gitRunBytes(path, "show", spec)
			if !ok {
				versions = append(versions, "")
				continue
			}
			if looksBinary(out) {
				return nil, web.ErrInvalidInput("Binary files are not supported").WithDetail(spec)
			}
			versions = append(versions, string(out))
		}
		merged := ""
		if b, rerr := os.ReadFile(filepath.Join(path, file)); rerr == nil {
			merged = string(b)
		}
		return map[string]any{
			"base":   versions[0],
			"ours":   versions[1],
			"theirs": versions[2],
			"merged": merged,
		}, nil
	})

	// git_resolve_conflict → (): write resolved content, then stage it.
	reg.Register("git_resolve_conflict", func(c *web.Context) (any, error) {
		path, err := pathParam(c)
		if err != nil {
			return nil, err
		}
		file := GetString(c, "file")
		if file == "" {
			return nil, web.ErrInvalidInput("file required")
		}
		content := GetString(c, "content")
		if werr := os.WriteFile(filepath.Join(path, file), []byte(content), 0o644); werr != nil {
			return nil, web.ErrIO("Failed to write resolved file: " + werr.Error())
		}
		if _, stderr, ok := gitRun(path, "add", file); !ok {
			return nil, gitCommandError("add", stderr)
		}
		return EmptyResult(), nil
	})

	// git_abort_operation → (): merge/pull → merge --abort; rebase → rebase --abort.
	reg.Register("git_abort_operation", func(c *web.Context) (any, error) {
		path, err := pathParam(c)
		if err != nil {
			return nil, err
		}
		op := GetString(c, "operation")
		var args []string
		switch op {
		case "merge", "pull":
			args = []string{"merge", "--abort"}
		case "rebase":
			args = []string{"rebase", "--abort"}
		default:
			return nil, web.ErrInvalidInput("Unknown operation: " + op)
		}
		if _, stderr, ok := gitRun(path, args...); !ok {
			return nil, gitCommandError(op+" --abort", stderr)
		}
		return EmptyResult(), nil
	})

	// git_continue_operation → (): merge/pull → commit --no-edit; rebase →
	// rebase --continue. GIT_EDITOR=true keeps git non-interactive.
	reg.Register("git_continue_operation", func(c *web.Context) (any, error) {
		path, err := pathParam(c)
		if err != nil {
			return nil, err
		}
		op := GetString(c, "operation")
		var args []string
		switch op {
		case "merge", "pull":
			args = []string{"commit", "--no-edit"}
		case "rebase":
			args = []string{"rebase", "--continue"}
		default:
			return nil, web.ErrInvalidInput("Unknown operation: " + op)
		}
		if _, stderr, ok := gitRunEnv(path, []string{"GIT_EDITOR=true"}, args...); !ok {
			return nil, gitCommandError(op+" --continue", strings.TrimSpace(stderr))
		}
		return EmptyResult(), nil
	})
}
