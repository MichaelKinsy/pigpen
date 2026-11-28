package archive

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// upstream: packages/coding-agent/src/archive/archive.ts

// ArchiveSession is a listed session. All fields serialize explicitly (nullable
// fields serialize as JSON `null`, never omitted) so Python `row["recap"]` never
// raises KeyError. Times are ISO-8601 UTC (`2006-01-02T15:04:05.000Z`).
//
// upstream: packages/coding-agent/src/archive/archive.ts:44-58
type ArchiveSession struct {
	ID       string  `json:"id"`
	File     string  `json:"file"`
	Project  string  `json:"project"`
	Title    string  `json:"title"`
	Created  string  `json:"created"`
	Modified string  `json:"modified"`
	Messages int     `json:"messages"`
	Status   *string `json:"status"`
	Recap    *string `json:"recap"`
}

// ArchiveProject is a project with its newest non-empty session.
// upstream: packages/coding-agent/src/archive/archive.ts:61-68
type ArchiveProject struct {
	Path       string         `json:"path"`
	Sessions   int            `json:"sessions"`
	LastActive string         `json:"lastActive"`
	Latest     ArchiveSession `json:"latest"`
}

// ArchivePrompt is a unique prompt from prompt history, with its latest submission's provenance.
// upstream: packages/coding-agent/src/archive/archive.ts:71-79
type ArchivePrompt struct {
	Text    string  `json:"text"`
	At      string  `json:"at"`
	Project *string `json:"project"`
	Session *string `json:"session"`
	Uses    int     `json:"uses"`
}

// ArchiveRecap is a journaled recap entry.
// upstream: packages/coding-agent/src/archive/archive.ts:82-87
type ArchiveRecap struct {
	Text    string `json:"text"`
	At      string `json:"at"`
	Session string `json:"session"`
	Project string `json:"project"`
}

// ArchiveMatch is text in a session that answered a search.
// upstream: packages/coding-agent/src/archive/archive.ts:90-97
type ArchiveMatch struct {
	Source string  `json:"source"`
	Text   string  `json:"text"`
	At     *string `json:"at"`
}

// ArchiveSearchHit is a session whose title, recaps, or transcript answered a search.
// upstream: packages/coding-agent/src/archive/archive.ts:100-103
type ArchiveSearchHit struct {
	ID       string         `json:"id"`
	File     string         `json:"file"`
	Project  string         `json:"project"`
	Title    string         `json:"title"`
	Created  string         `json:"created"`
	Modified string         `json:"modified"`
	Messages int            `json:"messages"`
	Status   *string        `json:"status"`
	Recap    *string        `json:"recap"`
	Matches  []ArchiveMatch `json:"matches"`
}

// ArchiveSessionDetail is one session with its full recap journal and prompt history.
// upstream: packages/coding-agent/src/archive/archive.ts:106-113
type ArchiveSessionDetail struct {
	ID       string          `json:"id"`
	File     string          `json:"file"`
	Project  string          `json:"project"`
	Title    string          `json:"title"`
	Created  string          `json:"created"`
	Modified string          `json:"modified"`
	Messages int             `json:"messages"`
	Status   *string         `json:"status"`
	Recap    *string         `json:"recap"`
	Parent   *string         `json:"parent"`
	Recaps   []ArchiveRecap  `json:"recaps"`
	Prompts  []ArchivePrompt `json:"prompts"`
}

// ArchiveView pairs structured records (always non-nil) with formatted text output.
// upstream: packages/coding-agent/src/archive/archive.ts:116-119
type ArchiveView struct {
	Text    string
	Records any
}

// Display constants matching upstream archive.ts:122-138 and render-utils.ts:199.
const (
	promptPreviewWidth   = 160
	promptDetailWidth    = 600
	recapTruncateWidth   = 280
	searchMatchLines     = 3
	searchMessageMatches = 5
	shortIDLength        = 13
	iso8601MillisFormat  = "2006-01-02T15:04:05.000Z"
)

// nowFunc is overridable in tests for deterministic relative age formatting.
var nowFunc = time.Now

// ArchiveProjects returns recent projects ordered by last real activity (newest first),
// skipping projects with only empty sessions.
//
// upstream: packages/coding-agent/src/archive/archive.ts:141-161,
//
//	packages/coding-agent/src/session/session-listing.ts:1607-1637
func ArchiveProjects(ctx context.Context, limit int) (ArchiveView, error) {
	allFiles := discoverAllSessionFiles(ctx)
	if ctx != nil && ctx.Err() != nil {
		return ArchiveView{}, ctx.Err()
	}

	type projectAcc struct {
		cwd          string
		sessionCount int
		latest       *sessionInfo
	}

	// Group by bucket directory (preserving newest-first file order within each bucket).
	var bucketOrder []string
	byBucket := make(map[string][]statedFile)
	for _, f := range allFiles {
		if _, exists := byBucket[f.BucketDir]; !exists {
			bucketOrder = append(bucketOrder, f.BucketDir)
		}
		byBucket[f.BucketDir] = append(byBucket[f.BucketDir], f)
	}

	// Merge buckets that resolve to the same normalized project cwd across
	// ~/.pig/agent/sessions and ~/.omp/agent/sessions.
	byProject := make(map[string]*projectAcc)
	seenSessionIDs := make(map[string]map[string]struct{})
	for _, bucket := range bucketOrder {
		if ctx != nil && ctx.Err() != nil {
			return ArchiveView{}, ctx.Err()
		}
		files := byBucket[bucket]
		var latestNonEmpty *sessionInfo
		var anyCwd string
		bucketIDs := make([]string, 0, len(files))

		for _, f := range files {
			if ctx != nil && ctx.Err() != nil {
				return ArchiveView{}, ctx.Err()
			}
			id := sessionIDFromPath(f.Path)
			if latestNonEmpty == nil {
				info := scanSessionMetadata(f.Path, f.Size, f.ModTime)
				if info != nil {
					if anyCwd == "" && info.Cwd != "" {
						anyCwd = info.Cwd
					}
					if id == "" {
						id = info.ID
					}
					if !isEmptySession(info) {
						latestNonEmpty = info
					}
				}
			}
			if id == "" {
				id = f.RealPath
			}
			bucketIDs = append(bucketIDs, id)
		}
		if latestNonEmpty == nil || latestNonEmpty.Cwd == "" {
			continue
		}
		normCwd := normalizeProjectPath(latestNonEmpty.Cwd)
		acc, exists := byProject[normCwd]
		if !exists {
			acc = &projectAcc{cwd: latestNonEmpty.Cwd, latest: latestNonEmpty}
			byProject[normCwd] = acc
			seenSessionIDs[normCwd] = make(map[string]struct{}, len(bucketIDs))
		} else if latestNonEmpty.Modified.After(acc.latest.Modified) {
			acc.latest = latestNonEmpty
			acc.cwd = latestNonEmpty.Cwd
		}
		idSet := seenSessionIDs[normCwd]
		for _, id := range bucketIDs {
			if _, dup := idSet[id]; !dup {
				idSet[id] = struct{}{}
				acc.sessionCount++
			}
		}
	}

	accs := make([]*projectAcc, 0, len(byProject))
	for _, acc := range byProject {
		accs = append(accs, acc)
	}
	sort.SliceStable(accs, func(i, j int) bool {
		if !accs[i].latest.Modified.Equal(accs[j].latest.Modified) {
			return accs[i].latest.Modified.After(accs[j].latest.Modified)
		}
		return accs[i].cwd < accs[j].cwd
	})
	if len(accs) > limit {
		accs = accs[:limit]
	}

	optRecaps := latestOptionalRecaps(ctx)
	records := make([]ArchiveProject, 0, len(accs))
	for _, acc := range accs {
		recap := acc.latest.LatestRecap
		if opt, ok := optRecaps[acc.latest.ID]; ok && opt != "" {
			recap = opt
		}
		records = append(records, ArchiveProject{
			Path:       acc.cwd,
			Sessions:   acc.sessionCount,
			LastActive: formatISO(acc.latest.Modified),
			Latest:     toArchiveSession(acc.latest, recap),
		})
	}

	if len(records) == 0 {
		return ArchiveView{Text: "No projects with sessions yet.", Records: records}, nil
	}
	lines := []string{"Recent projects, newest first:"}
	for _, p := range records {
		lines = append(lines,
			fmt.Sprintf("- %s · %s · active %s", shortenPath(p.Path), formatCount("session", p.Sessions), ago(p.LastActive)),
			fmt.Sprintf("  latest %s · %s", shortID(p.Latest.ID), p.Latest.Title),
		)
		if p.Latest.Recap != nil && *p.Latest.Recap != "" {
			lines = append(lines, fmt.Sprintf("  recap: %s", previewLine(*p.Latest.Recap, recapTruncateWidth)))
		}
	}
	return ArchiveView{Text: strings.Join(lines, "\n"), Records: records}, nil
}

// ArchiveSessions returns the newest non-empty sessions for one project (cwd != "")
// or across all projects (cwd == "").
//
// upstream: packages/coding-agent/src/archive/archive.ts:164-177
func ArchiveSessions(ctx context.Context, cwd string, limit int) (ArchiveView, error) {
	allFiles := discoverAllSessionFiles(ctx)
	if ctx != nil && ctx.Err() != nil {
		return ArchiveView{}, ctx.Err()
	}

	optRecaps := latestOptionalRecaps(ctx)
	seenID := make(map[string]struct{})
	records := make([]ArchiveSession, 0)

	for _, f := range allFiles {
		if ctx != nil && ctx.Err() != nil {
			return ArchiveView{}, ctx.Err()
		}
		if len(records) >= limit {
			break
		}
		if !bucketMatchesProject(f, cwd) {
			continue
		}
		info := scanSessionMetadata(f.Path, f.Size, f.ModTime)
		if info == nil || isEmptySession(info) {
			continue
		}
		if cwd != "" && !sameProjectPath(info.Cwd, cwd) {
			continue
		}
		if _, dup := seenID[info.ID]; dup {
			continue
		}
		seenID[info.ID] = struct{}{}
		recap := info.LatestRecap
		if opt, ok := optRecaps[info.ID]; ok && opt != "" {
			recap = opt
		}
		records = append(records, toArchiveSession(info, recap))
	}

	if len(records) == 0 {
		return ArchiveView{Text: fmt.Sprintf("No sessions %s.", scopeLabel(cwd)), Records: records}, nil
	}
	lines := []string{fmt.Sprintf("Sessions %s, newest first:", scopeLabel(cwd))}
	for _, s := range records {
		lines = append(lines, sessionLine(s, cwd))
		if s.Recap != nil && *s.Recap != "" {
			lines = append(lines, fmt.Sprintf("  recap: %s", previewLine(*s.Recap, recapTruncateWidth)))
		}
	}
	return ArchiveView{Text: strings.Join(lines, "\n"), Records: records}, nil
}

// ArchiveSessionDetailByID resolves one session by unique ID prefix or `.jsonl` file path
// and returns its metadata, all recaps (oldest first), and newest `limit` prompts (oldest first).
//
// upstream: packages/coding-agent/src/archive/archive.ts:268-302, 346-355
func ArchiveSessionDetailByID(ctx context.Context, idOrFile string, limit int) (ArchiveView, error) {
	var file string
	if filepath.IsAbs(idOrFile) {
		file = idOrFile
	} else {
		resolved, err := resolveSessionFile(ctx, idOrFile)
		if err != nil {
			return ArchiveView{}, err
		}
		file = resolved
	}

	full := scanFullSession(ctx, file, true, true, false)
	if full == nil || full.Info == nil {
		return ArchiveView{}, fmt.Errorf("Not a readable session file: %s", shortenPath(file))
	}
	info := full.Info

	// Merge JSONL recaps with any optional recaps.jsonl entries for this session.
	allRecaps := append([]sessionRecapEntry(nil), full.Recaps...)
	for _, r := range readOptionalHistoryRecaps(ctx) {
		if r.SessionID == info.ID {
			allRecaps = append(allRecaps, r)
		}
	}
	sort.SliceStable(allRecaps, func(i, j int) bool {
		if !allRecaps[i].At.Equal(allRecaps[j].At) {
			return allRecaps[i].At.Before(allRecaps[j].At)
		}
		return allRecaps[i].Order < allRecaps[j].Order
	})
	recaps := make([]ArchiveRecap, 0, len(allRecaps))
	for _, r := range allRecaps {
		proj := r.Project
		if proj == "" {
			proj = info.Cwd
		}
		recaps = append(recaps, ArchiveRecap{
			Text:    r.Text,
			At:      formatISO(r.At),
			Session: info.ID,
			Project: proj,
		})
	}

	// Merge JSONL prompts with any optional history.jsonl entries for this session,
	// deduplicating by exact normalized prompt text (matching HistoryStorage), keeping
	// newest `limit` prompts ordered oldest-first.
	promptAgg := newPromptAggregator()
	for _, p := range full.Prompts {
		promptAgg.add(p)
	}
	for _, p := range readOptionalHistoryPrompts(ctx) {
		if p.SessionID == info.ID {
			promptAgg.add(p)
		}
	}
	dedupedPrompts := promptAgg.top("", limit)
	// Reverse newest-first -> oldest-first for session detail view.
	prompts := make([]ArchivePrompt, len(dedupedPrompts))
	for i := range dedupedPrompts {
		prompts[len(dedupedPrompts)-1-i] = dedupedPrompts[i]
	}

	latestRecap := ""
	if len(recaps) > 0 {
		latestRecap = recaps[len(recaps)-1].Text
	}
	base := toArchiveSession(info, latestRecap)
	var parentPtr *string
	if info.ParentSessionPath != "" {
		p := info.ParentSessionPath
		parentPtr = &p
	}

	record := ArchiveSessionDetail{
		ID:       base.ID,
		File:     base.File,
		Project:  base.Project,
		Title:    base.Title,
		Created:  base.Created,
		Modified: base.Modified,
		Messages: base.Messages,
		Status:   base.Status,
		Recap:    base.Recap,
		Parent:   parentPtr,
		Recaps:   recaps,
		Prompts:  prompts,
	}

	facts := []string{
		fmt.Sprintf("project %s", shortenPath(record.Project)),
		fmt.Sprintf("created %s", ago(record.Created)),
		fmt.Sprintf("active %s", ago(record.Modified)),
		formatCount("msg", record.Messages),
	}
	if record.Status != nil && *record.Status != "" {
		facts = append(facts, *record.Status)
	}
	lines := []string{
		fmt.Sprintf("Session %s · %s", record.ID, record.Title),
		strings.Join(facts, " · "),
		fmt.Sprintf("file %s", shortenPath(record.File)),
	}
	if record.Parent != nil && *record.Parent != "" {
		lines = append(lines, fmt.Sprintf("forked from %s", shortenPath(*record.Parent)))
	}
	lines = append(lines, "")
	if len(recaps) == 0 {
		lines = append(lines, "No recaps journaled.")
	} else {
		lines = append(lines, "Recaps, oldest first:")
		for _, r := range recaps {
			lines = append(lines, fmt.Sprintf("- %s: %s", ago(r.At), previewLine(r.Text, recapTruncateWidth)))
		}
	}
	if len(prompts) == 0 {
		lines = append(lines, "No prompts in prompt history.")
	} else {
		more := ""
		if len(prompts) == limit {
			more = fmt.Sprintf("latest %d, ", limit)
		}
		lines = append(lines, fmt.Sprintf("Prompts, %soldest first:", more))
		for _, p := range prompts {
			lines = append(lines, fmt.Sprintf("- %s: %s", ago(p.At), previewLine(p.Text, promptDetailWidth)))
		}
	}
	return ArchiveView{Text: strings.Join(lines, "\n"), Records: record}, nil
}

func resolveSessionFile(ctx context.Context, idPrefix string) (string, error) {
	files := findSessionFilesByPrefix(ctx, idPrefix)
	if len(files) == 1 {
		return files[0], nil
	}
	if len(files) == 0 {
		return "", fmt.Errorf("No session id starts with %q.", idPrefix)
	}
	n := len(files)
	if n > 5 {
		n = 5
	}
	ids := make([]string, 0, n)
	for _, f := range files[:n] {
		id := sessionIDFromPath(f)
		if id == "" {
			stem := strings.TrimSuffix(filepath.Base(f), ".jsonl")
			if sep := strings.LastIndexByte(stem, '_'); sep >= 0 {
				id = stem[sep+1:]
			} else {
				id = stem
			}
		}
		ids = append(ids, id)
	}
	return "", fmt.Errorf("%q matches %d sessions (%s); use a longer prefix.", idPrefix, len(files), strings.Join(ids, ", "))
}

// ArchiveSearch searches session titles, recaps, and user/assistant messages for query
// using WordQuery semantics, returning up to `limit` newest matching sessions.
//
// upstream: packages/coding-agent/src/archive/archive.ts:186-243
func ArchiveSearch(ctx context.Context, query string, cwd string, limit int) (ArchiveView, error) {
	words := ParseWordQuery(query)
	records := make([]ArchiveSearchHit, 0)

	if words != nil {
		allFiles := discoverAllSessionFiles(ctx)
		if ctx != nil && ctx.Err() != nil {
			return ArchiveView{}, ctx.Err()
		}

		// Index optional recaps.jsonl entries by session ID.
		optRecapsBySession := make(map[string][]sessionRecapEntry)
		for _, r := range readOptionalHistoryRecaps(ctx) {
			if cwd != "" && r.Project != "" && !sameProjectPath(r.Project, cwd) {
				continue
			}
			optRecapsBySession[r.SessionID] = append(optRecapsBySession[r.SessionID], r)
		}

		seenID := make(map[string]struct{})
		scanned := 0
		for _, f := range allFiles {
			if ctx != nil && ctx.Err() != nil {
				return ArchiveView{}, ctx.Err()
			}
			if len(records) >= limit || scanned >= maxSearchFiles {
				break
			}
			// Cheaply skip wrong-project buckets via header-only prefix probe before any full scan.
			if !bucketMatchesProject(f, cwd) {
				continue
			}
			meta := scanSessionMetadata(f.Path, f.Size, f.ModTime)
			if meta == nil || isEmptySession(meta) {
				continue
			}
			if cwd != "" && !sameProjectPath(meta.Cwd, cwd) {
				continue
			}
			if _, dup := seenID[meta.ID]; dup {
				continue
			}
			seenID[meta.ID] = struct{}{}
			scanned++

			full := scanFullSession(ctx, f.Path, true, false, true)
			if full == nil || full.Info == nil {
				continue
			}
			info := full.Info

			var matches []ArchiveMatch
			// 1. Title match (only explicit title, matching upstream listSessionTitles;
			//    note firstMessage is already searched as a user message below).
			if explicitTitle := sanitizeSessionName(info.Title); explicitTitle != "" && words.Test(explicitTitle) {
				matches = append(matches, ArchiveMatch{
					Source: "title",
					Text:   explicitTitle,
					At:     nil,
				})
			}

			// 2. Recap matches, newest first.
			sessionRecaps := append([]sessionRecapEntry(nil), full.Recaps...)
			sessionRecaps = append(sessionRecaps, optRecapsBySession[info.ID]...)
			sort.SliceStable(sessionRecaps, func(i, j int) bool {
				if !sessionRecaps[i].At.Equal(sessionRecaps[j].At) {
					return sessionRecaps[i].At.After(sessionRecaps[j].At)
				}
				return sessionRecaps[i].Order > sessionRecaps[j].Order
			})
			for _, r := range sessionRecaps {
				if words.Test(r.Text) {
					atStr := formatISO(r.At)
					matches = append(matches, ArchiveMatch{
						Source: "recap",
						Text:   r.Text,
						At:     &atStr,
					})
				}
			}

			// 3. Message matches (user/assistant), newest first, capped at searchMessageMatches.
			//    upstream: transcript-index.ts:498 (ORDER BY m.at DESC, m.id DESC)
			var msgMatches []sessionTranscriptMsg
			for _, m := range full.Messages {
				if words.Test(m.Text) {
					msgMatches = append(msgMatches, m)
				}
			}
			sort.SliceStable(msgMatches, func(i, j int) bool {
				if !msgMatches[i].At.Equal(msgMatches[j].At) {
					return msgMatches[i].At.After(msgMatches[j].At)
				}
				return msgMatches[i].Order > msgMatches[j].Order
			})
			if len(msgMatches) > searchMessageMatches {
				msgMatches = msgMatches[:searchMessageMatches]
			}
			for _, m := range msgMatches {
				atStr := formatISO(m.At)
				matches = append(matches, ArchiveMatch{
					Source: m.Role,
					Text:   makeSnippet(m.Text, words),
					At:     &atStr,
				})
			}

			if len(matches) == 0 {
				continue
			}

			latestRecap := ""
			if len(sessionRecaps) > 0 {
				latestRecap = sessionRecaps[0].Text
			}
			base := toArchiveSession(info, latestRecap)
			records = append(records, ArchiveSearchHit{
				ID:       base.ID,
				File:     base.File,
				Project:  base.Project,
				Title:    base.Title,
				Created:  base.Created,
				Modified: base.Modified,
				Messages: base.Messages,
				Status:   base.Status,
				Recap:    base.Recap,
				Matches:  matches,
			})
		}
	}

	subject := fmt.Sprintf("%s matching %s", scopeLabel(cwd), jsonQuote(query))
	var lines []string
	if len(records) == 0 {
		lines = []string{fmt.Sprintf("No sessions %s.", subject)}
	} else {
		lines = []string{fmt.Sprintf("Sessions %s, newest first:", subject)}
	}
	for _, hit := range records {
		lines = append(lines, sessionLine(ArchiveSession{
			ID:       hit.ID,
			File:     hit.File,
			Project:  hit.Project,
			Title:    hit.Title,
			Created:  hit.Created,
			Modified: hit.Modified,
			Messages: hit.Messages,
			Status:   hit.Status,
			Recap:    hit.Recap,
		}, cwd))
		var shown []ArchiveMatch
		for _, m := range hit.Matches {
			if m.Source != "title" {
				shown = append(shown, m)
			}
		}
		capShown := len(shown)
		if capShown > searchMatchLines {
			capShown = searchMatchLines
		}
		for _, m := range shown[:capShown] {
			width := promptPreviewWidth
			if m.Source == "recap" {
				width = recapTruncateWidth
			}
			lines = append(lines, fmt.Sprintf("  %s: %s", m.Source, previewLine(m.Text, width)))
		}
		if len(shown) > searchMatchLines {
			lines = append(lines, fmt.Sprintf("  … %d more", len(shown)-searchMatchLines))
		}
	}

	return ArchiveView{Text: strings.Join(lines, "\n"), Records: records}, nil
}

// ArchivePrompts returns unique user prompts newest-first, filtered by project and optional WordQuery.
//
// upstream: packages/coding-agent/src/archive/archive.ts:305-330
func ArchivePrompts(ctx context.Context, query string, hasQuery bool, cwd string, limit int) (ArchiveView, error) {
	var words *WordQuery
	if hasQuery {
		words = ParseWordQuery(query)
		if words == nil {
			subject := fmt.Sprintf("%s matching %s", scopeLabel(cwd), jsonQuote(query))
			return ArchiveView{Text: fmt.Sprintf("No prompts %s.", subject), Records: []ArchivePrompt{}}, nil
		}
	}

	agg := newPromptAggregator()
	// Include optional history.jsonl prompts (which may have no session ID).
	for _, p := range readOptionalHistoryPrompts(ctx) {
		if cwd != "" && p.Project != "" && !sameProjectPath(p.Project, cwd) {
			continue
		}
		if words != nil && !words.Test(p.Text) {
			continue
		}
		agg.add(p)
	}

	// Scan session JSONL user messages across discovered sessions (capped).
	allFiles := discoverAllSessionFiles(ctx)
	if ctx != nil && ctx.Err() != nil {
		return ArchiveView{}, ctx.Err()
	}
	seenID := make(map[string]struct{})
	scanned := 0
	for _, f := range allFiles {
		if ctx != nil && ctx.Err() != nil {
			return ArchiveView{}, ctx.Err()
		}
		if scanned >= maxHistoryScanFiles {
			break
		}
		if !bucketMatchesProject(f, cwd) {
			continue
		}
		meta := scanSessionMetadata(f.Path, f.Size, f.ModTime)
		if meta == nil {
			continue
		}
		if cwd != "" && !sameProjectPath(meta.Cwd, cwd) {
			continue
		}
		if _, dup := seenID[meta.ID]; dup {
			continue
		}
		seenID[meta.ID] = struct{}{}
		scanned++

		full := scanFullSession(ctx, f.Path, false, true, false)
		if full != nil {
			for _, p := range full.Prompts {
				if words != nil && !words.Test(p.Text) {
					continue
				}
				agg.add(p)
			}
		}
	}

	records := agg.top(cwd, limit)

	subject := scopeLabel(cwd)
	if hasQuery && query != "" {
		subject = fmt.Sprintf("%s matching %s", scopeLabel(cwd), jsonQuote(query))
	}
	if len(records) == 0 {
		return ArchiveView{Text: fmt.Sprintf("No prompts %s.", subject), Records: records}, nil
	}
	lines := []string{fmt.Sprintf("Prompts %s, newest first:", subject)}
	for _, p := range records {
		facts := []string{ago(p.At)}
		if cwd == "" && p.Project != nil && *p.Project != "" {
			facts = append(facts, shortenPath(*p.Project))
		}
		if p.Session != nil && *p.Session != "" {
			facts = append(facts, shortID(*p.Session))
		}
		if p.Uses > 1 {
			facts = append(facts, fmt.Sprintf("used %d×", p.Uses))
		}
		lines = append(lines,
			fmt.Sprintf("- %s", strings.Join(facts, " · ")),
			fmt.Sprintf("  %s", previewLine(p.Text, promptPreviewWidth)),
		)
	}
	return ArchiveView{Text: strings.Join(lines, "\n"), Records: records}, nil
}

// ArchiveRecaps returns journaled recaps newest-first within scope.
//
// upstream: packages/coding-agent/src/archive/archive.ts:333-344
func ArchiveRecaps(ctx context.Context, cwd string, limit int) (ArchiveView, error) {
	var raw []sessionRecapEntry
	for _, r := range readOptionalHistoryRecaps(ctx) {
		if cwd != "" && !sameProjectPath(r.Project, cwd) {
			continue
		}
		raw = append(raw, r)
	}

	allFiles := discoverAllSessionFiles(ctx)
	if ctx != nil && ctx.Err() != nil {
		return ArchiveView{}, ctx.Err()
	}
	seenID := make(map[string]struct{})
	scanned := 0
	for _, f := range allFiles {
		if ctx != nil && ctx.Err() != nil {
			return ArchiveView{}, ctx.Err()
		}
		if scanned >= maxHistoryScanFiles {
			break
		}
		if !bucketMatchesProject(f, cwd) {
			continue
		}
		meta := scanSessionMetadata(f.Path, f.Size, f.ModTime)
		if meta == nil {
			continue
		}
		if cwd != "" && !sameProjectPath(meta.Cwd, cwd) {
			continue
		}
		if _, dup := seenID[meta.ID]; dup {
			continue
		}
		seenID[meta.ID] = struct{}{}
		scanned++

		if meta.LatestRecap == "" {
			// Fast skip: scanSessionMetadata already walked the file and saw no recap entry.
			continue
		}
		full := scanFullSession(ctx, f.Path, true, false, false)
		if full != nil {
			raw = append(raw, full.Recaps...)
		}
	}

	sort.SliceStable(raw, func(i, j int) bool {
		if !raw[i].At.Equal(raw[j].At) {
			return raw[i].At.After(raw[j].At)
		}
		return raw[i].Order > raw[j].Order
	})
	if len(raw) > limit {
		raw = raw[:limit]
	}

	records := make([]ArchiveRecap, 0, len(raw))
	for _, r := range raw {
		records = append(records, ArchiveRecap{
			Text:    r.Text,
			At:      formatISO(r.At),
			Session: r.SessionID,
			Project: r.Project,
		})
	}
	if len(records) == 0 {
		return ArchiveView{Text: fmt.Sprintf("No recaps %s.", scopeLabel(cwd)), Records: records}, nil
	}
	lines := []string{fmt.Sprintf("Recaps %s, newest first:", scopeLabel(cwd))}
	for _, r := range records {
		facts := []string{ago(r.At)}
		if cwd == "" && r.Project != "" {
			facts = append(facts, shortenPath(r.Project))
		}
		facts = append(facts, shortID(r.Session))
		lines = append(lines,
			fmt.Sprintf("- %s", strings.Join(facts, " · ")),
			fmt.Sprintf("  %s", previewLine(r.Text, recapTruncateWidth)),
		)
	}
	return ArchiveView{Text: strings.Join(lines, "\n"), Records: records}, nil
}

// promptAggregator deduplicates exact normalized prompt text incrementally as
// files are scanned, retaining the newest submission's timestamp/project/session
// and total use count without buffering all raw prompts in memory.
type promptAggEntry struct {
	text    string
	at      time.Time
	order   int
	project string
	session string
	uses    int
}

type promptAggregator struct {
	byText map[string]*promptAggEntry
	seq    int
}

func newPromptAggregator() *promptAggregator {
	return &promptAggregator{byText: make(map[string]*promptAggEntry)}
}

func (pa *promptAggregator) add(e sessionPromptEntry) {
	if e.Text == "" {
		return
	}
	pa.seq++
	ord := pa.seq
	cur, exists := pa.byText[e.Text]
	if !exists {
		pa.byText[e.Text] = &promptAggEntry{
			text:    e.Text,
			at:      e.At,
			order:   ord,
			project: e.Project,
			session: e.SessionID,
			uses:    1,
		}
		return
	}
	cur.uses++
	if e.At.After(cur.at) || (e.At.Equal(cur.at) && ord > cur.order) {
		cur.at = e.At
		cur.order = ord
		cur.project = e.Project
		cur.session = e.SessionID
	}
}

func (pa *promptAggregator) top(cwd string, limit int) []ArchivePrompt {
	if limit <= 0 || len(pa.byText) == 0 {
		return []ArchivePrompt{}
	}
	// Keep a bounded top-limit slice sorted newest-first.
	best := make([]*promptAggEntry, 0, min(limit, len(pa.byText)))
	better := func(a, b *promptAggEntry) bool {
		if !a.at.Equal(b.at) {
			return a.at.After(b.at)
		}
		return a.order > b.order
	}
	for _, a := range pa.byText {
		if cwd != "" && (a.project == "" || !sameProjectPath(a.project, cwd)) {
			continue
		}
		if len(best) == limit && !better(a, best[len(best)-1]) {
			continue
		}
		idx := sort.Search(len(best), func(i int) bool { return better(a, best[i]) })
		if len(best) < limit {
			best = append(best, nil)
		}
		copy(best[idx+1:], best[idx:])
		best[idx] = a
	}
	out := make([]ArchivePrompt, 0, len(best))
	for _, a := range best {
		var projPtr, sessPtr *string
		if a.project != "" {
			p := a.project
			projPtr = &p
		}
		if a.session != "" {
			s := a.session
			sessPtr = &s
		}
		out = append(out, ArchivePrompt{
			Text:    a.text,
			At:      formatISO(a.at),
			Project: projPtr,
			Session: sessPtr,
			Uses:    a.uses,
		})
	}
	return out
}

func latestOptionalRecaps(ctx context.Context) map[string]string {
	raw := readOptionalHistoryRecaps(ctx)
	if len(raw) == 0 {
		return nil
	}
	sort.SliceStable(raw, func(i, j int) bool {
		if !raw[i].At.Equal(raw[j].At) {
			return raw[i].At.After(raw[j].At)
		}
		return raw[i].Order > raw[j].Order
	})
	out := make(map[string]string, len(raw))
	for _, r := range raw {
		if _, exists := out[r.SessionID]; !exists {
			out[r.SessionID] = r.Text
		}
	}
	return out
}

func toArchiveSession(info *sessionInfo, recap string) ArchiveSession {
	created := info.Created
	if created.IsZero() {
		created = info.Modified
	}
	var statusPtr *string
	if info.Status != "" {
		s := string(info.Status)
		statusPtr = &s
	}
	var recapPtr *string
	if recap != "" {
		r := recap
		recapPtr = &r
	}
	return ArchiveSession{
		ID:       info.ID,
		File:     info.Path,
		Project:  info.Cwd,
		Title:    sessionDisplayName(info),
		Created:  formatISO(created),
		Modified: formatISO(info.Modified),
		Messages: info.MessageCount,
		Status:   statusPtr,
		Recap:    recapPtr,
	}
}

func sessionLine(s ArchiveSession, cwd string) string {
	facts := []string{shortID(s.ID), ago(s.Modified)}
	if cwd == "" && s.Project != "" {
		facts = append(facts, shortenPath(s.Project))
	}
	facts = append(facts, formatCount("msg", s.Messages))
	if s.Status != nil && *s.Status != "" && *s.Status != string(StatusComplete) && *s.Status != string(StatusUnknown) {
		facts = append(facts, *s.Status)
	}
	facts = append(facts, s.Title)
	return "- " + strings.Join(facts, " · ")
}

func scopeLabel(cwd string) string {
	if cwd != "" {
		return "in " + shortenPath(cwd)
	}
	return "across all projects"
}

func shortID(id string) string {
	if len(id) <= shortIDLength {
		return id
	}
	return id[:shortIDLength]
}

func formatISO(t time.Time) string {
	if t.IsZero() {
		return time.Unix(0, 0).UTC().Format(iso8601MillisFormat)
	}
	return t.UTC().Format(iso8601MillisFormat)
}

func ago(iso string) string {
	t, ok := parseTimestamp(iso)
	if !ok {
		return ""
	}
	secs := nowFunc().Sub(t).Seconds()
	if secs < 1 {
		secs = 1
	}
	return formatAge(secs)
}

// formatAge mirrors upstream packages/utils/src/format.ts:106-122.
func formatAge(ageSeconds float64) string {
	if ageSeconds <= 0 || math.IsNaN(ageSeconds) || math.IsInf(ageSeconds, 0) {
		return ""
	}
	mins := int(math.Floor(ageSeconds / 60))
	hours := mins / 60
	days := hours / 24
	weeks := days / 7
	months := days / 30
	years := days / 365

	if years > 0 {
		return fmt.Sprintf("%dy ago", years)
	}
	if months > 0 {
		return fmt.Sprintf("%dmo ago", months)
	}
	if weeks > 0 {
		return fmt.Sprintf("%dw ago", weeks)
	}
	if days > 0 {
		return fmt.Sprintf("%dd ago", days)
	}
	if hours > 0 {
		return fmt.Sprintf("%dh ago", hours)
	}
	if mins > 0 {
		return fmt.Sprintf("%dm ago", mins)
	}
	return "just now"
}

// formatCount mirrors upstream packages/utils/src/format.ts:98-101, 127-132.
func formatCount(label string, count int) string {
	if count == 1 {
		return fmt.Sprintf("1 %s", label)
	}
	lower := strings.ToLower(label)
	if strings.HasSuffix(lower, "ch") || strings.HasSuffix(lower, "sh") ||
		strings.HasSuffix(lower, "s") || strings.HasSuffix(lower, "x") || strings.HasSuffix(lower, "z") {
		return fmt.Sprintf("%d %ses", count, label)
	}
	if len(lower) >= 2 && lower[len(lower)-1] == 'y' && !strings.ContainsRune("aeiou", rune(lower[len(lower)-2])) {
		return fmt.Sprintf("%d %sies", count, label[:len(label)-1])
	}
	return fmt.Sprintf("%d %ss", count, label)
}

// flattenLine collapses runs of whitespace to a single space and trims outer whitespace.
// upstream: packages/tui/src/render/render-utils.ts:239-241
func flattenLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// previewLine collapses whitespace to one line and truncates to maxRunes with trailing "…".
// upstream: packages/tui/src/render/render-utils.ts:250-252
func previewLine(text string, maxRunes int) string {
	flat := flattenLine(text)
	if maxRunes <= 0 {
		return ""
	}
	runes := []rune(flat)
	if len(runes) <= maxRunes {
		return flat
	}
	if maxRunes == 1 {
		return "…"
	}
	return string(runes[:maxRunes-1]) + "…"
}

// shortenPath replaces a leading home directory with "~".
// upstream: packages/tui/src/render/render-utils.ts:973-986
func shortenPath(filePath string) string {
	if filePath == "" {
		return ""
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filePath
	}
	home = strings.TrimRight(home, `/\`)
	if filePath == home {
		return "~"
	}
	if strings.HasPrefix(filePath, home+"/") || strings.HasPrefix(filePath, home+`\`) {
		rest := strings.ReplaceAll(filePath[len(home):], `\`, "/")
		return "~" + rest
	}
	return filePath
}

func expandHome(p string) string {
	if p == "" {
		return ""
	}
	if p == "~" || strings.HasPrefix(p, "~/") || strings.HasPrefix(p, `~\`) {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			if p == "~" {
				return home
			}
			return filepath.Join(home, p[2:])
		}
	}
	return p
}

func resolveToCwd(target, cwd string) string {
	p := strings.TrimSpace(target)
	if strings.HasPrefix(p, "@") {
		rest := p[1:]
		if strings.HasPrefix(rest, "/") || rest == "~" || strings.HasPrefix(rest, "~/") || filepath.IsAbs(rest) {
			p = rest
		}
	}
	p = expandHome(p)
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	if cwd == "" {
		if abs, err := filepath.Abs(p); err == nil {
			return abs
		}
		return filepath.Clean(p)
	}
	return filepath.Clean(filepath.Join(cwd, p))
}

func jsonQuote(s string) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(s); err != nil {
		return fmt.Sprintf("%q", s)
	}
	return strings.TrimSuffix(buf.String(), "\n")
}
