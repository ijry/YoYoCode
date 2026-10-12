package handlers

// Forge REST clients (B9b) — GitHub (github.com + Enterprise /api/v3). Resolves
// a folder's remote → matching stored account → token, then maps the GitHub
// REST shapes onto the shared forge DTOs (ForgeIssueRow/List, ForgeComment,
// ForgeLabel, ForgeIdentity). The recoverable auth failures carry the exact
// i18n keys mcode branches on (Forge.errors.noAccount / unsupportedHost /
// wrongForge, all 422). Gitea/GitLab land in a follow-up.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"yoyocode/src-go/db"
	"yoyocode/src-go/web"
)

const (
	noAccountI18n   = "Forge.errors.noAccount"
	unsupportedI18n = "Forge.errors.unsupportedHost"
	wrongForgeI18n  = "Forge.errors.wrongForge"
)

type forgeAuth struct {
	accountID string
	provider  string
	host      string
	ownerRepo string
	apiBase   string
	token     string
	username  string
	avatarURL string
}

func providerDisplay(p string) string {
	switch p {
	case "github":
		return "GitHub"
	case "gitlab":
		return "GitLab"
	case "gitea":
		return "Gitea"
	}
	return p
}

// resolveForgeAuth maps folder → remote → account → token, or a typed i18n error.
func resolveForgeAuth(d *db.Database, folderID int64, accountID string) (*forgeAuth, error) {
	path := folderPath(d, folderID)
	if path == "" {
		return nil, web.ErrNotFound("folder not found")
	}
	stdout, _, ok := gitRun(path, "remote", "get-url", "origin")
	if !ok {
		return nil, web.ErrConfigMissing("no origin remote").WithI18n(noAccountI18n, map[string]string{"host": "", "provider": "GitHub"})
	}
	host, ownerRepo, ok := parseRemoteURL(strings.TrimSpace(stdout))
	if !ok {
		return nil, web.ErrConfigMissing("unparseable remote").WithI18n(unsupportedI18n, map[string]string{"host": ""})
	}
	provider, recognized := resolveForgeProvider(d, host)
	if !recognized {
		return nil, web.ErrConfigMissing("unsupported host").WithI18n(unsupportedI18n, map[string]string{"host": host})
	}
	acct, found := matchForgeAccount(d, host, accountID)
	if !found {
		return nil, web.ErrConfigMissing("no account for host").WithI18n(noAccountI18n, map[string]string{"host": host, "provider": providerDisplay(provider)})
	}
	// Account's declared provider must agree with the detected one.
	if ap, _ := acct["provider"].(string); ap != "" && ap != provider {
		return nil, web.ErrConfigMissing("wrong forge").WithI18n(wrongForgeI18n, map[string]string{"host": host, "detected": providerDisplay(provider)})
	}
	acctID, _ := acct["id"].(string)
	token := accountTokenValue(d, accountTokenPrefix+acctID)
	if token == "" {
		return nil, web.ErrAuthFailed("missing token for account " + acctID)
	}
	username, _ := acct["username"].(string)
	avatar, _ := acct["avatar_url"].(string)
	if provider != "github" {
		return nil, web.ErrTaskExecution("forge provider not yet supported over REST: " + provider)
	}
	return &forgeAuth{
		accountID: acctID,
		provider:  provider,
		host:      host,
		ownerRepo: ownerRepo,
		apiBase:   githubAPIBase(host),
		token:     token,
		username:  username,
		avatarURL: avatar,
	}, nil
}

func githubAPIBase(host string) string {
	if host == "github.com" {
		return "https://api.github.com"
	}
	return "https://" + host + "/api/v3"
}

// matchForgeAccount finds a stored account for host (optional pinned id).
func matchForgeAccount(d *db.Database, host, accountID string) (map[string]any, bool) {
	accounts := loadGithubAccounts(d)
	arr, _ := accounts["accounts"].([]any)
	var fallback map[string]any
	for _, a := range arr {
		m, ok := a.(map[string]any)
		if !ok {
			continue
		}
		su, _ := m["server_url"].(string)
		ah, _, _ := parseAccountHost(su)
		if ah != strings.ToLower(host) {
			continue
		}
		if accountID != "" {
			if id, _ := m["id"].(string); id == accountID {
				return m, true
			}
			continue
		}
		if b, _ := m["is_default"].(bool); b {
			return m, true
		}
		if fallback == nil {
			fallback = m
		}
	}
	if accountID == "" && fallback != nil {
		return fallback, true
	}
	return nil, false
}

// ─── GitHub HTTP ─────────────────────────────────────────────────────────────

func (a *forgeAuth) ghRequest(method, path string, body any) (int, []byte, error) {
	var reader io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		reader = bytes.NewReader(raw)
	}
	req, _ := http.NewRequest(method, a.apiBase+path, reader)
	req.Header.Set("Authorization", "Bearer "+a.token)
	req.Header.Set("User-Agent", "yoyocode")
	req.Header.Set("Accept", "application/vnd.github+json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := httpClient().Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, data, nil
}

func (a *forgeAuth) ghError(op string, status int, body []byte) error {
	if status == 401 || status == 403 {
		return web.ErrAuthFailed(fmt.Sprintf("github %s: %d", op, status)).WithDetail(string(body))
	}
	return web.ErrExternalCommand(fmt.Sprintf("github %s failed: %d", op, status)).WithDetail(string(body))
}

// ghUser is the shared GitHub user shape.
type ghUser struct {
	Login     string `json:"login"`
	AvatarURL string `json:"avatar_url"`
}

type ghLabel struct {
	Name  string `json:"name"`
	Color string `json:"color"`
}

type ghIssue struct {
	Number      int64     `json:"number"`
	Title       string    `json:"title"`
	Body        *string   `json:"body"`
	State       string    `json:"state"`
	Draft       bool      `json:"draft"`
	Labels      []ghLabel `json:"labels"`
	User        *ghUser   `json:"user"`
	UpdatedAt   string    `json:"updated_at"`
	HTMLURL     string    `json:"html_url"`
	Comments    int64     `json:"comments"`
	PullRequest *struct{} `json:"pull_request"`
}

func mapLabels(in []ghLabel) []map[string]any {
	out := []map[string]any{}
	for _, l := range in {
		if l.Name == "" {
			continue
		}
		out = append(out, map[string]any{"name": l.Name, "color": normalizeHexColor(l.Color)})
	}
	return out
}

func normalizeHexColor(raw string) any {
	h := strings.TrimPrefix(strings.TrimSpace(raw), "#")
	for _, ch := range h {
		if !((ch >= '0' && ch <= '9') || (ch >= 'a' && ch <= 'f') || (ch >= 'A' && ch <= 'F')) {
			return nil
		}
	}
	var expanded string
	switch len(h) {
	case 3:
		for _, ch := range h {
			expanded += string(ch) + string(ch)
		}
	case 6:
		expanded = h
	default:
		return nil
	}
	return "#" + strings.ToLower(expanded)
}

func (iss ghIssue) row(isPR bool) map[string]any {
	row := map[string]any{
		"number": iss.Number, "title": iss.Title, "body": optStrPtr(iss.Body),
		"state": iss.State, "draft": iss.Draft, "labels": mapLabels(iss.Labels),
		"author": nil, "author_avatar": nil,
		"updated_at": emptyToNil(iss.UpdatedAt), "html_url": iss.HTMLURL,
		"is_pr": isPR || iss.PullRequest != nil, "comments": iss.Comments,
	}
	if iss.User != nil {
		row["author"] = iss.User.Login
		row["author_avatar"] = emptyToNil(iss.User.AvatarURL)
	}
	return row
}

func optStrPtr(p *string) any {
	if p == nil || *p == "" {
		return nil
	}
	return *p
}

func emptyToNil(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// ─── handlers ────────────────────────────────────────────────────────────────

func RegisterForgeREST(reg *Registry) {
	reg.Register("forge_identity", func(c *web.Context) (any, error) {
		auth, err := forgeAuthFromCtx(c, GetString(c, "accountId", "account_id"))
		if err != nil {
			return nil, err
		}
		status, body, herr := auth.ghRequest("GET", "/user", nil)
		if herr != nil {
			return nil, web.ErrNetwork(herr.Error())
		}
		if status < 200 || status >= 300 {
			return nil, auth.ghError("identity", status, body)
		}
		var u ghUser
		_ = json.Unmarshal(body, &u)
		return map[string]any{"username": u.Login, "avatar_url": emptyToNil(u.AvatarURL)}, nil
	})

	reg.Register("forge_list_issues", func(c *web.Context) (any, error) {
		query, _ := c.Body["query"].(map[string]any)
		auth, err := forgeAuthFromCtx(c, asString(pick(orEmpty(query), "accountId", "account_id")))
		if err != nil {
			return nil, err
		}
		return auth.listIssues(query)
	})

	reg.Register("forge_tab_count", func(c *web.Context) (any, error) {
		// Honest "unknown": GitHub's accurate counts need the search API; the
		// list already carries has_next, so the switcher degrades to no badge.
		if _, err := forgeAuthFromCtx(c, ""); err != nil {
			return nil, err
		}
		return nil, nil
	})

	reg.Register("forge_list_labels", func(c *web.Context) (any, error) {
		auth, err := forgeAuthFromCtx(c, GetString(c, "accountId", "account_id"))
		if err != nil {
			return nil, err
		}
		status, body, herr := auth.ghRequest("GET", "/repos/"+auth.ownerRepo+"/labels?per_page=100", nil)
		if herr != nil {
			return nil, web.ErrNetwork(herr.Error())
		}
		if status < 200 || status >= 300 {
			return nil, auth.ghError("labels", status, body)
		}
		var labels []ghLabel
		_ = json.Unmarshal(body, &labels)
		return map[string]any{"labels": mapLabels(labels), "truncated": len(labels) >= 100}, nil
	})

	reg.Register("forge_list_comments", func(c *web.Context) (any, error) {
		filters, _ := c.Body["filters"].(map[string]any)
		auth, err := forgeAuthFromCtx(c, asString(pick(orEmpty(filters), "accountId", "account_id")))
		if err != nil {
			return nil, err
		}
		number := asInt64(pick(orEmpty(filters), "number"))
		page := clampPage(asInt64(pick(orEmpty(filters), "page")))
		perPage := clampPerPage(asInt64(pick(orEmpty(filters), "perPage", "per_page")))
		p := fmt.Sprintf("/repos/%s/issues/%d/comments?page=%d&per_page=%d", auth.ownerRepo, number, page, perPage)
		status, body, herr := auth.ghRequest("GET", p, nil)
		if herr != nil {
			return nil, web.ErrNetwork(herr.Error())
		}
		if status < 200 || status >= 300 {
			return nil, auth.ghError("comments", status, body)
		}
		var raw []map[string]any
		_ = json.Unmarshal(body, &raw)
		comments := make([]map[string]any, 0, len(raw))
		for _, m := range raw {
			comments = append(comments, mapGitHubComment(m))
		}
		return map[string]any{"comments": comments, "page": page, "per_page": perPage, "has_next": int64(len(raw)) >= perPage}, nil
	})

	reg.Register("forge_create_comment", func(c *web.Context) (any, error) {
		draft, _ := c.Body["draft"].(map[string]any)
		auth, err := forgeAuthFromCtx(c, asString(pick(orEmpty(draft), "accountId", "account_id")))
		if err != nil {
			return nil, err
		}
		number := asInt64(pick(orEmpty(draft), "number"))
		bodyText := asString(pick(orEmpty(draft), "body"))
		p := fmt.Sprintf("/repos/%s/issues/%d/comments", auth.ownerRepo, number)
		status, resp, herr := auth.ghRequest("POST", p, map[string]any{"body": bodyText})
		if herr != nil {
			return nil, web.ErrNetwork(herr.Error())
		}
		if status < 200 || status >= 300 {
			return nil, auth.ghError("create comment", status, resp)
		}
		var m map[string]any
		_ = json.Unmarshal(resp, &m)
		return mapGitHubComment(m), nil
	})

	reg.Register("forge_set_item_state", func(c *web.Context) (any, error) {
		request, _ := c.Body["request"].(map[string]any)
		auth, err := forgeAuthFromCtx(c, asString(pick(orEmpty(request), "accountId", "account_id")))
		if err != nil {
			return nil, err
		}
		number := asInt64(pick(orEmpty(request), "number"))
		kind := asString(pick(orEmpty(request), "kind"))
		action := asString(pick(orEmpty(request), "action"))
		state := "closed"
		if action == "reopen" {
			state = "open"
		}
		seg := "issues"
		if kind == "pr" || kind == "change" {
			seg = "pulls"
		}
		p := fmt.Sprintf("/repos/%s/%s/%d", auth.ownerRepo, seg, number)
		status, resp, herr := auth.ghRequest("PATCH", p, map[string]any{"state": state})
		if herr != nil {
			return nil, web.ErrNetwork(herr.Error())
		}
		if status < 200 || status >= 300 {
			return nil, auth.ghError("set state", status, resp)
		}
		var iss ghIssue
		_ = json.Unmarshal(resp, &iss)
		return iss.row(seg == "pulls"), nil
	})

	reg.Register("forge_create_issue", func(c *web.Context) (any, error) {
		draft, _ := c.Body["draft"].(map[string]any)
		auth, err := forgeAuthFromCtx(c, asString(pick(orEmpty(draft), "accountId", "account_id")))
		if err != nil {
			return nil, err
		}
		payload := map[string]any{"title": asString(pick(orEmpty(draft), "title"))}
		if b := asString(pick(orEmpty(draft), "body")); b != "" {
			payload["body"] = b
		}
		if labels, ok := pick(orEmpty(draft), "labels").([]any); ok && len(labels) > 0 {
			payload["labels"] = labels
		}
		status, resp, herr := auth.ghRequest("POST", "/repos/"+auth.ownerRepo+"/issues", payload)
		if herr != nil {
			return nil, web.ErrNetwork(herr.Error())
		}
		if status < 200 || status >= 300 {
			return nil, auth.ghError("create issue", status, resp)
		}
		var iss ghIssue
		_ = json.Unmarshal(resp, &iss)
		return iss.row(false), nil
	})
}

func (a *forgeAuth) listIssues(query map[string]any) (any, error) {
	q := orEmpty(query)
	tab := asString(pick(q, "tab"))
	state := asString(pick(q, "state"))
	if state == "" {
		state = "open"
	}
	page := clampPage(asInt64(pick(q, "page")))
	perPage := clampPerPage(asInt64(pick(q, "perPage", "per_page")))
	vals := url.Values{}
	vals.Set("state", state)
	vals.Set("page", strconv.FormatInt(page, 10))
	vals.Set("per_page", strconv.FormatInt(perPage, 10))
	if labels, ok := pick(q, "labels").([]any); ok && len(labels) > 0 {
		parts := []string{}
		for _, l := range labels {
			if s, ok := l.(string); ok {
				parts = append(parts, s)
			}
		}
		if len(parts) > 0 {
			vals.Set("labels", strings.Join(parts, ","))
		}
	}
	if asBool(pick(q, "assignedMe", "assigned_me")) && a.username != "" {
		vals.Set("assignee", a.username)
	}
	endpoint := "issues"
	isPR := tab == "prs"
	if isPR {
		endpoint = "pulls"
	}
	p := "/repos/" + a.ownerRepo + "/" + endpoint + "?" + vals.Encode()
	status, body, herr := a.ghRequest("GET", p, nil)
	if herr != nil {
		return nil, web.ErrNetwork(herr.Error())
	}
	if status < 200 || status >= 300 {
		return nil, a.ghError("list", status, body)
	}
	var issues []ghIssue
	_ = json.Unmarshal(body, &issues)
	rows := make([]map[string]any, 0, len(issues))
	for _, iss := range issues {
		if endpoint == "issues" && iss.PullRequest != nil {
			continue // the /issues feed includes PRs; the Issues tab excludes them
		}
		rows = append(rows, iss.row(isPR))
	}
	return map[string]any{
		"rows": rows, "page": page, "per_page": perPage,
		"total_count": nil, "reachable_count": nil,
		"has_next": int64(len(issues)) >= perPage, "incomplete": true,
	}, nil
}

func mapGitHubComment(m map[string]any) map[string]any {
	user, _ := m["user"].(map[string]any)
	var author, avatar any = nil, nil
	if user != nil {
		if l, ok := user["login"].(string); ok {
			author = l
		}
		if av, ok := user["avatar_url"].(string); ok && av != "" {
			avatar = av
		}
	}
	created := asString(m["created_at"])
	updated := asString(m["updated_at"])
	var updatedOut any = nil
	if updated != "" && updated != created {
		updatedOut = updated
	}
	return map[string]any{
		"id":            fmt.Sprintf("%v", m["id"]),
		"author":        author,
		"author_avatar": avatar,
		"body":          asString(m["body"]),
		"created_at":    emptyToNil(created),
		"updated_at":    updatedOut,
		"html_url":      emptyToNil(asString(m["html_url"])),
	}
}

func forgeAuthFromCtx(c *web.Context, accountID string) (*forgeAuth, error) {
	d := stateDB(c)
	folderID := int64Num(c.Body["folderId"])
	if folderID == 0 {
		folderID = int64Num(c.Body["folder_id"])
	}
	return resolveForgeAuth(d, folderID, accountID)
}

func orEmpty(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}

func clampPage(p int64) int64 {
	if p < 1 {
		return 1
	}
	return p
}

func clampPerPage(p int64) int64 {
	if p < 1 {
		return 20
	}
	if p > 100 {
		return 100
	}
	return p
}
