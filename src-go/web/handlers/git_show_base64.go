package handlers

// git_show_file_base64 (binary-safe blob read for image diffs) and
// git_commit_files (on-demand per-commit file changes), mirroring
// commands/folders.rs. Local reads, no credentials.

import (
	"encoding/base64"
	"strconv"
	"strings"

	"yoyocode/src-go/web"
)

const (
	fileBase64DefaultMaxBytes = 20_000_000
	fileBase64MaxBytes        = 100_000_000
)

// RegisterGitShowBase64 wires git_show_file_base64 + git_commit_files.
func RegisterGitShowBase64(reg *Registry) {
	// git_show_file_base64 → GitBlobBase64 {exists, ref_missing, data, byte_size,
	// too_large}: the raw bytes of <ref>:<file>, base64-encoded, size-gated.
	reg.Register("git_show_file_base64", func(c *web.Context) (any, error) {
		path, err := pathParam(c)
		if err != nil {
			return nil, err
		}
		if err := ensureGitRepo(path); err != nil {
			return nil, err
		}
		file := GetString(c, "file")
		if file == "" {
			return nil, web.ErrInvalidInput("file required")
		}
		gitRef := GetString(c, "refName", "ref_name")
		if gitRef == "" {
			gitRef = "HEAD"
		}
		fileSpec := gitRef + ":" + file
		limit := GetInt(c, "maxBytes", "max_bytes")
		if limit == 0 {
			limit = fileBase64DefaultMaxBytes
		}
		if limit < 4096 {
			limit = 4096
		}
		if limit > fileBase64MaxBytes {
			limit = fileBase64MaxBytes
		}

		missing := func(refMissing bool) map[string]any {
			return map[string]any{"exists": false, "ref_missing": refMissing, "data": "", "byte_size": int64(0), "too_large": false}
		}

		// Pin the object id first so size + read describe the same immutable blob.
		oidOut, _, oidOK := gitRun(path, "rev-parse", "--verify", "--quiet", fileSpec)
		oid := strings.TrimSpace(oidOut)
		if !oidOK || oid == "" {
			// Distinguish a missing path from a missing revision.
			_, _, revOK := gitRun(path, "rev-parse", "--verify", "--quiet", gitRef+"^{commit}")
			return missing(!revOK), nil
		}

		sizeOut, sizeErr, sizeOK := gitRun(path, "cat-file", "-s", oid)
		if !sizeOK {
			return nil, gitCommandError("cat-file", sizeErr)
		}
		byteSize, perr := strconv.ParseInt(strings.TrimSpace(sizeOut), 10, 64)
		if perr != nil {
			return nil, web.ErrExternalCommand("git cat-file returned an unreadable object size").WithDetail(strings.TrimSpace(sizeOut))
		}
		if byteSize > int64(limit) {
			return map[string]any{"exists": true, "ref_missing": false, "data": "", "byte_size": byteSize, "too_large": true}, nil
		}

		blob, blobErr, blobOK := gitRunBytes(path, "cat-file", "blob", oid)
		if !blobOK {
			return nil, gitCommandError("cat-file", blobErr)
		}
		return map[string]any{
			"exists":      true,
			"ref_missing": false,
			"data":        base64.StdEncoding.EncodeToString(blob),
			"byte_size":   int64(len(blob)),
			"too_large":   false,
		}, nil
	})

	// git_commit_files → Vec<GitLogFileChange>: file changes of one commit, same
	// --raw/--numstat shape git_log emits (first-parent, no renames).
	reg.Register("git_commit_files", func(c *web.Context) (any, error) {
		path, err := pathParam(c)
		if err != nil {
			return nil, err
		}
		if err := ensureGitRepo(path); err != nil {
			return nil, err
		}
		commit := GetString(c, "commit")
		if commit == "" {
			return nil, web.ErrInvalidInput("commit required")
		}
		stdout, stderr, ok := gitRun(path, "-c", "core.quotePath=false",
			"show", "--format=", "--first-parent", "--raw", "--numstat", "--no-renames", commit)
		if !ok {
			return nil, gitCommandError("show", stderr)
		}
		return parseCommitFileChanges(stdout), nil
	})
}

// parseCommitFileChanges merges the :-prefixed raw status lines and the numstat
// lines (same parsers git_log uses) into ordered {path, status, additions,
// deletions} records.
func parseCommitFileChanges(stdout string) []map[string]any {
	type fc struct {
		status               string
		additions, deletions int64
	}
	order := []string{}
	m := map[string]*fc{}
	get := func(p string) *fc {
		if f, ok := m[p]; ok {
			return f
		}
		f := &fc{}
		m[p] = f
		order = append(order, p)
		return f
	}
	for _, line := range strings.Split(stdout, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, ":") {
			if st, fp, ok := parseRawFileLine(line); ok {
				get(fp).status = st
			}
			continue
		}
		if add, del, fp, ok := parseNumstatFileLine(line); ok {
			f := get(fp)
			f.additions = add
			f.deletions = del
		}
	}
	out := make([]map[string]any, 0, len(order))
	for _, fp := range order {
		f := m[fp]
		st := f.status
		if st == "" {
			st = "M"
		}
		out = append(out, map[string]any{"path": fp, "status": st, "additions": f.additions, "deletions": f.deletions})
	}
	return out
}
