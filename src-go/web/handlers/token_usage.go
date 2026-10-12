package handlers

// Token-usage dashboard (B11): report / facets / status / sync, mirroring
// commands/token_usage.rs + token_usage_service.rs. Facts live in
// token_usage_turn (joined to conversation for folder/agent); the aggregation
// (totals, time series with densification, by folder/agent/model, weekday×hour
// heatmap, streaks, top sessions, previous-period compare) is ported faithfully.
// Time buckets are computed in the viewer’s UTC offset. sync is a no-op here:
// materializing facts needs the transcript parser (a later enhancement), so a
// pass writes nothing and reports zeros — the dashboard shows whatever is stored.

import (
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"

	"yoyocode/src-go/db"
	"yoyocode/src-go/web"
)

const (
	tokenUsageMaxScanned = 500000
	tokenUsageMaxDense   = 2000
	tokenUsageTopConvs   = 8
	tokenUsageUnknownMdl = "__unknown__"
)

type usageFact struct {
	conversationID int64
	folderID       int64
	agentType      string
	model          sql.NullString
	occurred       time.Time
	input          int64
	output         int64
	cacheCreate    int64
	cacheRead      int64
	total          int64
	durationMs     int64
}

type usageFilter struct {
	start         *time.Time
	end           *time.Time // exclusive
	folderIDs     []int64
	folderIDsSet  bool
	agentTypes    []string
	agentTypesSet bool
	models        []string
	modelsSet     bool
	bucket        string // day|week|month
	tzOffsetMin   int
	comparePrev   bool
}

func (f usageFilter) emptySelection() bool {
	return (f.folderIDsSet && len(f.folderIDs) == 0) ||
		(f.agentTypesSet && len(f.agentTypes) == 0) ||
		(f.modelsSet && len(f.models) == 0)
}

// ── time / bucket helpers ──

func normalizeTz(min int) int {
	if min < -12*60 {
		return -12 * 60
	}
	if min > 14*60 {
		return 14 * 60
	}
	return min
}

// localCivil shifts a UTC instant into the viewer’s offset and returns the civil
// (naive) datetime as a UTC-tagged time for date math.
func localCivil(t time.Time, tzMin int) time.Time {
	return t.UTC().Add(time.Duration(tzMin) * time.Minute)
}

func civilDate(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

func weekdayFromMonday(t time.Time) int { return (int(t.Weekday()) + 6) % 7 }

func bucketStartDate(date time.Time, bucket string) time.Time {
	switch bucket {
	case "week":
		return date.AddDate(0, 0, -weekdayFromMonday(date))
	case "month":
		return time.Date(date.Year(), date.Month(), 1, 0, 0, 0, 0, time.UTC)
	default:
		return date
	}
}

func nextBucketDate(date time.Time, bucket string) time.Time {
	switch bucket {
	case "week":
		return date.AddDate(0, 0, 7)
	case "month":
		return time.Date(date.Year(), date.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, 1, 0)
	default:
		return date.AddDate(0, 0, 1)
	}
}

func bucketKey(date time.Time, bucket string) string {
	if bucket == "month" {
		return fmt.Sprintf("%04d-%02d", date.Year(), int(date.Month()))
	}
	return date.Format("2006-01-02")
}

// utcFromCivil maps a civil midnight back to the UTC instant it names.
func utcFromCivil(date time.Time, tzMin int) time.Time {
	return date.Add(-time.Duration(tzMin) * time.Minute)
}

func rfc3339Ptr(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UTC().Format(time.RFC3339)
}

// ── aggregation ──

type usageAcc struct {
	input, output, cacheCreate, cacheRead, total, durationMs, turns int64
	convs                                                           map[int64]bool
}

func (a *usageAcc) add(r usageFact) {
	if a.convs == nil {
		a.convs = map[int64]bool{}
	}
	a.input += nonNeg(r.input)
	a.output += nonNeg(r.output)
	a.cacheCreate += nonNeg(r.cacheCreate)
	a.cacheRead += nonNeg(r.cacheRead)
	a.total += nonNeg(r.total)
	a.durationMs += nonNeg(r.durationMs)
	a.turns++
	a.convs[r.conversationID] = true
}

func nonNeg(v int64) int64 {
	if v < 0 {
		return 0
	}
	return v
}

func (a usageAcc) totals(activeDays int) map[string]any {
	return map[string]any{
		"input_tokens": a.input, "output_tokens": a.output,
		"cache_creation_tokens": a.cacheCreate, "cache_read_tokens": a.cacheRead,
		"total_tokens": a.total, "turn_count": a.turns,
		"conversation_count": int64(len(a.convs)), "duration_ms": a.durationMs,
		"active_days": activeDays,
	}
}

func (a usageAcc) breakdown(key, label string) map[string]any {
	return map[string]any{
		"key": key, "label": label,
		"input_tokens": a.input, "output_tokens": a.output,
		"cache_creation_tokens": a.cacheCreate, "cache_read_tokens": a.cacheRead,
		"total_tokens": a.total, "turn_count": a.turns,
		"conversation_count": int64(len(a.convs)),
	}
}

type aggOpts struct {
	bucket       string
	tzMin        int
	rangeStart   *time.Time
	rangeEnd     *time.Time
	folderLabels map[int64]string
	truncated    bool
	comparePrev  bool
}

func aggregateReport(rows, prevRows []usageFact, opts aggOpts) map[string]any {
	tz := opts.tzMin
	bucket := opts.bucket
	var totals usageAcc
	byBucket := map[string]*usageAcc{}
	bucketDates := map[string]time.Time{}
	byFolder := map[int64]*usageAcc{}
	byAgent := map[string]*usageAcc{}
	byModel := map[string]*usageAcc{}
	type heatK struct{ wd, hr int }
	heat := map[heatK][2]int64{}
	type convAgg struct {
		tokens, turns int64
		last          time.Time
		agent         string
		folderID      int64
	}
	perConv := map[int64]*convAgg{}
	activeDates := map[string]bool{}
	var firstAct, lastAct *time.Time

	for _, r := range rows {
		local := localCivil(r.occurred, tz)
		date := civilDate(local)
		totals.add(r)
		bs := bucketStartDate(date, bucket)
		bk := bs.Format("2006-01-02")
		if byBucket[bk] == nil {
			byBucket[bk] = &usageAcc{}
			bucketDates[bk] = bs
		}
		byBucket[bk].add(r)
		if byFolder[r.folderID] == nil {
			byFolder[r.folderID] = &usageAcc{}
		}
		byFolder[r.folderID].add(r)
		if byAgent[r.agentType] == nil {
			byAgent[r.agentType] = &usageAcc{}
		}
		byAgent[r.agentType].add(r)
		mdl := tokenUsageUnknownMdl
		if r.model.Valid && r.model.String != "" {
			mdl = r.model.String
		}
		if byModel[mdl] == nil {
			byModel[mdl] = &usageAcc{}
		}
		byModel[mdl].add(r)
		hk := heatK{weekdayFromMonday(local), local.Hour()}
		cell := heat[hk]
		cell[0] += nonNeg(r.total)
		cell[1]++
		heat[hk] = cell
		if perConv[r.conversationID] == nil {
			perConv[r.conversationID] = &convAgg{last: r.occurred, agent: r.agentType, folderID: r.folderID}
		}
		pc := perConv[r.conversationID]
		pc.tokens += nonNeg(r.total)
		pc.turns++
		if r.occurred.After(pc.last) {
			pc.last = r.occurred
		}
		activeDates[date.Format("2006-01-02")] = true
		occ := r.occurred
		if firstAct == nil || occ.Before(*firstAct) {
			t := occ
			firstAct = &t
		}
		if lastAct == nil || occ.After(*lastAct) {
			t := occ
			lastAct = &t
		}
	}

	series := buildUsageSeries(byBucket, bucketDates, opts, firstAct, lastAct)

	folderItems := []map[string]any{}
	for id, acc := range byFolder {
		label := opts.folderLabels[id]
		if label == "" {
			label = fmt.Sprintf("#%d", id)
		}
		folderItems = append(folderItems, acc.breakdown(fmt.Sprintf("%d", id), label))
	}
	sortBreakdown(folderItems)
	agentItems := []map[string]any{}
	for key, acc := range byAgent {
		agentItems = append(agentItems, acc.breakdown(key, key))
	}
	sortBreakdown(agentItems)
	modelItems := []map[string]any{}
	for key, acc := range byModel {
		modelItems = append(modelItems, acc.breakdown(key, key))
	}
	sortBreakdown(modelItems)

	heatmap := []map[string]any{}
	for k, v := range heat {
		heatmap = append(heatmap, map[string]any{"weekday": k.wd, "hour": k.hr, "total_tokens": v[0], "turn_count": v[1]})
	}
	sort.SliceStable(heatmap, func(i, j int) bool {
		if heatmap[i]["weekday"].(int) != heatmap[j]["weekday"].(int) {
			return heatmap[i]["weekday"].(int) < heatmap[j]["weekday"].(int)
		}
		return heatmap[i]["hour"].(int) < heatmap[j]["hour"].(int)
	})

	type topRow struct {
		id, tokens, turns int64
		last              time.Time
		agent             string
		folderID          int64
	}
	tops := []topRow{}
	for id, pc := range perConv {
		tops = append(tops, topRow{id, pc.tokens, pc.turns, pc.last, pc.agent, pc.folderID})
	}
	sort.SliceStable(tops, func(i, j int) bool {
		if tops[i].tokens != tops[j].tokens {
			return tops[i].tokens > tops[j].tokens
		}
		return tops[i].id < tops[j].id
	})
	if len(tops) > tokenUsageTopConvs {
		tops = tops[:tokenUsageTopConvs]
	}
	topConvs := []map[string]any{}
	for _, tr := range tops {
		var folderLabel any = nil
		if lbl, ok := opts.folderLabels[tr.folderID]; ok {
			folderLabel = lbl
		}
		topConvs = append(topConvs, map[string]any{
			"conversation_id": tr.id, "title": nil, "agent_type": tr.agent,
			"folder_label": folderLabel, "total_tokens": tr.tokens, "turn_count": tr.turns,
			"last_activity_at": tr.last.UTC().Format(time.RFC3339),
		})
	}

	var prevTotals any = nil
	if opts.comparePrev {
		var prev usageAcc
		prevDates := map[string]bool{}
		for _, r := range prevRows {
			prev.add(r)
			prevDates[civilDate(localCivil(r.occurred, tz)).Format("2006-01-02")] = true
		}
		prevTotals = prev.totals(len(prevDates))
	}

	rangeStart := opts.rangeStart
	if rangeStart == nil {
		rangeStart = firstAct
	}
	rangeEnd := opts.rangeEnd
	if rangeEnd == nil && lastAct != nil {
		e := lastAct.Add(time.Millisecond)
		rangeEnd = &e
	}

	return map[string]any{
		"range_start": rfc3339Ptr(rangeStart), "range_end": rfc3339Ptr(rangeEnd),
		"bucket": bucket, "totals": totals.totals(len(activeDates)),
		"previous_totals": prevTotals, "series": series,
		"by_folder": folderItems, "by_agent": agentItems, "by_model": modelItems,
		"heatmap": heatmap, "top_conversations": topConvs,
		"streak":            computeStreak(activeDates),
		"first_activity_at": rfc3339Ptr(firstAct), "last_activity_at": rfc3339Ptr(lastAct),
		"truncated": opts.truncated,
	}
}

func sortBreakdown(items []map[string]any) {
	sort.SliceStable(items, func(i, j int) bool {
		ti := items[i]["total_tokens"].(int64)
		tj := items[j]["total_tokens"].(int64)
		if ti != tj {
			return ti > tj
		}
		return items[i]["key"].(string) < items[j]["key"].(string)
	})
}
func buildUsageSeries(byBucket map[string]*usageAcc, bucketDates map[string]time.Time, opts aggOpts, firstAct, lastAct *time.Time) []map[string]any {
	tz := opts.tzMin
	bucket := opts.bucket
	pointOf := func(date time.Time, acc *usageAcc) map[string]any {
		next := nextBucketDate(date, bucket)
		if acc == nil {
			acc = &usageAcc{}
		}
		return map[string]any{
			"bucket_key":   bucketKey(date, bucket),
			"start":        utcFromCivil(date, tz).Format(time.RFC3339),
			"end":          utcFromCivil(next, tz).Format(time.RFC3339),
			"input_tokens": acc.input, "output_tokens": acc.output,
			"cache_creation_tokens": acc.cacheCreate, "cache_read_tokens": acc.cacheRead,
			"total_tokens": acc.total, "turn_count": acc.turns,
			"conversation_count": int64(len(acc.convs)),
		}
	}
	sparse := func() []map[string]any {
		keys := make([]string, 0, len(bucketDates))
		for k := range bucketDates {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		out := []map[string]any{}
		for _, k := range keys {
			out = append(out, pointOf(bucketDates[k], byBucket[k]))
		}
		return out
	}
	lo := opts.rangeStart
	if lo == nil {
		lo = firstAct
	}
	var hi *time.Time
	if opts.rangeEnd != nil {
		h := opts.rangeEnd.Add(-time.Millisecond)
		hi = &h
	} else {
		hi = lastAct
	}
	if lo == nil || hi == nil {
		return sparse()
	}
	first := bucketStartDate(civilDate(localCivil(*lo, tz)), bucket)
	last := bucketStartDate(civilDate(localCivil(*hi, tz)), bucket)
	if last.Before(first) {
		return sparse()
	}
	out := []map[string]any{}
	cursor := first
	for !cursor.After(last) {
		k := cursor.Format("2006-01-02")
		out = append(out, pointOf(cursor, byBucket[k]))
		if len(out) >= tokenUsageMaxDense {
			return sparse()
		}
		cursor = nextBucketDate(cursor, bucket)
	}
	return out
}

func computeStreak(active map[string]bool) map[string]any {
	if len(active) == 0 {
		return map[string]any{"longest_days": 0, "current_days": 0, "current_ends_on": nil}
	}
	dates := make([]time.Time, 0, len(active))
	for k := range active {
		if t, err := time.Parse("2006-01-02", k); err == nil {
			dates = append(dates, t)
		}
	}
	sort.Slice(dates, func(i, j int) bool { return dates[i].Before(dates[j]) })
	longest, run := 1, 1
	for i := 1; i < len(dates); i++ {
		if dates[i].Equal(dates[i-1].AddDate(0, 0, 1)) {
			run++
		} else {
			run = 1
		}
		if run > longest {
			longest = run
		}
	}
	return map[string]any{
		"longest_days": longest, "current_days": run,
		"current_ends_on": dates[len(dates)-1].Format("2006-01-02"),
	}
}

// ── SQL ──

func fetchFacts(d *db.Database, f usageFilter, limit int) []usageFact {
	out := []usageFact{}
	if f.emptySelection() {
		return out
	}
	q := `SELECT t.conversation_id, c.folder_id, c.agent_type, t.model, t.occurred_at,
		t.input_tokens, t.output_tokens, t.cache_creation_tokens, t.cache_read_tokens, t.total_tokens, t.duration_ms
		FROM token_usage_turn t JOIN conversation c ON c.id = t.conversation_id WHERE 1=1`
	args := []any{}
	if f.start != nil {
		q += " AND t.occurred_at >= ?"
		args = append(args, f.start.UTC().Format(time.RFC3339))
	}
	if f.end != nil {
		q += " AND t.occurred_at < ?"
		args = append(args, f.end.UTC().Format(time.RFC3339))
	}
	if f.folderIDsSet && len(f.folderIDs) > 0 {
		q += " AND c.folder_id IN (" + placeholders(len(f.folderIDs)) + ")"
		for _, id := range f.folderIDs {
			args = append(args, id)
		}
	}
	if f.agentTypesSet && len(f.agentTypes) > 0 {
		q += " AND c.agent_type IN (" + placeholders(len(f.agentTypes)) + ")"
		for _, a := range f.agentTypes {
			args = append(args, a)
		}
	}
	if f.modelsSet && len(f.models) > 0 {
		q += " AND t.model IN (" + placeholders(len(f.models)) + ")"
		for _, m := range f.models {
			args = append(args, m)
		}
	}
	q += " ORDER BY t.occurred_at DESC LIMIT ?"
	args = append(args, limit)
	rows, err := d.Conn.Query(q, args...)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var r usageFact
		var occ string
		if rows.Scan(&r.conversationID, &r.folderID, &r.agentType, &r.model, &occ,
			&r.input, &r.output, &r.cacheCreate, &r.cacheRead, &r.total, &r.durationMs) != nil {
			continue
		}
		r.occurred = parseUsageTime(occ)
		out = append(out, r)
	}
	return out
}

func placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

func parseUsageTime(s string) time.Time {
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05-07:00", "2006-01-02T15:04:05"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}

func folderLabelMap(d *db.Database) map[int64]string {
	out := map[int64]string{}
	rows, err := d.Conn.Query(`SELECT id, name, alias FROM folder`)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var name string
		var alias sql.NullString
		if rows.Scan(&id, &name, &alias) != nil {
			continue
		}
		out[id] = folderDisplayLabel(name, alias)
	}
	return out
}

func folderDisplayLabel(name string, alias sql.NullString) string {
	if alias.Valid {
		if a := strings.TrimSpace(alias.String); a != "" {
			return a
		}
	}
	return name
}

func conversationTitles(d *db.Database, ids []int64) map[int64]string {
	out := map[int64]string{}
	if len(ids) == 0 {
		return out
	}
	q := `SELECT id, title FROM conversation WHERE id IN (` + placeholders(len(ids)) + `)`
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	rows, err := d.Conn.Query(q, args...)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var title sql.NullString
		if rows.Scan(&id, &title) == nil && title.Valid {
			out[id] = title.String
		}
	}
	return out
}

// ── facets / status ──

func facetFolders(d *db.Database) []map[string]any {
	out := []map[string]any{}
	rows, err := d.Conn.Query(`SELECT DISTINCT c.folder_id, f.name, f.alias, f.path, f.parent_id
		FROM token_usage_turn t JOIN conversation c ON c.id = t.conversation_id
		JOIN folder f ON f.id = c.folder_id ORDER BY f.name ASC`)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var fid int64
		var name string
		var alias, path sql.NullString
		var parentID sql.NullInt64
		if rows.Scan(&fid, &name, &alias, &path, &parentID) != nil {
			continue
		}
		var aliasAny any = nil
		if alias.Valid && strings.TrimSpace(alias.String) != "" {
			aliasAny = strings.TrimSpace(alias.String)
		}
		out = append(out, map[string]any{
			"folder_id": fid, "label": folderDisplayLabel(name, alias), "name": name,
			"alias": aliasAny, "path": nsAny(path), "parent_id": niAny(parentID),
		})
	}
	return out
}

// facetDimension lists distinct values of a dimension column present in the
// facts, ordered by summed tokens descending. col is a trusted constant
// ("c.agent_type" / "t.model"), never user input.
func facetDimension(d *db.Database, col string) []any {
	out := []any{}
	q := "SELECT " + col + " AS dim, SUM(t.total_tokens) AS toks " +
		"FROM token_usage_turn t JOIN conversation c ON c.id = t.conversation_id " +
		"WHERE " + col + " IS NOT NULL AND " + col + " != '' " +
		"GROUP BY " + col + " ORDER BY toks DESC"
	rows, err := d.Conn.Query(q)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var v string
		var toks sql.NullInt64
		if rows.Scan(&v, &toks) == nil {
			out = append(out, v)
		}
	}
	return out
}

func facetExtent(d *db.Database) (any, any) {
	var lo, hi sql.NullString
	_ = d.Conn.QueryRow(`SELECT MIN(occurred_at), MAX(occurred_at) FROM token_usage_turn`).Scan(&lo, &hi)
	var loAny, hiAny any = nil, nil
	if lo.Valid && lo.String != "" {
		loAny = parseUsageTime(lo.String).UTC().Format(time.RFC3339)
	}
	if hi.Valid && hi.String != "" {
		hiAny = parseUsageTime(hi.String).UTC().Format(time.RFC3339)
	}
	return loAny, hiAny
}

func workspaceConversationCount(d *db.Database, f usageFilter) int64 {
	if f.emptySelection() {
		return 0
	}
	// Distinct live top-level conversations with usage in range.
	q := `SELECT COUNT(DISTINCT c.id) FROM token_usage_turn t JOIN conversation c ON c.id = t.conversation_id
		WHERE c.deleted_at IS NULL AND c.parent_id IS NULL`
	args := []any{}
	if f.start != nil {
		q += " AND t.occurred_at >= ?"
		args = append(args, f.start.UTC().Format(time.RFC3339))
	}
	if f.end != nil {
		q += " AND t.occurred_at < ?"
		args = append(args, f.end.UTC().Format(time.RFC3339))
	}
	if f.folderIDsSet && len(f.folderIDs) > 0 {
		q += " AND c.folder_id IN (" + placeholders(len(f.folderIDs)) + ")"
		for _, id := range f.folderIDs {
			args = append(args, id)
		}
	}
	if f.agentTypesSet && len(f.agentTypes) > 0 {
		q += " AND c.agent_type IN (" + placeholders(len(f.agentTypes)) + ")"
		for _, a := range f.agentTypes {
			args = append(args, a)
		}
	}
	if f.modelsSet && len(f.models) > 0 {
		q += " AND t.model IN (" + placeholders(len(f.models)) + ")"
		for _, m := range f.models {
			args = append(args, m)
		}
	}
	var n int64
	_ = d.Conn.QueryRow(q, args...).Scan(&n)
	return n
}

func syncCounts(d *db.Database) map[string]any {
	var total, synced, stale, factRows int64
	var lastSynced sql.NullString
	_ = d.Conn.QueryRow(`SELECT COUNT(*) FROM conversation WHERE deleted_at IS NULL`).Scan(&total)
	_ = d.Conn.QueryRow(`SELECT COUNT(*) FROM token_usage_sync`).Scan(&synced)
	_ = d.Conn.QueryRow(`SELECT COUNT(*) FROM token_usage_turn`).Scan(&factRows)
	_ = d.Conn.QueryRow(`SELECT MAX(synced_at) FROM token_usage_sync`).Scan(&lastSynced)
	// Stale: live conversations never synced, or changed since last parse.
	_ = d.Conn.QueryRow(`SELECT COUNT(*) FROM conversation c WHERE c.deleted_at IS NULL AND NOT EXISTS (
		SELECT 1 FROM token_usage_sync s WHERE s.conversation_id = c.id AND s.source_updated_at >= c.updated_at)`).Scan(&stale)
	var lastAny any = nil
	if lastSynced.Valid && lastSynced.String != "" {
		lastAny = parseUsageTime(lastSynced.String).UTC().Format(time.RFC3339)
	}
	return map[string]any{
		"total_conversations": total, "synced_conversations": synced,
		"stale_conversations": stale, "fact_rows": factRows,
		"last_synced_at": lastAny, "running": false,
	}
}

// ── filter parse + commands ──

func parseUsageFilter(raw map[string]any) usageFilter {
	f := usageFilter{bucket: "day"}
	if raw == nil {
		return f
	}
	if s := asString(pick(raw, "start")); s != "" {
		t := parseUsageTime(s)
		if !t.IsZero() {
			f.start = &t
		}
	}
	if s := asString(pick(raw, "end")); s != "" {
		t := parseUsageTime(s)
		if !t.IsZero() {
			f.end = &t
		}
	}
	if v, ok := raw["folderIds"]; ok {
		if arr, isArr := v.([]any); isArr {
			f.folderIDsSet = true
			for _, e := range arr {
				f.folderIDs = append(f.folderIDs, asInt64(e))
			}
		}
	}
	if v, ok := raw["agentTypes"]; ok {
		if arr, isArr := v.([]any); isArr {
			f.agentTypesSet = true
			for _, e := range arr {
				if s, ok := e.(string); ok {
					f.agentTypes = append(f.agentTypes, s)
				}
			}
		}
	}
	if v, ok := raw["models"]; ok {
		if arr, isArr := v.([]any); isArr {
			f.modelsSet = true
			for _, e := range arr {
				if s, ok := e.(string); ok {
					f.models = append(f.models, s)
				}
			}
		}
	}
	switch asString(pick(raw, "bucket")) {
	case "week":
		f.bucket = "week"
	case "month":
		f.bucket = "month"
	default:
		f.bucket = "day"
	}
	f.tzOffsetMin = normalizeTz(int(asInt64(pick(raw, "tzOffsetMinutes"))))
	f.comparePrev = asBool(pick(raw, "comparePrevious"))
	return f
}

func RegisterTokenUsage(reg *Registry) {
	reg.Register("token_usage_report", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		raw, _ := c.Body["filter"].(map[string]any)
		f := parseUsageFilter(raw)
		rows := fetchFacts(d, f, tokenUsageMaxScanned)
		truncated := len(rows) >= tokenUsageMaxScanned
		var prevRows []usageFact
		comparable := f.comparePrev && f.start != nil && f.end != nil && f.end.After(*f.start)
		if comparable {
			span := f.end.Sub(*f.start)
			ps := f.start.Add(-span)
			pe := *f.start
			pf := f
			pf.start = &ps
			pf.end = &pe
			pf.comparePrev = false
			prevRows = fetchFacts(d, pf, tokenUsageMaxScanned)
			if len(prevRows) >= tokenUsageMaxScanned {
				truncated = true
			}
		}
		report := aggregateReport(rows, prevRows, aggOpts{
			bucket: f.bucket, tzMin: f.tzOffsetMin, rangeStart: f.start, rangeEnd: f.end,
			folderLabels: folderLabelMap(d), truncated: truncated, comparePrev: comparable,
		})
		// Headline session count follows the workspace list, not the fold.
		if totals, ok := report["totals"].(map[string]any); ok {
			totals["conversation_count"] = workspaceConversationCount(d, f)
		}
		// Resolve titles for the top sessions.
		if tops, ok := report["top_conversations"].([]map[string]any); ok {
			ids := []int64{}
			for _, it := range tops {
				ids = append(ids, it["conversation_id"].(int64))
			}
			titles := conversationTitles(d, ids)
			for _, it := range tops {
				if t, ok := titles[it["conversation_id"].(int64)]; ok {
					it["title"] = t
				}
			}
		}
		return report, nil
	})

	reg.Register("token_usage_facets", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		lo, hi := facetExtent(d)
		return map[string]any{
			"folders":    facetFolders(d),
			"agents":     facetDimension(d, "c.agent_type"),
			"models":     facetDimension(d, "t.model"),
			"data_start": lo, "data_end": hi,
		}, nil
	})

	reg.Register("token_usage_status", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		return syncCounts(d), nil
	})

	reg.Register("token_usage_sync", func(c *web.Context) (any, error) {
		// Materializing facts needs the transcript parser (later enhancement);
		// a pass currently writes nothing. Report an empty, successful result.
		return map[string]any{
			"scanned": 0, "synced": 0, "skipped": 0, "failed": 0, "lost": 0,
			"turns_written": 0, "tokens_written": 0, "pruned_conversations": 0,
		}, nil
	})
}
