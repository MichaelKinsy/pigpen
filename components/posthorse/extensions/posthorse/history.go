package posthorse

// Port of src/history.ts: the `history` tool (search and read) over the active branch and, with
// all=true, every session file of the project.

import (
	"context"
	"errors"
	"path/filepath"
	"regexp"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// locatedEntry is the TS LocatedEntry (history.ts:12).
type locatedEntry struct {
	item   windowedEntry
	source string
}

// entryReference is `entryReference` (history.ts:13-24): `id` or `id@fileKey`. hasKey is false
// when the reference has no "@" (the key is then undefined, not "").
func entryReference(id string) (entryID, fileKey string, hasKey bool) {
	separator := strings.Index(id, "@")
	if separator < 0 {
		return id, "", false
	}
	return id[:separator], id[separator+1:], true
}

// branchEntry is `branchEntry` (history.ts:25-36).
func branchEntry(source historySource, id string) (windowedEntry, bool, error) {
	branch, err := source.branch()
	if err != nil {
		return windowedEntry{}, false, err
	}
	project := windowProjection()
	for _, entry := range branch {
		windowID := project.project(entry)
		if entryID, _ := str(entry, "id"); entryID == id {
			if item, ok := toWindowedEntry(entry, windowID); ok {
				return item, true, nil
			}
		}
	}
	return windowedEntry{}, false, nil
}

var plainEntryID = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// findEntry is `findEntry` (history.ts:37-70).
func findEntry(ctx context.Context, source historySource, id string) (locatedEntry, error) {
	entryID, fileKey, hasKey := entryReference(id)
	if !hasKey {
		current, ok, err := branchEntry(source, entryID)
		if err != nil {
			return locatedEntry{}, err
		}
		if ok {
			return locatedEntry{item: current}, nil
		}
	}
	scope := sessionScope{dir: source.dir, cwd: source.cwd, currentFile: source.currentFile}
	if hasKey {
		scope.fileKey = &fileKey
	} else if plainEntryID.MatchString(entryID) {
		scope.needles = []string{`"id":"` + entryID + `"`}
	}
	for file, err := range scopedSessionFiles(ctx, scope) {
		if err != nil {
			return locatedEntry{}, err
		}
		for item, err := range sessionWindowEntries(ctx, file, func(candidate string) bool { return candidate == entryID }, nil, true) {
			if err != nil {
				return locatedEntry{}, err
			}
			rel, _ := filepath.Rel(source.dir, file)
			return locatedEntry{item: item, source: rel}, nil
		}
	}
	return locatedEntry{}, errors.New("No history entry \"" + id + "\" in this session or this project's other sessions. Pass an id exactly as history search prints it, such as 1a2f07de, or 1a2f07de@Ab3dE5fG7h from another session; search first when you only have text or a timestamp.")
}

// newHistorySource is `historySource` (history.ts:71-81).
func newHistorySource(ctx sdk.Context) (historySource, error) {
	manager := ctx.SessionManager()
	dir, err := manager.GetSessionDir()
	if err != nil {
		return historySource{}, err
	}
	file, err := manager.GetSessionFile()
	if err != nil {
		return historySource{}, err
	}
	source := historySource{
		dir: dir,
		cwd: absPath(ctx.Cwd()),
		branch: func() ([]entryLike, error) {
			branch, err := manager.GetBranch(nil)
			if err != nil {
				return nil, err
			}
			entries := make([]entryLike, len(branch))
			for i, entry := range branch {
				entries[i] = entry
			}
			return entries, nil
		},
		projection: func() ([]projectedEntry, error) {
			projection, err := manager.BuildSessionProjection()
			if err != nil {
				return nil, err
			}
			entries := make([]projectedEntry, len(projection.Entries))
			for i, entry := range projection.Entries {
				messages := make([]messageLike, len(entry.Messages))
				for j, message := range entry.Messages {
					messages[j] = message
				}
				entries[i] = projectedEntry{sourceEntry: entry.SourceEntry, messages: messages}
			}
			return entries, nil
		},
	}
	if file != nil {
		source.currentFile = *file
	}
	return source, nil
}

// historyParamsOf reads the tool arguments. The host validates them against the schema first, so
// a member of another type reads as absent.
func historyParamsOf(params map[string]any) historyParams {
	var p historyParams
	p.op, _ = str(params, "op")
	p.query, p.hasQuery = str(params, "query")
	p.id, p.hasID = str(params, "id")
	p.all, _ = boolean(params, "all")
	p.cursor, _ = str(params, "cursor")
	if v, ok := num(params, "limit"); ok {
		n := jsToInt(v)
		p.limit = &n
	}
	if v, ok := num(params, "offset"); ok {
		p.offset = &v
	}
	if v, ok := num(params, "imageOffset"); ok {
		p.imageOffset = &v
	}
	return p
}

// historySchema is the TypeBox `parameters` of the tool (history.ts:104-137), members in
// declaration order.
func historySchema() sdk.Schema {
	return sdk.Type.Object(sdk.NewOrderedObject(
		"op", sdk.Type.Union([]any{sdk.Type.Literal("search"), sdk.Type.Literal("read")}, sdk.NewOrderedObject("description", "Operation to perform")),
		"query", sdk.Type.Optional(sdk.Type.String(sdk.NewOrderedObject("description", "Case-insensitive text to find (search)"))),
		"id", sdk.Type.Optional(sdk.Type.String(sdk.NewOrderedObject("description", "Entry id exactly as search printed it (read)"))),
		"all", sdk.Type.Optional(sdk.Type.Boolean(sdk.NewOrderedObject("description", "Also search this project's other sessions and subagent runs"))),
		"limit", sdk.Type.Optional(sdk.Type.Integer(sdk.NewOrderedObject("description", "Maximum search results per page (default 10, max 50)", "minimum", 1, "maximum", 50))),
		"cursor", sdk.Type.Optional(sdk.Type.String(sdk.NewOrderedObject("description", "Continuation from the previous search page; keep query and all unchanged", "maxLength", 512))),
		"offset", sdk.Type.Optional(sdk.Type.Integer(sdk.NewOrderedObject("description", "Character offset for read (default 0)", "minimum", 0))),
		"imageOffset", sdk.Type.Optional(sdk.Type.Integer(sdk.NewOrderedObject("description", "First image to return (read); use the value from the continuation", "minimum", 0))),
	)).Schema()
}

// registerHistory is `registerHistory` (history.ts:82-148). policy is called once per use, like
// the TS `policy(ctx)`: each call is its own snapshot.
func registerHistory(ext *sdk.Extension, policy snapshotReader, pages pageAccess) {
	def := sdk.ToolDefinition{
		Name:             "history",
		Label:            "History",
		Description:      "Search or read earlier session entries, including previous context windows. search skips entries still in your active context and lists original content before recovery echoes, newest first; all=true also searches this project's other sessions and subagent runs. read takes an entry id exactly as search printed it and pages text and images.",
		PromptSnippet:    "recover earlier conversation that left the active context window",
		PromptGuidelines: []string{"Use history search first, then history read with the entry id exactly as printed"},
		Parameters:       historySchema(),
		Execute: func(ctx sdk.Context, args map[string]any) (any, error) {
			source, err := newHistorySource(ctx)
			if err != nil {
				return nil, err
			}
			params := historyParamsOf(args)
			signal := signalOf(ctx)
			if params.op == "search" {
				fail := func(message string) error {
					return pages.error(policy(ctx, nil), message, params.cursor)
				}
				matches, err := historyMatches(signal, source, params, fail)
				if err != nil {
					return nil, err
				}
				result, err := searchPage(matches, params, policy(ctx, nil), pages)
				if err != nil {
					return nil, err
				}
				return result, nil
			}
			id, err := requireValue(params.id, params.hasID, "id", params.op)
			if err != nil {
				return nil, err
			}
			located, err := findEntry(signal, source, id)
			if err != nil {
				return nil, err
			}
			result, err := historyReadPage(located.item, params, policy(ctx, nil), pages, located.source)
			if err != nil {
				return nil, err
			}
			return result, nil
		},
	}
	applyToolCards(&def, "history")
	ext.RegisterTool(def)
}
