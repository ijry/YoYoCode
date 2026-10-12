package handlers

// Forge PR detail/merge (B9c, GitHub): change_detail, change_files,
// merge_options, merge_change. Reuses the forge auth resolver + GitHub client
// from forge_rest.go. Maps GitHub pulls shapes onto ForgeChangeDetail /
// ForgeChangedFileList / ForgeMergeOptions.

import (
	"encoding/json"
	"fmt"

	"yoyocode/src-go/web"
)

func RegisterForgePR(reg *Registry) {
	reg.Register("forge_change_detail", func(c *web.Context) (any, error) {
		query, _ := c.Body["query"].(map[string]any)
		auth, err := forgeAuthFromCtx(c, asString(pick(orEmpty(query), "accountId", "account_id")))
		if err != nil {
			return nil, err
		}
		number := asInt64(pick(orEmpty(query), "number"))
		status, body, herr := auth.ghRequest("GET", fmt.Sprintf("/repos/%s/pulls/%d", auth.ownerRepo, number), nil)
		if herr != nil {
			return nil, web.ErrNetwork(herr.Error())
		}
		if status < 200 || status >= 300 {
			return nil, auth.ghError("change detail", status, body)
		}
		var pr struct {
			Number         int64  `json:"number"`
			State          string `json:"state"`
			Draft          bool   `json:"draft"`
			Mergeable      *bool  `json:"mergeable"`
			MergeableState string `json:"mergeable_state"`
			Additions      *int64 `json:"additions"`
			Deletions      *int64 `json:"deletions"`
			ChangedFiles   *int64 `json:"changed_files"`
			Commits        *int64 `json:"commits"`
			Base           struct {
				Ref string `json:"ref"`
			} `json:"base"`
			Head struct {
				Ref  string `json:"ref"`
				SHA  string `json:"sha"`
				Repo *struct {
					FullName string `json:"full_name"`
				} `json:"repo"`
			} `json:"head"`
		}
		_ = json.Unmarshal(body, &pr)
		detail := map[string]any{
			"number": pr.Number, "base_ref": pr.Base.Ref, "head_ref": pr.Head.Ref,
			"head_repo": nil, "head_sha": emptyToNil(pr.Head.SHA), "draft": pr.Draft,
			"state": pr.State, "mergeable": pr.Mergeable, "merge_state": emptyToNil(pr.MergeableState),
			"additions": int64Ptr(pr.Additions), "deletions": int64Ptr(pr.Deletions),
			"changed_files": int64Ptr(pr.ChangedFiles), "commits": int64Ptr(pr.Commits),
			"checks": auth.fetchChecks(pr.Head.SHA),
		}
		if pr.Head.Repo != nil && pr.Head.Repo.FullName != "" {
			detail["head_repo"] = pr.Head.Repo.FullName
		}
		return detail, nil
	})

	reg.Register("forge_change_files", func(c *web.Context) (any, error) {
		query, _ := c.Body["query"].(map[string]any)
		auth, err := forgeAuthFromCtx(c, asString(pick(orEmpty(query), "accountId", "account_id")))
		if err != nil {
			return nil, err
		}
		number := asInt64(pick(orEmpty(query), "number"))
		page := clampPage(asInt64(pick(orEmpty(query), "page")))
		perPage := clampPerPage(asInt64(pick(orEmpty(query), "perPage", "per_page")))
		p := fmt.Sprintf("/repos/%s/pulls/%d/files?page=%d&per_page=%d", auth.ownerRepo, number, page, perPage)
		status, body, herr := auth.ghRequest("GET", p, nil)
		if herr != nil {
			return nil, web.ErrNetwork(herr.Error())
		}
		if status < 200 || status >= 300 {
			return nil, auth.ghError("change files", status, body)
		}
		var raw []struct {
			Filename         string `json:"filename"`
			PreviousFilename string `json:"previous_filename"`
			Status           string `json:"status"`
			Additions        int64  `json:"additions"`
			Deletions        int64  `json:"deletions"`
		}
		_ = json.Unmarshal(body, &raw)
		files := make([]map[string]any, 0, len(raw))
		for _, f := range raw {
			files = append(files, map[string]any{
				"path": f.Filename, "previous_path": emptyToNil(f.PreviousFilename),
				"status": mapFileStatus(f.Status), "additions": f.Additions, "deletions": f.Deletions,
			})
		}
		return map[string]any{"files": files, "page": page, "per_page": perPage, "has_next": int64(len(raw)) >= perPage}, nil
	})

	reg.Register("forge_merge_options", func(c *web.Context) (any, error) {
		auth, err := forgeAuthFromCtx(c, GetString(c, "accountId", "account_id"))
		if err != nil {
			return nil, err
		}
		status, body, herr := auth.ghRequest("GET", "/repos/"+auth.ownerRepo, nil)
		if herr != nil {
			return nil, web.ErrNetwork(herr.Error())
		}
		if status < 200 || status >= 300 {
			return nil, auth.ghError("merge options", status, body)
		}
		var repo struct {
			AllowMergeCommit *bool `json:"allow_merge_commit"`
			AllowSquashMerge *bool `json:"allow_squash_merge"`
			AllowRebaseMerge *bool `json:"allow_rebase_merge"`
		}
		_ = json.Unmarshal(body, &repo)
		methods := []string{}
		allow := func(p *bool) bool { return p == nil || *p }
		if allow(repo.AllowMergeCommit) {
			methods = append(methods, "merge")
		}
		if allow(repo.AllowSquashMerge) {
			methods = append(methods, "squash")
		}
		if allow(repo.AllowRebaseMerge) {
			methods = append(methods, "rebase")
		}
		if len(methods) == 0 {
			methods = []string{"merge"}
		}
		return map[string]any{
			"methods": methods, "default_method": methods[0], "merge_strategy": "merge_commit",
		}, nil
	})

	reg.Register("forge_merge_change", func(c *web.Context) (any, error) {
		request, _ := c.Body["request"].(map[string]any)
		auth, err := forgeAuthFromCtx(c, asString(pick(orEmpty(request), "accountId", "account_id")))
		if err != nil {
			return nil, err
		}
		number := asInt64(pick(orEmpty(request), "number"))
		method := asString(pick(orEmpty(request), "method"))
		if method == "" {
			method = "merge"
		}
		payload := map[string]any{"merge_method": method}
		if sha := asString(pick(orEmpty(request), "headSha", "head_sha")); sha != "" {
			payload["sha"] = sha
		}
		status, body, herr := auth.ghRequest("PUT", fmt.Sprintf("/repos/%s/pulls/%d/merge", auth.ownerRepo, number), payload)
		if herr != nil {
			return nil, web.ErrNetwork(herr.Error())
		}
		if status < 200 || status >= 300 {
			return nil, auth.ghError("merge", status, body)
		}
		// Return the merged PR row so the board updates in place.
		st2, rowBody, _ := auth.ghRequest("GET", fmt.Sprintf("/repos/%s/pulls/%d", auth.ownerRepo, number), nil)
		if st2 >= 200 && st2 < 300 {
			var iss ghIssue
			_ = json.Unmarshal(rowBody, &iss)
			return iss.row(true), nil
		}
		return nil, nil
	})
}

// fetchChecks best-effort fetches the GitHub check-runs for a head sha.
func (a *forgeAuth) fetchChecks(sha string) map[string]any {
	empty := map[string]any{"checks": []any{}, "available": false, "partial": false}
	if sha == "" {
		return empty
	}
	status, body, err := a.ghRequest("GET", fmt.Sprintf("/repos/%s/commits/%s/check-runs", a.ownerRepo, sha), nil)
	if err != nil || status < 200 || status >= 300 {
		return empty
	}
	var raw struct {
		CheckRuns []struct {
			ID         int64  `json:"id"`
			Name       string `json:"name"`
			Status     string `json:"status"`
			Conclusion string `json:"conclusion"`
			HTMLURL    string `json:"html_url"`
			Output     struct {
				Summary string `json:"summary"`
			} `json:"output"`
		} `json:"check_runs"`
	}
	_ = json.Unmarshal(body, &raw)
	checks := make([]map[string]any, 0, len(raw.CheckRuns))
	for _, cr := range raw.CheckRuns {
		checks = append(checks, map[string]any{
			"id": fmt.Sprintf("%d", cr.ID), "name": cr.Name, "state": mapCheckState(cr.Status, cr.Conclusion),
			"summary": emptyToNil(cr.Output.Summary), "url": emptyToNil(cr.HTMLURL), "allow_failure": false,
		})
	}
	return map[string]any{"checks": checks, "available": true, "partial": false}
}

func mapFileStatus(s string) string {
	switch s {
	case "added", "copied":
		return "added"
	case "removed":
		return "removed"
	case "renamed":
		return "renamed"
	default:
		return "modified"
	}
}

func mapCheckState(status, conclusion string) string {
	if status != "completed" {
		if status == "in_progress" {
			return "running"
		}
		return "queued"
	}
	switch conclusion {
	case "success":
		return "success"
	case "failure", "timed_out", "action_required", "startup_failure":
		return "failure"
	default:
		return "neutral"
	}
}

func int64Ptr(p *int64) any {
	if p == nil {
		return nil
	}
	return *p
}
