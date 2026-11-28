package commit

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// upstream: packages/coding-agent/src/commit/selective/apply.ts

const (
	indexWriteAttempts  = 5
	indexWriteBackoffMs = 40
	headRaceAttempts    = 3
)

// CommitRequest specifies the message and options of a selective commit.
// upstream: apply.ts:90
type CommitRequest struct {
	Subject  string
	Body     string   // already normalized string (joined if array)
	Fixes    []string // formatted as strings (numbers converted to decimal strings)
	Amend    bool
	NoVerify bool
}

// CommitOutcome is the result of CommitSelection.
// upstream: apply.ts:104
type CommitOutcome struct {
	SHA        string   `json:"sha"`
	Subject    string   `json:"subject"`
	Paths      []string `json:"paths"`
	Parent     *string  `json:"parent"`
	Amended    string   `json:"amended,omitempty"`
	IndexNotes []string `json:"indexNotes"`
}

// CommitContext supplies journal, agent identity, and cancellation for CommitSelection.
type CommitContext struct {
	Journal *ChangeJournal
	Agent   JournalAgent
	Ctx     context.Context
	// testOnCommitDetails is an optional test hook mirroring upstream's vi.spyOn(vcs, "requireGit")
	// commitDetails hook in commit-selective-flow.test.ts:1212.
	testOnCommitDetails func(sha string, ctx context.Context)
}

var digitsOnlyRE = regexp.MustCompile(`^\d+$`)

// ComposeMessage builds a commit message with single-line subject, blank line, body, and Fixes trailers.
// upstream: apply.ts:124
func ComposeMessage(req CommitRequest) (string, error) {
	subject := strings.TrimSpace(req.Subject)
	if subject == "" {
		return "", errors.New("Commit subject is empty.")
	}
	if strings.ContainsRune(subject, '\n') {
		return "", errors.New("Commit subject must be one line; put details in `body`.")
	}
	body := strings.TrimSpace(req.Body)
	var fixLines []string
	for _, ref := range req.Fixes {
		if digitsOnlyRE.MatchString(ref) {
			fixLines = append(fixLines, "Fixes #"+ref)
		} else {
			fixLines = append(fixLines, "Fixes "+ref)
		}
	}
	fixes := strings.Join(fixLines, "\n")
	var parts []string
	parts = append(parts, subject)
	if body != "" {
		parts = append(parts, body)
	}
	if fixes != "" {
		parts = append(parts, fixes)
	}
	return strings.Join(parts, "\n\n") + "\n", nil
}

func shortSHA(sha *string) string {
	if sha == nil || *sha == "" {
		return "(no commits yet)"
	}
	if len(*sha) > 10 {
		return (*sha)[:10]
	}
	return *sha
}

// shortenHomePath replaces a leading home directory with "~" (matching packages/tui/src/render/render-utils.ts:973).
func shortenHomePath(p string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return p
	}
	cleanHome := filepath.Clean(home)
	cleanP := filepath.Clean(p)
	if cleanP == cleanHome {
		return "~"
	}
	prefix := cleanHome + string(filepath.Separator)
	if strings.HasPrefix(cleanP, prefix) {
		rest := cleanP[len(cleanHome):]
		return "~" + filepath.ToSlash(rest)
	}
	return p
}

type pathPatch struct {
	path  string
	patch string
}

type indexSyncNote struct {
	path string
	note string
}

func applyToSharedIndex(ctx context.Context, repo *gitRepo, patch string) (string, bool) {
	failure := ""
	for attempt := range indexWriteAttempts {
		if attempt > 0 {
			canApply, err := repo.canApplyPatchCached(ctx, patch, "", false)
			if err == nil && !canApply {
				return failure, false
			}
			delay := time.Duration(indexWriteBackoffMs<<(attempt-1)) * time.Millisecond
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return ctx.Err().Error(), false
			}
		}
		err := repo.applyPatchCached(ctx, patch, "", false)
		if err == nil {
			return "", true
		}
		failure = err.Error()
	}
	return failure, false
}

func syncSharedIndex(ctx context.Context, repo *gitRepo, patches []pathPatch) []indexSyncNote {
	raw := make([]string, len(patches))
	for i, p := range patches {
		raw[i] = p.patch
	}
	joined := joinPatches(raw)
	if _, ok := applyToSharedIndex(ctx, repo, joined); ok {
		return nil
	}
	var notes []indexSyncNote
	for _, entry := range patches {
		failure, ok := applyToSharedIndex(ctx, repo, entry.patch)
		if ok {
			continue
		}
		if revOK, err := repo.canApplyPatchCached(ctx, entry.patch, "", true); err == nil && revOK {
			continue
		}
		stagedOK, stagedErr := repo.canApplyPatchCached(ctx, entry.patch, "", false)
		var note string
		if stagedErr == nil && !stagedOK {
			note = fmt.Sprintf(
				"%s: its shared-index entry holds someone else's staged changes and was left as is; "+
					"`git diff --cached` there shows this commit reverted until they restage.",
				entry.path,
			)
		} else {
			note = fmt.Sprintf(
				"%s: the shared index could not be written (%s); "+
					"`git diff --cached` there shows this commit reverted until `git reset -q -- %s`.",
				entry.path, failure, entry.path,
			)
		}
		notes = append(notes, indexSyncNote{path: entry.path, note: note})
	}
	return notes
}

func patchForChange(ctx context.Context, repo *gitRepo, ch SelectedChange) (string, error) {
	if ch.Kind == "text" {
		if ch.AfterText != nil && ch.AfterText.Mode == "160000" {
			abs := filepath.Join(repo.root, filepath.FromSlash(ch.Path))
			modRepo, err := discoverGitRepo(ctx, abs)
			sameRoot := false
			if err == nil && modRepo != nil {
				r1, e1 := filepath.EvalSymlinks(modRepo.root)
				r2, e2 := filepath.EvalSymlinks(abs)
				if e1 != nil {
					r1 = modRepo.root
				}
				if e2 != nil {
					r2 = abs
				}
				sameRoot = r1 == r2
			}
			var modHead string
			if sameRoot {
				modHead, _, _ = modRepo.headSHA(ctx)
			}
			if !sameRoot || fmt.Sprintf("Subproject commit %s\n", modHead) != ch.AfterText.Text {
				return "", fmt.Errorf("%s: submodule HEAD changed since the draft; build a new draft.", ch.Path)
			}
		}
		return FormatTextPatch(ch.Path, ch.BeforeText, ch.AfterText), nil
	}
	return repo.diffBlobs(ctx, ch.Path, ch.BeforeWhole, ch.AfterWhole)
}

// CommitSelection commits the draft's selection on top of the current HEAD using a private index.
// upstream: apply.ts:171
func CommitSelection(draft *Draft, req CommitRequest, cctx CommitContext) (*CommitOutcome, error) {
	ctx := cctx.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	changes := draft.Selection()
	if len(changes) == 0 {
		if len(draft.Paths()) > 0 {
			return nil, errors.New("Nothing selected; use take() first.")
		}
		if len(draft.Skipped) > 0 {
			return nil, fmt.Errorf("Nothing to commit: only %s changed (submodule or nested repository), which selective commits skip.", strings.Join(draft.Skipped, ", "))
		}
		return nil, fmt.Errorf("Nothing to commit: %s has no changes against HEAD.", shortenHomePath(draft.Root))
	}

	repo, err := discoverGitRepo(ctx, draft.Root)
	if err != nil {
		return nil, err
	}
	if repo == nil {
		return nil, fmt.Errorf("Not inside a git repository: %s", draft.Root)
	}
	operation, err := repo.operationKind(ctx)
	if err != nil {
		return nil, err
	}
	if operation == "unmerged-index" {
		return nil, errors.New("The index has unresolved conflicts; resolve them before using commit().")
	}
	if operation != "" {
		label := operation
		if label == "am" {
			label = "git am"
		}
		return nil, fmt.Errorf("A %s is in progress; finish or abort it before using commit(). Use plain `git commit` for commits within that operation.", label)
	}

	message, err := ComposeMessage(req)
	if err != nil {
		return nil, err
	}
	var patches []pathPatch
	for _, ch := range changes {
		p, err := patchForChange(ctx, repo, ch)
		if err != nil {
			return nil, err
		}
		if p != "" {
			patches = append(patches, pathPatch{path: ch.Path, patch: p})
		}
	}
	if len(patches) == 0 {
		return nil, errors.New("The selection matches HEAD; nothing to commit.")
	}

	var ownCommits map[string]bool
	if req.Amend {
		ownCommits = make(map[string]bool)
		if cctx.Journal != nil {
			recs, err := cctx.Journal.Read()
			if err != nil {
				return nil, err
			}
			for _, rec := range recs {
				if rec.Type == "commit" && rec.SHA != "" {
					ownCommits[rec.SHA] = true
				}
			}
		}
	}

	rawPatches := make([]string, len(patches))
	for i, p := range patches {
		rawPatches[i] = p.patch
	}
	joined := joinPatches(rawPatches)
	indexPath := filepath.Join(repo.gitDir, fmt.Sprintf("omp-commit-%s-%s.index", draft.ID, randomBase36(46656)))
	defer os.Remove(indexPath)

	var sha string
	base := draft.Head
	selectedTree := ""

	for attempt := 1; sha == ""; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		headStr, hasHead, err := repo.headSHA(ctx)
		if err != nil {
			return nil, err
		}
		var headPtr *string
		if hasHead {
			h := headStr
			headPtr = &h
		}
		if ownCommits != nil && (headPtr == nil || !ownCommits[*headPtr]) {
			return nil, fmt.Errorf("HEAD %s was not made by this session's commit flow; refusing to amend it.", shortSHA(headPtr))
		}
		if headPtr == nil {
			_ = os.Remove(indexPath)
		} else {
			if err := repo.readTreeKeepingSparse(ctx, *headPtr, indexPath); err != nil {
				return nil, err
			}
		}
		if err := repo.applyPatchCached(ctx, joined, indexPath, false); err != nil {
			if equalStringPtr(headPtr, draft.Head) {
				return nil, err
			}
			var blocked []string
			for _, entry := range patches {
				ok, _ := repo.canApplyPatchCached(ctx, entry.patch, indexPath, false)
				if !ok {
					blocked = append(blocked, entry.path)
				}
			}
			blockedText := strings.Join(blocked, ", ")
			if blockedText == "" {
				blockedText = "selected paths"
			}
			return nil, fmt.Errorf("HEAD moved from %s to %s, which changed %s; build a new draft.", shortSHA(draft.Head), shortSHA(headPtr), blockedText)
		}
		selectedTree, err = repo.writeTree(ctx, indexPath)
		if err != nil {
			return nil, err
		}
		created, err := repo.commitCreate(ctx, message, commitCreateOptions{
			indexPath:    indexPath,
			expectedHead: headStr,
			hasExpected:  hasHead,
			amend:        req.Amend,
			noVerify:     req.NoVerify,
		})
		if err != nil {
			if !isHeadMoved(err) || attempt >= headRaceAttempts {
				return nil, err
			}
			continue
		}
		sha = created
		base = headPtr
	}

	indexNotes := []string{}
	sharedIndexFile := filepath.Join(repo.gitDir, "index")
	_, sharedExistsErr := os.Stat(sharedIndexFile)
	if base == nil && errors.Is(sharedExistsErr, os.ErrNotExist) {
		if err := repo.readTreeShared(ctx, sha); err != nil {
			return nil, err
		}
	} else {
		left := syncSharedIndex(ctx, repo, patches)
		kept := make(map[string]bool, len(left))
		for _, entry := range left {
			kept[entry.path] = true
		}
		var rewrites []pathPatch
		hookChanged, err := repo.changedFilesBetween(context.Background(), selectedTree, sha)
		if err == nil {
			for _, file := range hookChanged {
				if kept[file] {
					continue
				}
				patch, err := repo.diffTextBetween(context.Background(), selectedTree, sha, file)
				if err == nil && patch != "" {
					rewrites = append(rewrites, pathPatch{path: file, patch: patch})
				}
			}
		}
		if len(rewrites) > 0 {
			left = append(left, syncSharedIndex(ctx, repo, rewrites)...)
		}
		for _, entry := range left {
			indexNotes = append(indexNotes, entry.note)
		}
	}

	selectedPaths := make([]string, len(patches))
	for i, p := range patches {
		selectedPaths[i] = p.path
	}
	if cctx.Journal != nil {
		if err := cctx.Journal.RecordCommit(cctx.Agent, sha, selectedPaths); err != nil {
			return nil, err
		}
	}

	// The commit exists and is recorded: the reads below describe it and are not cancelled.
	if cctx.testOnCommitDetails != nil {
		cctx.testOnCommitDetails(sha, nil)
	}
	details, err := repo.commitDetails(context.Background(), sha)
	if err != nil {
		return nil, err
	}
	subject := details.message
	if nl := strings.IndexByte(subject, '\n'); nl >= 0 {
		subject = subject[:nl]
	}
	if !req.Amend {
		return &CommitOutcome{
			SHA:        sha,
			Subject:    subject,
			Paths:      selectedPaths,
			Parent:     base,
			IndexNotes: indexNotes,
		}, nil
	}

	var parent *string
	if len(details.parents) > 0 {
		p := details.parents[0]
		parent = &p
	}
	var paths []string
	if parent == nil {
		entries, err := repo.lsTree(context.Background(), sha, nil)
		if err != nil {
			return nil, err
		}
		paths = make([]string, len(entries))
		for i, e := range entries {
			paths[i] = e.path
		}
	} else {
		paths, err = repo.changedFilesBetween(context.Background(), *parent, sha)
		if err != nil {
			return nil, err
		}
	}
	amended := ""
	if base != nil {
		amended = *base
	}
	return &CommitOutcome{
		SHA:        sha,
		Subject:    subject,
		Paths:      paths,
		Parent:     parent,
		Amended:    amended,
		IndexNotes: indexNotes,
	}, nil
}
