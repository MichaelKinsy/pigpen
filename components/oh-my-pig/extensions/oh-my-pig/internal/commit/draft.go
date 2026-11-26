package commit

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// upstream: packages/coding-agent/src/commit/selective/draft.ts

const (
	maxTextBytes        = 4 * 1024 * 1024
	binarySniffBytes    = 8192
	symlinkMode         = "120000"
	readConcurrency     = 16
	recentCommitLimit   = 256
	commitDateSlackMs   = 1000
	lineDisplayChars    = 160
	unrecordedListLimit = 12
)

// ScopeSpec selects which agents belong to a draft's scope.
// Empty slice / nil: caller + subagents.
// Single element "self": caller alone.
// Single element "*": every agent of the session.
// Explicit agent ID(s): each with its own subagents.
// upstream: draft.ts:61
type ScopeSpec struct {
	Set    bool
	Single string   // when Set && len(List)==0
	List   []string // when Set && passed as array
}

func ScopeDefault() ScopeSpec        { return ScopeSpec{} }
func ScopeString(s string) ScopeSpec { return ScopeSpec{Set: true, Single: s} }
func ScopeList(list []string) ScopeSpec {
	return ScopeSpec{Set: true, List: append([]string(nil), list...)}
}

// DraftSource holds inputs for CreateDraft.
// upstream: draft.ts:64
type DraftSource struct {
	Cwd       string
	Journal   *ChangeJournal // nil when session keeps no artifacts
	Caller    string
	Agents    ScopeSpec
	IsRunning func(agent string) bool
	Ctx       context.Context
}

// WholeFileState holds a blob object id and its git mode on one side of a whole-file change.
// upstream: draft.ts:83
type WholeFileState struct {
	ID   string
	Mode string
}

// SelectedChange is the exact content to commit for one path.
// upstream: draft.ts:89
type SelectedChange struct {
	Kind        string // "text" or "whole"
	Path        string
	BeforeText  *FileState
	AfterText   *FileState
	BeforeWhole *WholeFileState
	AfterWhole  *WholeFileState
}

type wholeReason string

const (
	wholeReasonContent wholeReason = "content"
	wholeReasonSymlink wholeReason = "symlink"
	wholeReasonMode    wholeReason = "mode"
	wholeReasonGitlink wholeReason = "gitlink"
)

// SelectionTarget describes what to select or deselect in Take/Drop.
// upstream: draft.ts:102
type SelectionTarget struct {
	Path  *string
	Units []string
	Grep  *string
}

type lineCounts struct {
	added   int
	removed int
}

type wholeInfo struct {
	reason wholeReason
	before *WholeFileState
	after  *WholeFileState
}

type fileChange struct {
	path           string
	head           *string
	work           *string
	headMode       string
	mode           string
	counts         lineCounts
	selected       map[int]bool
	wholeSelected  bool
	editors        []string
	attribution    *Attribution
	classification *Classification
	whole          *wholeInfo
}

// AgentInfo summarizes one agent known to a Draft.
type AgentInfo struct {
	ID      string
	Parent  string
	InScope bool
	Running bool
}

// Draft is one agent tree's proposed commit in a shared, dirty worktree.
// upstream: draft.ts:636
type Draft struct {
	ID        string
	Root      string
	Head      *string // nil before first commit
	Branch    string
	Caller    string
	Scope     []string // deterministic insertion order matching JS Set iteration
	scopeSet  map[string]bool
	Agents    []AgentInfo
	Staged    []string
	Skipped   []string
	Journaled bool
	files     []*fileChange
}

// isProbablyBinaryHeader mirrors packages/utils/src/binary.ts:33.
func isProbablyBinaryHeader(header []byte) bool {
	if bytes.IndexByte(header, 0) >= 0 {
		return true
	}
	// Tolerate a multibyte UTF-8 sequence truncated at the end of header (streaming decode).
	if utf8.Valid(header) {
		return false
	}
	// Strip up to 3 trailing incomplete-sequence bytes before checking validity.
	for trim := 1; trim <= 3 && trim <= len(header); trim++ {
		tail := header[len(header)-trim:]
		if utf8.RuneStart(tail[0]) && !utf8.FullRune(tail) {
			return !utf8.Valid(header[:len(header)-trim])
		}
	}
	return true
}

// decodeText returns (text, true) when bytes are valid UTF-8 text within maxTextBytes.
// upstream: draft.ts:156
func decodeText(data []byte) (string, bool) {
	if len(data) > maxTextBytes {
		return "", false
	}
	sniff := data
	if len(sniff) > binarySniffBytes {
		sniff = sniff[:binarySniffBytes]
	}
	if isProbablyBinaryHeader(sniff) {
		return "", false
	}
	if !utf8.Valid(data) {
		return "", false
	}
	return string(data), true
}

func actorName(actor string) string {
	switch actor {
	case PRE:
		return "pre-existing"
	case GAP, TAIL, UNRECORDED, REORDERED:
		return "unrecorded"
	default:
		return actor
	}
}

func describeReason(reason ReviewReason) string {
	switch reason.Kind {
	case "reordered":
		return "replayed edits disagree with the HEAD-to-worktree diff after a reorder; review the net change"
	case "entangled":
		names := make([]string, len(reason.With))
		for i, a := range reason.With {
			names[i] = actorName(a)
		}
		return fmt.Sprintf("edits lines added by %s that were never committed", strings.Join(names, ", "))
	case "placement":
		names := make([]string, len(reason.With))
		for i, a := range reason.With {
			names[i] = actorName(a)
		}
		return fmt.Sprintf("placement depends on an uncommitted replacement by %s", strings.Join(names, ", "))
	case "rewritten":
		if reason.By == GAP || reason.By == TAIL {
			suffix := ""
			if reason.By == GAP {
				suffix = " before a later edit"
			}
			return "in-scope lines rewritten afterwards by an unrecorded change" + suffix
		}
		return fmt.Sprintf("in-scope lines rewritten afterwards by %s", reason.By)
	case "hunk":
		return "shares a hunk with a change that needs review"
	default:
		return "needs review"
	}
}

func displayLine(text string) string {
	runes := []rune(text)
	if len(runes) > lineDisplayChars {
		return string(runes[:lineDisplayChars]) + "…"
	}
	return text
}

type hunkUnitGroup struct {
	actor string
	ops   []*ChangeOp
}

func hunkUnits(hunk *Hunk) []hunkUnitGroup {
	var order []hunkUnitGroup
	byActor := make(map[string]int)
	for _, op := range hunk.Ops {
		if idx, ok := byActor[op.By]; ok {
			order[idx].ops = append(order[idx].ops, op)
		} else {
			byActor[op.By] = len(order)
			order = append(order, hunkUnitGroup{actor: op.By, ops: []*ChangeOp{op}})
		}
	}
	return order
}

func unitLetter(index int) string {
	ch := byte('a' + (index % 26))
	return strings.Repeat(string(ch), index/26+1)
}

func subtree(roots []string, children map[string][]string) ([]string, map[string]bool) {
	resultSet := make(map[string]bool)
	var resultOrder []string
	// Match JS stack-pop traversal order in draft.ts:220.
	pending := make([]string, len(roots))
	copy(pending, roots)
	for len(pending) > 0 {
		id := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		if resultSet[id] {
			continue
		}
		resultSet[id] = true
		resultOrder = append(resultOrder, id)
		pending = append(pending, children[id]...)
	}
	return resultOrder, resultSet
}

func resolveScope(spec ScopeSpec, caller string, parentOrder []string, parents map[string]string) ([]string, map[string]bool, error) {
	children := make(map[string][]string)
	for _, id := range parentOrder {
		if p := parents[id]; p != "" {
			children[p] = append(children[p], id)
		}
	}
	if !spec.Set {
		order, set := subtree([]string{caller}, children)
		return order, set, nil
	}
	if len(spec.List) == 0 && spec.Single == "self" {
		return []string{caller}, map[string]bool{caller: true}, nil
	}
	if len(spec.List) == 0 && spec.Single == "*" {
		set := map[string]bool{caller: true}
		order := []string{caller}
		for _, id := range parentOrder {
			if !set[id] {
				set[id] = true
				order = append(order, id)
			}
		}
		return order, set, nil
	}
	var ids []string
	if len(spec.List) > 0 || spec.Single == "" {
		ids = spec.List
	} else {
		ids = []string{spec.Single}
	}
	for _, id := range ids {
		if _, known := parents[id]; id != caller && !known {
			knownStr := "none"
			if len(parentOrder) > 0 {
				knownStr = strings.Join(parentOrder, ", ")
			}
			return nil, nil, fmt.Errorf(`Unknown agent "%s"; agents with recorded changes: %s`, id, knownStr)
		}
	}
	order, set := subtree(ids, children)
	return order, set, nil
}

type recentCommit struct {
	timeMs int64
	parent string
}

func loadRecentCommits(ctx context.Context, repo *gitRepo, head string, byPath map[string][]JournalRecord) ([]recentCommit, error) {
	var since int64 = 1<<62 - 1
	for _, list := range byPath {
		if len(list) > 0 && list[0].At < since {
			since = list[0].At
		}
	}
	var commits []recentCommit
	rev := head
	for rev != "" && len(commits) < recentCommitLimit {
		det, err := repo.commitDetails(ctx, rev)
		if err != nil {
			return nil, err
		}
		if !det.hasDate {
			break
		}
		tMs := det.authorDate.UnixMilli()
		if !(tMs+commitDateSlackMs > since) {
			break
		}
		parent := ""
		if len(det.parents) > 0 {
			parent = det.parents[0]
		}
		commits = append(commits, recentCommit{timeMs: tMs, parent: parent})
		rev = parent
	}
	return commits, nil
}

func headMovedSince(ctx context.Context, repo *gitRepo, head, file string, since int64, recent []recentCommit) bool {
	var lastAfter *recentCommit
	for i := range recent {
		if recent[i].timeMs+commitDateSlackMs > since {
			lastAfter = &recent[i]
		}
	}
	if lastAfter == nil {
		return false
	}
	now, nowOK := repo.resolveRef(ctx, head+":"+file)
	var then string
	var thenOK bool
	if lastAfter.parent != "" {
		then, thenOK = repo.resolveRef(ctx, lastAfter.parent+":"+file)
	}
	if !nowOK {
		now = ""
	}
	if !thenOK {
		then = ""
	}
	return now != then
}

type pendingFile struct {
	inputs []worktreeInput
	finish func(cleaned [][]byte) (*fileChange, error)
}

type loadOutcome struct {
	skip    bool
	file    *fileChange
	pending *pendingFile
}

// CreateDraft builds a draft of the repository's current changes for the agents in scope.
// upstream: draft.ts:269
func CreateDraft(source DraftSource) (*Draft, error) {
	ctx := source.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if isPureJJ(source.Cwd) {
		return nil, errors.New("Selective commits need git; this is a Jujutsu workspace.")
	}
	repo, err := discoverGitRepo(ctx, source.Cwd)
	if err != nil {
		return nil, err
	}
	if repo == nil {
		return nil, fmt.Errorf("Not inside a git repository: %s", source.Cwd)
	}
	root := repo.root
	headSHA, hasHead, err := repo.headSHA(ctx)
	if err != nil {
		return nil, err
	}
	var headPtr *string
	if hasHead {
		h := headSHA
		headPtr = &h
	}
	branch, _ := repo.currentBranch(ctx)

	var records []JournalRecord
	if source.Journal != nil {
		records, err = source.Journal.Read()
		if err != nil {
			return nil, err
		}
	}
	parents := make(map[string]string)
	var parentOrder []string
	for _, rec := range records {
		if _, exists := parents[rec.Agent]; !exists {
			parentOrder = append(parentOrder, rec.Agent)
			parents[rec.Agent] = rec.Parent
		} else if rec.Parent != "" {
			parents[rec.Agent] = rec.Parent
		}
	}
	scopeOrder, scopeSet, err := resolveScope(source.Agents, source.Caller, parentOrder, parents)
	if err != nil {
		return nil, err
	}

	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		realRoot = root
	}
	realDirs := make(map[string]string)
	byPath := make(map[string][]JournalRecord)
	for _, rec := range records {
		if rec.Type != "change" {
			continue
		}
		rel, relErr := filepath.Rel(root, rec.Path)
		if relErr != nil || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
			dir := filepath.Dir(rec.Path)
			realDir, ok := realDirs[dir]
			if !ok {
				if resolved, rErr := filepath.EvalSymlinks(dir); rErr == nil {
					realDir = resolved
				} else {
					realDir = dir
				}
				realDirs[dir] = realDir
			}
			rel, relErr = filepath.Rel(realRoot, filepath.Join(realDir, filepath.Base(rec.Path)))
		}
		if relErr != nil || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
			continue
		}
		rel = filepath.ToSlash(rel)
		byPath[rel] = append(byPath[rel], rec)
	}

	statusText, err := repo.statusPorcelain(ctx)
	if err != nil {
		return nil, err
	}
	staged, err := repo.changedFilesCached(ctx)
	if err != nil {
		return nil, err
	}
	dirty := parseDirtyPathsWithStatus(statusText)
	var tracked []string
	for _, entry := range dirty {
		if !entry.untracked {
			tracked = append(tracked, entry.path)
		}
	}
	headModes := make(map[string]string)
	if hasHead && len(tracked) > 0 {
		treeEntries, err := repo.lsTree(ctx, headSHA, tracked)
		if err != nil {
			return nil, err
		}
		for _, te := range treeEntries {
			headModes[te.path] = te.mode
		}
	}
	var recent []recentCommit
	if hasHead && len(byPath) > 0 {
		recent, err = loadRecentCommits(ctx, repo, headSHA, byPath)
		if err != nil {
			return nil, err
		}
	}

	var files []*fileChange
	var skipped []string

	for start := 0; start < len(dirty); start += readConcurrency {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		end := min(start+readConcurrency, len(dirty))
		chunk := dirty[start:end]
		outcomes := make([]loadOutcome, len(chunk))
		for i, entry := range chunk {
			out, err := loadFile(ctx, repo, root, headPtr, headModes[entry.path], recent, entry.path, byPath[entry.path], source.Journal)
			if err != nil {
				return nil, err
			}
			outcomes[i] = out
		}
		var inputs []worktreeInput
		for _, o := range outcomes {
			if o.pending != nil {
				inputs = append(inputs, o.pending.inputs...)
			}
		}
		var cleaned [][]byte
		if len(inputs) > 0 {
			cleaned, err = repo.worktreeToGit(ctx, inputs)
			if err != nil {
				return nil, err
			}
		}
		offset := 0
		for i, o := range outcomes {
			if o.skip {
				skipped = append(skipped, chunk[i].path)
				continue
			}
			if o.pending != nil {
				n := len(o.pending.inputs)
				fc, err := o.pending.finish(cleaned[offset : offset+n])
				offset += n
				if err != nil {
					return nil, err
				}
				if fc != nil {
					files = append(files, fc)
				}
				continue
			}
			if o.file != nil {
				files = append(files, o.file)
			}
		}
	}

	sort.Slice(files, func(i, j int) bool { return files[i].path < files[j].path })
	for _, fc := range files {
		if fc.attribution != nil {
			fc.classification = Classify(fc.attribution, scopeSet)
			for _, op := range fc.attribution.Ops {
				if fc.classification.Status[op.ID] == OpOwn {
					fc.selected[op.ID] = true
				}
			}
		}
	}

	var agentList []AgentInfo
	seenAgents := make(map[string]bool)
	addAgent := func(id string) {
		if seenAgents[id] {
			return
		}
		seenAgents[id] = true
		running := false
		if source.IsRunning != nil {
			running = source.IsRunning(id)
		}
		agentList = append(agentList, AgentInfo{
			ID:      id,
			Parent:  parents[id],
			InScope: scopeSet[id],
			Running: running,
		})
	}
	addAgent(source.Caller)
	for _, id := range parentOrder {
		addAgent(id)
	}

	draftID := "d" + strconv.FormatInt(time.Now().UnixMilli(), 36) + randomBase36(1296)
	return &Draft{
		ID:        draftID,
		Root:      root,
		Head:      headPtr,
		Branch:    branch,
		Caller:    source.Caller,
		Scope:     scopeOrder,
		scopeSet:  scopeSet,
		Agents:    agentList,
		Staged:    staged,
		Skipped:   skipped,
		Journaled: source.Journal != nil,
		files:     files,
	}, nil
}

func loadFile(
	ctx context.Context,
	repo *gitRepo,
	root string,
	headSHA *string,
	headMode string,
	recent []recentCommit,
	file string,
	records []JournalRecord,
	journal *ChangeJournal,
) (loadOutcome, error) {
	absolute := filepath.Join(root, filepath.FromSlash(file))
	if headMode == "160000" {
		st, err := os.Lstat(absolute)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return loadOutcome{skip: true}, nil
			}
			return loadOutcome{}, err
		}
		if !st.IsDir() {
			return loadOutcome{skip: true}, nil
		}
		modRepo, err := discoverGitRepo(ctx, absolute)
		if err != nil || modRepo == nil {
			return loadOutcome{skip: true}, nil
		}
		realMod, err1 := filepath.EvalSymlinks(modRepo.root)
		realAbs, err2 := filepath.EvalSymlinks(absolute)
		if err1 != nil {
			realMod = modRepo.root
		}
		if err2 != nil {
			realAbs = absolute
		}
		if realMod != realAbs {
			return loadOutcome{skip: true}, nil
		}
		if headSHA == nil {
			return loadOutcome{skip: true}, nil
		}
		before, beforeOK := repo.resolveRef(ctx, *headSHA+":"+file)
		after, afterOK, _ := modRepo.headSHA(ctx)
		if !beforeOK || !afterOK {
			return loadOutcome{skip: true}, nil
		}
		if before == after {
			return loadOutcome{}, nil
		}
		return loadOutcome{
			file: &fileChange{
				path:     file,
				headMode: "160000",
				mode:     "160000",
				selected: make(map[int]bool),
				whole: &wholeInfo{
					reason: wholeReasonGitlink,
					before: &WholeFileState{ID: before, Mode: "160000"},
					after:  &WholeFileState{ID: after, Mode: "160000"},
				},
			},
		}, nil
	}

	var workBytes []byte
	var hasWork bool
	var mode string
	st, err := os.Lstat(absolute)
	if err == nil {
		if st.Mode()&os.ModeSymlink != 0 {
			mode = symlinkMode
			target, rerr := os.Readlink(absolute)
			if rerr != nil {
				return loadOutcome{}, rerr
			}
			workBytes = []byte(target)
			hasWork = true
		} else if !st.Mode().IsRegular() {
			return loadOutcome{skip: true}, nil
		} else {
			if st.Mode().Perm()&0o111 != 0 {
				mode = "100755"
			} else {
				mode = "100644"
			}
			b, rerr := os.ReadFile(absolute)
			if rerr != nil {
				return loadOutcome{}, rerr
			}
			workBytes = b
			hasWork = true
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return loadOutcome{}, err
	}

	if headMode == "" && mode == "" {
		return loadOutcome{}, nil
	}

	var editors []string
	seenEditors := make(map[string]bool)
	for _, rec := range records {
		if !seenEditors[rec.Agent] {
			seenEditors[rec.Agent] = true
			editors = append(editors, rec.Agent)
		}
	}

	makeWhole := func(reason wholeReason, stored []byte, hasStored bool) (*fileChange, error) {
		var before *WholeFileState
		if headSHA != nil && headMode != "" {
			id, ok := repo.resolveRef(ctx, *headSHA+":"+file)
			if !ok {
				return nil, fmt.Errorf("Could not resolve %s at draft HEAD %s.", file, *headSHA)
			}
			before = &WholeFileState{ID: id, Mode: headMode}
		}
		var after *WholeFileState
		if mode != "" {
			if !hasStored {
				return nil, fmt.Errorf("Could not capture %s for the draft.", file)
			}
			id, err := repo.writeBlob(ctx, stored)
			if err != nil {
				return nil, err
			}
			after = &WholeFileState{ID: id, Mode: mode}
		}
		return &fileChange{
			path:     file,
			headMode: headMode,
			mode:     mode,
			selected: make(map[int]bool),
			editors:  editors,
			whole:    &wholeInfo{reason: reason, before: before, after: after},
		}, nil
	}

	if headMode == symlinkMode || mode == symlinkMode {
		stored := workBytes
		hasStored := hasWork
		if mode != symlinkMode && hasWork {
			cleaned, err := repo.worktreeToGit(ctx, []worktreeInput{{path: file, data: workBytes}})
			if err != nil {
				return loadOutcome{}, err
			}
			stored = cleaned[0]
		}
		fc, err := makeWhole(wholeReasonSymlink, stored, hasStored)
		if err != nil {
			return loadOutcome{}, err
		}
		return loadOutcome{file: fc}, nil
	}

	var headBytes []byte
	hasHeadBytes := false
	if headSHA != nil && headMode != "" {
		b, err := repo.showBlob(ctx, *headSHA+":"+file)
		if err != nil {
			return loadOutcome{}, err
		}
		headBytes = b
		hasHeadBytes = true
	}

	var snapshots []RecordedWrite
	hasSnapshots := false
	if journal != nil && len(records) > 0 {
		s, ok, err := journal.Snapshots(records)
		if err != nil {
			return loadOutcome{}, err
		}
		if ok {
			snapshots = s
			hasSnapshots = true
		}
	}
	headMoved := false
	if headSHA != nil && headMode != "" && len(records) > 0 {
		headMoved = headMovedSince(ctx, repo, *headSHA, file, records[0].At, recent)
	}

	var inputs []worktreeInput
	if hasWork {
		inputs = append(inputs, worktreeInput{path: file, data: workBytes})
	}
	if hasSnapshots {
		for _, snap := range snapshots {
			if snap.Before != nil {
				inputs = append(inputs, worktreeInput{path: file, data: []byte(*snap.Before)})
			}
			if snap.After != nil {
				inputs = append(inputs, worktreeInput{path: file, data: []byte(*snap.After)})
			}
		}
	}

	return loadOutcome{
		pending: &pendingFile{
			inputs: inputs,
			finish: func(cleaned [][]byte) (*fileChange, error) {
				next := 0
				var gitForm []byte
				hasGitForm := false
				if hasWork {
					gitForm = cleaned[next]
					next++
					hasGitForm = true
				}
				snapshotLines := func(text *string) []string {
					if text == nil {
						return nil
					}
					s := string(cleaned[next])
					next++
					return ToLines(&s)
				}
				var headText *string
				if hasHeadBytes {
					s, ok := decodeText(headBytes)
					if !ok {
						return makeWhole(wholeReasonContent, gitForm, hasGitForm)
					}
					headText = &s
				}
				var workText *string
				if hasGitForm {
					s, ok := decodeText(gitForm)
					if !ok {
						return makeWhole(wholeReasonContent, gitForm, hasGitForm)
					}
					workText = &s
				}
				if equalStringPtr(headText, workText) {
					if headMode == mode {
						return nil, nil
					}
					return makeWhole(wholeReasonMode, gitForm, hasGitForm)
				}
				headLines := ToLines(headText)
				workLines := ToLines(workText)
				var edits []RecordedEdit
				if hasSnapshots {
					edits = make([]RecordedEdit, len(snapshots))
					for i, snap := range snapshots {
						edits[i] = RecordedEdit{
							Actor:  snap.Agent,
							Before: snapshotLines(snap.Before),
							After:  snapshotLines(snap.After),
						}
					}
				}
				steps := HistorySteps(headLines, edits, workLines)
				attr, err := Attribute(headLines, steps, workLines, headMoved)
				if err != nil {
					return nil, err
				}
				fc := &fileChange{
					path:     file,
					head:     headText,
					work:     workText,
					headMode: headMode,
					mode:     mode,
					selected: make(map[int]bool),
					editors:  editors,
				}
				fc.counts = countOps(fc, attr, attr.Ops)
				if hasSnapshots {
					fc.attribution = attr
				}
				return fc, nil
			},
		},
	}, nil
}

func countOps(fc *fileChange, attr *Attribution, ops []*ChangeOp) lineCounts {
	var newline *ChangeOp
	if (fc.head == nil || fc.work == nil) && len(attr.Ops) > 0 {
		last := attr.Ops[len(attr.Ops)-1]
		if last.Text == "" {
			newline = last
		}
	}
	var c lineCounts
	for _, op := range ops {
		if op == newline {
			continue
		}
		if op.Kind == "+" {
			c.added++
		} else {
			c.removed++
		}
	}
	return c
}

func lineAttribution(fc *fileChange) *Attribution {
	if fc.attribution != nil {
		return fc.attribution
	}
	head := ToLines(fc.head)
	work := ToLines(fc.work)
	attr, _ := Attribute(head, HistorySteps(head, nil, work), work, false)
	return attr
}

func modeChangeText(fc *fileChange) string {
	if fc.headMode != "" && fc.mode != "" && fc.headMode != fc.mode {
		return fmt.Sprintf(" mode %s → %s", fc.headMode, fc.mode)
	}
	return ""
}

func wholeLabel(fc *fileChange) string {
	switch fc.whole.reason {
	case wholeReasonGitlink:
		var beforeID, afterID string
		if fc.whole.before != nil {
			beforeID = fc.whole.before.ID
		}
		if fc.whole.after != nil {
			afterID = fc.whole.after.ID
		}
		return fmt.Sprintf("gitlink %s → %s", beforeID, afterID)
	case wholeReasonSymlink:
		return "symlink"
	case wholeReasonMode:
		return "file," + modeChangeText(fc)
	default:
		return "binary, non-UTF-8, or large file"
	}
}

func formatCounts(c lineCounts) string {
	var parts []string
	if c.added > 0 {
		parts = append(parts, fmt.Sprintf("+%d", c.added))
	}
	if c.removed > 0 {
		parts = append(parts, fmt.Sprintf("−%d", c.removed))
	}
	if len(parts) == 0 {
		return "±0"
	}
	return strings.Join(parts, " ")
}

// Paths returns the changed paths in the draft.
// upstream: draft.ts:667
func (d *Draft) Paths() []string {
	out := make([]string, len(d.files))
	for i, f := range d.files {
		out[i] = f.path
	}
	return out
}

// Selection returns the exact content to commit per path with anything selected.
// upstream: draft.ts:672
func (d *Draft) Selection() []SelectedChange {
	var changes []SelectedChange
	for _, fc := range d.files {
		if fc.whole != nil {
			if !fc.wholeSelected {
				continue
			}
			if fc.whole.reason == wholeReasonGitlink {
				var before, after *FileState
				if fc.whole.before != nil {
					before = &FileState{Text: fmt.Sprintf("Subproject commit %s\n", fc.whole.before.ID), Mode: fc.whole.before.Mode}
				}
				if fc.whole.after != nil {
					after = &FileState{Text: fmt.Sprintf("Subproject commit %s\n", fc.whole.after.ID), Mode: fc.whole.after.Mode}
				}
				changes = append(changes, SelectedChange{
					Kind:       "text",
					Path:       fc.path,
					BeforeText: before,
					AfterText:  after,
				})
			} else {
				changes = append(changes, SelectedChange{
					Kind:        "whole",
					Path:        fc.path,
					BeforeWhole: fc.whole.before,
					AfterWhole:  fc.whole.after,
				})
			}
			continue
		}
		if len(fc.selected) == 0 {
			continue
		}
		content := FromLines(BuildContent(lineAttribution(fc), fc.selected))
		mode := fc.mode
		if mode == "" {
			mode = fc.headMode
		}
		var before *FileState
		if fc.head != nil && fc.headMode != "" {
			before = &FileState{Text: *fc.head, Mode: fc.headMode}
		}
		var after *FileState
		if content != nil && mode != "" {
			after = &FileState{Text: *content, Mode: mode}
		}
		if (before == nil && after == nil) || (before != nil && after != nil && before.Text == after.Text && before.Mode == after.Mode) {
			continue
		}
		changes = append(changes, SelectedChange{
			Kind:       "text",
			Path:       fc.path,
			BeforeText: before,
			AfterText:  after,
		})
	}
	return changes
}

// Take selects changes and returns a one-line report.
// upstream: draft.ts:704
func (d *Draft) Take(target SelectionTarget) (string, error) {
	return d.selectTarget(target, true)
}

// Drop deselects changes and returns a one-line report.
// upstream: draft.ts:709
func (d *Draft) Drop(target SelectionTarget) (string, error) {
	return d.selectTarget(target, false)
}

// Only deselects every path matching none of globs.
// upstream: draft.ts:717
func (d *Draft) Only(globs []string) (string, error) {
	if len(globs) == 0 {
		return "", errors.New("Name at least one path or glob to keep.")
	}
	keep := make(map[*fileChange]bool)
	var unmatched []string
	for _, g := range globs {
		matched, err := d.resolve(g)
		if err != nil {
			return "", err
		}
		if len(matched) == 0 {
			unmatched = append(unmatched, g)
		}
		for _, fc := range matched {
			keep[fc] = true
		}
	}
	if len(keep) == 0 {
		return "", fmt.Errorf("No changed path matches any keep target: %s.", strings.Join(unmatched, ", "))
	}
	dropped := 0
	for _, fc := range d.files {
		if keep[fc] || (len(fc.selected) == 0 && !fc.wholeSelected) {
			continue
		}
		clear(fc.selected)
		fc.wholeSelected = false
		dropped++
	}
	report := fmt.Sprintf("Deselected %d path(s) outside %s.", dropped, strings.Join(globs, ", "))
	if len(unmatched) > 0 {
		return fmt.Sprintf("%s Unmatched: %s.", report, strings.Join(unmatched, ", ")), nil
	}
	return report, nil
}

func (d *Draft) selectTarget(target SelectionTarget, take bool) (string, error) {
	if target.Path == nil && target.Grep == nil {
		return "", errors.New("Name a path, units, or grep pattern.")
	}
	if len(target.Units) > 0 && target.Path == nil {
		return "", errors.New("Units need a path.")
	}
	var files []*fileChange
	var err error
	if target.Path == nil {
		files = d.files
	} else {
		files, err = d.match(*target.Path)
		if err != nil {
			return "", err
		}
	}
	var pattern *regexp.Regexp
	if target.Grep != nil {
		pattern, err = regexp.Compile(*target.Grep)
		if err != nil {
			return "", err
		}
	}
	type planEntry struct {
		file  *fileChange
		whole bool
		ops   []*ChangeOp
	}
	plans := make([]planEntry, len(files))
	for i, fc := range files {
		ops, isWhole, err := d.targetOps(fc, target.Units, pattern)
		if err != nil {
			return "", err
		}
		plans[i] = planEntry{file: fc, whole: isWhole, ops: ops}
	}
	touched := 0
	anyNonWhole := false
	for _, p := range plans {
		if p.whole {
			if p.file.wholeSelected != take {
				touched++
			}
			p.file.wholeSelected = take
			continue
		}
		anyNonWhole = true
		for _, op := range p.ops {
			if p.file.selected[op.ID] == take {
				continue
			}
			if take {
				p.file.selected[op.ID] = true
			} else {
				delete(p.file.selected, op.ID)
			}
			touched++
		}
	}
	if pattern != nil && touched == 0 && !anyNonWhole {
		return "", fmt.Errorf("No change line matches /%s/.", *target.Grep)
	}
	verb := "Deselected"
	if take {
		verb = "Selected"
	}
	return fmt.Sprintf("%s %d change line(s).", verb, touched), nil
}

func (d *Draft) targetOps(fc *fileChange, units []string, pattern *regexp.Regexp) ([]*ChangeOp, bool, error) {
	if fc.whole != nil {
		if len(units) > 0 {
			for _, u := range units {
				if u != "1" && u != "1a" {
					return nil, false, fmt.Errorf("%s is a whole-file change; it has only unit 1.", fc.path)
				}
			}
		}
		return nil, true, nil
	}
	attr := lineAttribution(fc)
	var ops []*ChangeOp
	wanted := make(map[string]bool, len(units))
	for _, u := range units {
		wanted[u] = true
	}
	known := make(map[string]bool)
	for hunkIndex, hunk := range attr.Hunks {
		hunkNum := strconv.Itoa(hunkIndex + 1)
		known[hunkNum] = true
		groups := hunkUnits(hunk)
		for unitIndex, group := range groups {
			id := hunkNum + unitLetter(unitIndex)
			known[id] = true
			if len(wanted) > 0 && !wanted[hunkNum] && !wanted[id] {
				continue
			}
			if pattern != nil {
				matched := false
				for _, op := range group.ops {
					if pattern.MatchString(op.Text) {
						matched = true
						break
					}
				}
				if !matched {
					continue
				}
			}
			ops = append(ops, group.ops...)
		}
	}
	if len(units) > 0 {
		for _, u := range units {
			if !known[u] {
				return nil, false, fmt.Errorf(`%s has no unit "%s" (hunks 1-%d).`, fc.path, u, len(attr.Hunks))
			}
		}
	}
	return ops, false, nil
}

func hasGlobChars(s string) bool {
	return strings.ContainsAny(s, "*?[{")
}

func (d *Draft) match(target string) ([]*fileChange, error) {
	matched, err := d.resolve(target)
	if err != nil {
		return nil, err
	}
	if len(matched) == 0 {
		label := fmt.Sprintf(`"%s"`, target)
		if hasGlobChars(target) {
			label = target
		}
		return nil, fmt.Errorf("No changed path matches %s.", label)
	}
	return matched, nil
}

func (d *Draft) resolve(target string) ([]*fileChange, error) {
	for _, fc := range d.files {
		if fc.path == target {
			return []*fileChange{fc}, nil
		}
	}
	if hasGlobChars(target) {
		var out []*fileChange
		for _, fc := range d.files {
			if matchGlob(target, fc.path) {
				out = append(out, fc)
			}
		}
		return out, nil
	}
	suffix := "/" + target
	var matches []*fileChange
	for _, fc := range d.files {
		if strings.HasSuffix(fc.path, suffix) {
			matches = append(matches, fc)
		}
	}
	if len(matches) > 1 {
		paths := make([]string, len(matches))
		for i, m := range matches {
			paths[i] = m.path
		}
		return nil, fmt.Errorf(`"%s" is ambiguous: %s`, target, strings.Join(paths, ", "))
	}
	return matches, nil
}

// matchGlob matches POSIX-style globs including **, *, ?, [...], {a,b} (matching Bun.Glob).
func matchGlob(pattern, name string) bool {
	re, err := compileGlobRegex(pattern)
	if err != nil {
		return false
	}
	return re.MatchString(name)
}

func compileGlobRegex(pattern string) (*regexp.Regexp, error) {
	var b strings.Builder
	b.WriteString("^")
	i := 0
	n := len(pattern)
	for i < n {
		c := pattern[i]
		switch c {
		case '*':
			if i+1 < n && pattern[i+1] == '*' {
				if i+2 < n && pattern[i+2] == '/' {
					b.WriteString("(?:.*/)?")
					i += 3
				} else {
					b.WriteString(".*")
					i += 2
				}
			} else {
				b.WriteString("[^/]*")
				i++
			}
		case '?':
			b.WriteString("[^/]")
			i++
		case '[':
			j := i + 1
			if j < n && (pattern[j] == '!' || pattern[j] == '^') {
				j++
			}
			if j < n && pattern[j] == ']' {
				j++
			}
			for j < n && pattern[j] != ']' {
				j++
			}
			if j >= n {
				b.WriteString(`\[`)
				i++
			} else {
				inner := pattern[i+1 : j]
				if strings.HasPrefix(inner, "!") {
					inner = "^" + inner[1:]
				}
				b.WriteByte('[')
				b.WriteString(inner)
				b.WriteByte(']')
				i = j + 1
			}
		case '{':
			end := findClosingBrace(pattern, i)
			if end < 0 {
				b.WriteString(`\{`)
				i++
			} else {
				alts := splitBraceAlts(pattern[i+1 : end])
				b.WriteString("(?:")
				for k, alt := range alts {
					if k > 0 {
						b.WriteByte('|')
					}
					sub, err := compileGlobInner(alt)
					if err != nil {
						return nil, err
					}
					b.WriteString(sub)
				}
				b.WriteByte(')')
				i = end + 1
			}
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
			i++
		}
	}
	b.WriteString("$")
	return regexp.Compile(b.String())
}

func compileGlobInner(pattern string) (string, error) {
	re, err := compileGlobRegex(pattern)
	if err != nil {
		return "", err
	}
	s := re.String()
	return s[1 : len(s)-1], nil
}

func findClosingBrace(s string, open int) int {
	depth := 0
	for i := open; i < len(s); i++ {
		if s[i] == '{' {
			depth++
		} else if s[i] == '}' {
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

func splitBraceAlts(s string) []string {
	var alts []string
	depth := 0
	start := 0
	for i := range len(s) {
		switch s[i] {
		case '{':
			depth++
		case '}':
			depth--
		case ',':
			if depth == 0 {
				alts = append(alts, s[start:i])
				start = i + 1
			}
		}
	}
	alts = append(alts, s[start:])
	return alts
}

// Summary renders the draft summary shown on draft creation and after selection updates.
// upstream: draft.ts:832
func (d *Draft) Summary() string {
	var running []string
	for _, a := range d.Agents {
		if a.InScope && a.Running {
			running = append(running, a.ID)
		}
	}
	headStr := "(no commits yet)"
	if d.Head != nil {
		headStr = *d.Head
		if len(headStr) > 10 {
			headStr = headStr[:10]
		}
	}
	branchPart := ""
	if d.Branch != "" {
		branchPart = " on " + d.Branch
	}
	runningPart := ""
	if len(running) > 0 {
		runningPart = fmt.Sprintf(" (still running: %s)", strings.Join(running, ", "))
	}
	lines := []string{
		fmt.Sprintf("HEAD %s%s · scope: %s%s", headStr, branchPart, strings.Join(d.Scope, ", "), runningPart),
	}
	if !d.Journaled {
		lines = append(lines, "No change journal: this session keeps no artifacts, so nothing is attributed; select with take().")
	}

	var selected []string
	var review []string
	var otherAgentOrder []string
	otherAgents := make(map[string]int)
	var unrecorded []*fileChange
	for _, fc := range d.files {
		desc := d.describeFile(fc)
		if desc.selected != "" {
			selected = append(selected, desc.selected)
		}
		review = append(review, desc.review...)
		for _, agent := range desc.outOfScopeOrder {
			if _, ok := otherAgents[agent]; !ok {
				otherAgentOrder = append(otherAgentOrder, agent)
			}
			otherAgents[agent]++
		}
		if desc.selected == "" && len(desc.review) == 0 && len(desc.outOfScopeOrder) == 0 {
			unrecorded = append(unrecorded, fc)
		}
	}

	if len(selected) > 0 {
		lines = append(lines, "Selected:")
		lines = append(lines, selected...)
	} else {
		lines = append(lines, "Selected: nothing")
	}
	if len(review) > 0 {
		lines = append(lines, "Review:")
		lines = append(lines, review...)
	}
	if len(otherAgentOrder) > 0 {
		parts := make([]string, len(otherAgentOrder))
		for i, agent := range otherAgentOrder {
			runTag := ""
			for _, a := range d.Agents {
				if a.ID == agent && a.Running {
					runTag = ", running"
					break
				}
			}
			parts[i] = fmt.Sprintf("%s (%d path(s)%s)", agent, otherAgents[agent], runTag)
		}
		lines = append(lines, "Other agents of this session, not in scope: "+strings.Join(parts, ", "))
	}
	if len(unrecorded) > 0 {
		lines = append(lines, fmt.Sprintf("Unrecorded: %d path(s) changed by other programs or by hand; take() the ones your commands produced.", len(unrecorded)))
		limit := min(len(unrecorded), unrecordedListLimit)
		for _, fc := range unrecorded[:limit] {
			var size string
			if fc.whole != nil {
				size = "whole " + wholeLabel(fc)
			} else {
				size = formatCounts(fc.counts) + modeChangeText(fc)
			}
			lines = append(lines, fmt.Sprintf("  · %s  %s", fc.path, size))
		}
		if len(unrecorded) > unrecordedListLimit {
			lines = append(lines, fmt.Sprintf("  … %d more", len(unrecorded)-unrecordedListLimit))
		}
	}
	var tail []string
	if len(d.Staged) > 0 {
		tail = append(tail, fmt.Sprintf("Staged by others: %d path(s), left staged.", len(d.Staged)))
	}
	if len(d.Skipped) > 0 {
		tail = append(tail, fmt.Sprintf("Skipped: %s (submodule or nested repository).", strings.Join(d.Skipped, ", ")))
	}
	if len(tail) > 0 {
		lines = append(lines, strings.Join(tail, " "))
	}
	return strings.Join(lines, "\n")
}

type fileDescription struct {
	selected        string
	review          []string
	outOfScopeOrder []string
}

func (d *Draft) describeFile(fc *fileChange) fileDescription {
	outOfScopeSet := make(map[string]bool)
	var outOfScopeOrder []string
	addOutOfScope := func(agent string) {
		if !outOfScopeSet[agent] {
			outOfScopeSet[agent] = true
			outOfScopeOrder = append(outOfScopeOrder, agent)
		}
	}
	for _, agent := range fc.editors {
		if !d.scopeSet[agent] {
			addOutOfScope(agent)
		}
	}
	if fc.whole != nil {
		label := wholeLabel(fc)
		if fc.wholeSelected {
			return fileDescription{
				selected:        fmt.Sprintf("  ✓ %s  whole %s", fc.path, label),
				outOfScopeOrder: outOfScopeOrder,
			}
		}
		var review []string
		if reason := d.wholeReviewReason(fc); reason != "" {
			review = append(review, fmt.Sprintf("  ? %s  whole %s; %s", fc.path, label, reason))
		}
		return fileDescription{review: review, outOfScopeOrder: outOfScopeOrder}
	}

	var attr *Attribution
	if fc.attribution != nil {
		attr = fc.attribution
	} else if len(fc.selected) > 0 {
		attr = lineAttribution(fc)
	}
	var ops []*ChangeOp
	var hunks []*Hunk
	if attr != nil {
		ops = attr.Ops
		hunks = attr.Hunks
	}
	var status []OpStatus
	if fc.classification != nil {
		status = fc.classification.Status
	}
	var chosen []*ChangeOp
	for _, op := range ops {
		if fc.selected[op.ID] {
			chosen = append(chosen, op)
		}
	}
	var review []string
	for index, hunk := range hunks {
		var reviewOps []*ChangeOp
		for _, op := range hunk.Ops {
			if op.ID < len(status) && status[op.ID] == OpReview && !fc.selected[op.ID] {
				reviewOps = append(reviewOps, op)
			}
		}
		if len(reviewOps) == 0 {
			continue
		}
		var reasons []string
		seenReasons := make(map[string]bool)
		for _, op := range reviewOps {
			if fc.classification != nil {
				if r, ok := fc.classification.Reasons[op.ID]; ok && r.Kind != "hunk" {
					desc := describeReason(r)
					if !seenReasons[desc] {
						seenReasons[desc] = true
						reasons = append(reasons, desc)
					}
				}
			}
		}
		reasonText := "needs review"
		if len(reasons) > 0 {
			reasonText = strings.Join(reasons, "; ")
		}
		review = append(review, fmt.Sprintf("  ? %s  hunk %d: %s", fc.path, index+1, reasonText))
	}
	if fc.attribution == nil && len(chosen) == 0 {
		hasInScopeEditor := false
		for _, a := range fc.editors {
			if d.scopeSet[a] {
				hasInScopeEditor = true
				break
			}
		}
		if hasInScopeEditor {
			review = append(review, fmt.Sprintf("  ? %s  %s%s; edited by %s, but the edit record cannot be replayed",
				fc.path, formatCounts(fc.counts), modeChangeText(fc), strings.Join(fc.editors, ", ")))
		}
	}
	for _, op := range ops {
		if op.ID < len(status) && status[op.ID] == OpOther && !IsPseudoActor(op.By) && !d.scopeSet[op.By] {
			addOutOfScope(op.By)
		}
	}
	if attr == nil || len(chosen) == 0 {
		return fileDescription{review: review, outOfScopeOrder: outOfScopeOrder}
	}
	var leftOut []*ChangeOp
	for _, op := range ops {
		if !fc.selected[op.ID] {
			leftOut = append(leftOut, op)
		}
	}
	var owners []string
	seenOwners := make(map[string]bool)
	for _, op := range chosen {
		if !seenOwners[op.By] {
			seenOwners[op.By] = true
			owners = append(owners, actorName(op.By))
		}
	}
	mark := "✓"
	if len(leftOut) > 0 {
		mark = "◐"
	}
	line := fmt.Sprintf("  %s %s  %s%s", mark, fc.path, formatCounts(countOps(fc, attr, chosen)), modeChangeText(fc))
	if len(leftOut) > 0 {
		var others []string
		seenOthers := make(map[string]bool)
		for _, op := range leftOut {
			name := actorName(op.By)
			if !seenOthers[name] {
				seenOthers[name] = true
				others = append(others, name)
			}
		}
		line += fmt.Sprintf(" of %s; left out: %s", formatCounts(fc.counts), strings.Join(others, ", "))
	}
	return fileDescription{
		selected:        fmt.Sprintf("%s  [%s]", line, strings.Join(owners, ", ")),
		review:          review,
		outOfScopeOrder: outOfScopeOrder,
	}
}

// Show prints hunks of target with unit ids, selection marks, and owners;
// when target is nil, every file that is partially selected or needs review.
// upstream: draft.ts:937
func (d *Draft) Show(target *string) (string, error) {
	var files []*fileChange
	var err error
	if target == nil {
		for _, fc := range d.files {
			if d.needsAttention(fc) {
				files = append(files, fc)
			}
		}
	} else {
		files, err = d.match(*target)
		if err != nil {
			return "", err
		}
	}
	if len(files) == 0 {
		return "Nothing is partially selected or needs review.", nil
	}
	parts := make([]string, len(files))
	for i, fc := range files {
		parts[i] = d.renderFile(fc)
	}
	return strings.Join(parts, "\n\n"), nil
}

func (d *Draft) needsAttention(fc *fileChange) bool {
	if len(fc.selected) > 0 && fc.whole == nil && len(fc.selected) < len(lineAttribution(fc).Ops) {
		return true
	}
	return len(d.describeFile(fc).review) > 0
}

func (d *Draft) wholeReviewReason(fc *fileChange) string {
	for _, agent := range fc.editors {
		if d.scopeSet[agent] {
			return "edited by " + strings.Join(fc.editors, ", ")
		}
	}
	if fc.whole != nil && fc.whole.reason == wholeReasonGitlink {
		return "select explicitly to update the submodule pointer"
	}
	return ""
}

func (d *Draft) renderFile(fc *fileChange) string {
	if fc.whole != nil {
		var reason string
		if !fc.wholeSelected {
			reason = d.wholeReviewReason(fc)
		}
		mark := "·"
		if fc.wholeSelected {
			mark = "✓"
		} else if reason != "" {
			mark = "?"
		}
		suffix := ""
		if reason != "" {
			suffix = "; " + reason
		}
		return fmt.Sprintf("%s  whole-file change (%s)%s\n  1 %s whole file", fc.path, wholeLabel(fc), suffix, mark)
	}
	attr := lineAttribution(fc)
	var status []OpStatus
	if fc.classification != nil {
		status = fc.classification.Status
	}
	var chosen []*ChangeOp
	for _, op := range attr.Ops {
		if fc.selected[op.ID] {
			chosen = append(chosen, op)
		}
	}
	lines := []string{
		fmt.Sprintf("%s  %s of %s selected%s", fc.path, formatCounts(countOps(fc, attr, chosen)), formatCounts(fc.counts), modeChangeText(fc)),
	}
	for hunkIndex, hunk := range attr.Hunks {
		headStart := hunk.HeadStart
		if hunk.HeadCount > 0 {
			headStart++
		}
		workStart := hunk.WorkStart
		if hunk.WorkCount > 0 {
			workStart++
		}
		lines = append(lines, fmt.Sprintf("@@ -%d,%d +%d,%d @@  hunk %d", headStart, hunk.HeadCount, workStart, hunk.WorkCount, hunkIndex+1))
		groups := hunkUnits(hunk)
		unitOf := make(map[int]string, len(hunk.Ops))
		for unitIndex, group := range groups {
			id := fmt.Sprintf("%d%s", hunkIndex+1, unitLetter(unitIndex))
			for _, op := range group.ops {
				unitOf[op.ID] = id
			}
		}
		header := ""
		for _, op := range hunk.Ops {
			mark := "·"
			if fc.selected[op.ID] {
				mark = "✓"
			} else if op.ID < len(status) && status[op.ID] == OpReview {
				mark = "?"
			}
			next := fmt.Sprintf("  %s %s %s", unitOf[op.ID], mark, actorName(op.By))
			if next != header {
				header = next
				lines = append(lines, header)
			}
			lines = append(lines, fmt.Sprintf("    %s%s", op.Kind, displayLine(op.Text)))
		}
		var reasons []string
		seenReasons := make(map[string]bool)
		for _, op := range hunk.Ops {
			if fc.classification != nil {
				if r, ok := fc.classification.Reasons[op.ID]; ok && r.Kind != "hunk" {
					desc := describeReason(r)
					if !seenReasons[desc] {
						seenReasons[desc] = true
						reasons = append(reasons, desc)
					}
				}
			}
		}
		for _, r := range reasons {
			lines = append(lines, "  ? review: "+r)
		}
	}
	return strings.Join(lines, "\n")
}

// Diff renders the candidate commit as a unified patch against the draft's HEAD.
// upstream: draft.ts:1002
func (d *Draft) Diff() string {
	sel := d.Selection()
	if len(sel) == 0 {
		return "Nothing selected."
	}
	var b strings.Builder
	for _, ch := range sel {
		if ch.Kind == "text" {
			b.WriteString(FormatTextPatch(ch.Path, ch.BeforeText, ch.AfterText))
		} else {
			fmt.Fprintf(&b, "diff --git a/%s b/%s\n(whole-file change; content captured at draft time)\n", ch.Path, ch.Path)
		}
	}
	if b.Len() == 0 {
		return "Nothing selected."
	}
	return b.String()
}
