package tintinweb_subagents

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"sync/atomic"
	"unicode/utf16"
	"unicode/utf8"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// What `@` can address, and the suggestions the editor shows for it. A subagent is addressable whether or not it is
// running: a live record is messaged or resumed, an evicted one whose session is on disk is reopened, and an agent
// type with no instance at all is started. The roster unions all three, and the dispatcher and the popup read the same
// list. Pi's own provider owns `@` as "attach a file"; this one adds the tokens that name an agent (agents first, under
// ONE prefix: where both sides produce rows they measured the same span) and delegates everything else.
// upstream: src/ui/agent-mention.ts.

// mentionRecord is what the roster reads of a live agent, copied under the manager's lock.
type mentionRecord struct {
	Handle, Alias, Type, Description, Status string
	StartedAt                                int64 // milliseconds, as the original's Date.now()
}

func (r mentionRecord) live() bool { return r.Status == statusRunning || r.Status == statusQueued }

// rosterSource is the part of the manager the roster reads (the original's AgentManager.listAgents, listTombstones).
type rosterSource interface {
	listMentionRecords() []mentionRecord // newest started first
	listTombstones() []tombstone         // newest completed first
}

// mentionTarget is one thing `@` can address and what sending to it will do. kind is "record", "tombstone" or "type".
type mentionTarget struct {
	Kind, Handle, TypeLabel string
	Record                  mentionRecord
	Entry                   tombstone
	Type, Description       string
}

// typeInfo is the registry's facts the roster needs.
type typeInfo struct{ Name, Description string }

// listMentionRecords is the agents with a handle, newest started first. upstream: agent-manager.ts listAgents.
func (m *manager) listMentionRecords() []mentionRecord {
	m.mu.Lock()
	out := make([]mentionRecord, 0, len(m.order))
	for _, id := range m.order {
		r := m.records[id]
		out = append(out, mentionRecord{Handle: r.Handle, Alias: r.Alias, Type: r.Type, Description: r.Description, Status: r.Status, StartedAt: r.StartedAt.UnixMilli()})
	}
	m.mu.Unlock()
	sort.SliceStable(out, func(i, j int) bool { return out[i].StartedAt > out[j].StartedAt })
	return out
}

// mentionRoster is everything `@` can reach, in the order the popup lists it: steerable agents first, then the other
// live ones earliest-launched, then evicted agents that can be reopened, then agent types with no live instance. A type
// whose handle a record already holds is omitted. displayNameOf is the type's display_name. upstream: mentionRoster.
func mentionRoster(src rosterSource, types []typeInfo, displayNameOf func(string) string) []mentionTarget {
	if displayNameOf == nil {
		displayNameOf = func(t string) string { return t }
	}
	all := src.listMentionRecords()
	records := make([]mentionRecord, 0, len(all))
	for _, r := range all {
		if r.Handle != "" {
			records = append(records, r)
		}
	}
	sort.SliceStable(records, func(i, j int) bool {
		a, b := records[i], records[j]
		if a.live() != b.live() {
			return a.live()
		}
		return a.StartedAt < b.StartedAt
	})
	taken := map[string]bool{}
	var targets []mentionTarget
	for _, r := range records {
		handle := r.Alias
		if handle == "" {
			handle = r.Handle
		}
		taken[strings.ToLower(handle)] = true
		taken[strings.ToLower(r.Handle)] = true
		targets = append(targets, mentionTarget{Kind: "record", Handle: handle, Record: r, TypeLabel: displayNameOf(r.Type)})
	}
	for _, e := range src.listTombstones() {
		handle := e.Alias
		if handle == "" {
			handle = e.Handle
		}
		if taken[strings.ToLower(handle)] {
			continue
		}
		taken[strings.ToLower(handle)] = true
		taken[strings.ToLower(e.Handle)] = true
		targets = append(targets, mentionTarget{Kind: "tombstone", Handle: handle, Entry: e, TypeLabel: displayNameOf(e.Type)})
	}
	for _, t := range types {
		handle := handleBase(t.Name)
		if taken[handle] {
			continue
		}
		taken[handle] = true
		targets = append(targets, mentionTarget{Kind: "type", Handle: handle, Type: t.Name, Description: t.Description})
	}
	return targets
}

// mentionItems is the suggestions for the `@...` token under the cursor, or nil when it names no agent.
func mentionItems(roster []mentionTarget, line string, cursorCol int) *sdk.AutocompleteSuggestions {
	typedRaw, ok := mentionTrigger(utf16Prefix(line, cursorCol))
	if !ok {
		return nil
	}
	typed := strings.ToLower(typedRaw)
	var items []sdk.AutocompleteItem
	for _, t := range roster {
		if !strings.HasPrefix(strings.ToLower(t.Handle), typed) {
			continue
		}
		items = append(items, sdk.AutocompleteItem{Value: "@" + t.Handle, Label: "@" + t.Handle, Description: describeTarget(t)})
	}
	if len(items) == 0 {
		return nil
	}
	return &sdk.AutocompleteSuggestions{Items: items, Prefix: "@" + typedRaw}
}

// describeTarget names the action that will actually happen, so the list never mispromises.
func describeTarget(t mentionTarget) string {
	switch t.Kind {
	case "type":
		return "start agent · " + summarize(t.Description)
	case "tombstone":
		// No status: the record is gone, and "completed" would imply one is still tracked.
		return "resume · " + t.TypeLabel + " · " + t.Entry.Description
	}
	r := t.Record
	action := "resume"
	if r.live() {
		action = "send message"
	}
	// A row listed under its alias has lost the type its handle would have shown, so name it.
	identity := ""
	if r.Alias != "" {
		identity = t.TypeLabel + " · "
	}
	return action + " · " + identity + r.Status + " · " + r.Description
}

// summarize is the first sentence of an agent description, clipped: these run to paragraphs. A sentence ends at
// . ! or ? followed by whitespace or the end. upstream: summarize.
func summarize(description string) string {
	first := description
	for i, r := range description {
		if r != '.' && r != '!' && r != '?' {
			continue
		}
		next := description[i+1:]
		if next == "" {
			break
		}
		if n, _ := utf8.DecodeRuneInString(next); isJSSpace(n) {
			first = description[:i+1]
			break
		}
	}
	first = jsTrim(strings.Join(strings.FieldsFunc(first, isJSSpace), " "))
	units := utf16.Encode([]rune(first))
	if len(units) > 60 {
		return strings.TrimRightFunc(string(utf16.Decode(units[:59])), isJSSpace) + "…"
	}
	return first
}

// mentionProviderState carries the once-only warning about a failing provider below.
type mentionProviderState struct{ warned atomic.Bool }

// newMentionProvider stacks the `@handle` suggestions on the provider below (pi's, or another extension's). It declares
// only `@`: Pi unions each wrapper's own characters onto the outermost one itself. Every callback recovers from a
// panic, as the provider runs on the editor's input path. upstream: createMentionProvider.
func newMentionProvider(current *sdk.AutocompleteProvider, roster func() []mentionTarget, isEnabled func() bool) *sdk.AutocompleteProvider {
	st := &mentionProviderState{}
	return &sdk.AutocompleteProvider{
		TriggerCharacters: []string{"@"},
		GetSuggestions: func(ctx sdk.Context, lines []string, cursorLine, cursorCol int, force bool) (*sdk.AutocompleteSuggestions, error) {
			var mine *sdk.AutocompleteSuggestions
			guard("mention suggestions", func() {
				if !isEnabled() {
					return
				}
				line := ""
				if cursorLine >= 0 && cursorLine < len(lines) {
					line = lines[cursorLine]
				}
				// The roster is built only for a token that can name an agent: any other keystroke costs the trigger test.
				if _, ok := mentionTrigger(utf16Prefix(line, cursorCol)); ok {
					mine = mentionItems(roster(), line, cursorCol)
				}
			})
			// Asked unconditionally: pi owns `@` and keeps answering for it when a handle matches too. A failure below
			// us must not delete the handle rows.
			theirs, err := suggestBelow(current, ctx, lines, cursorLine, cursorCol, force)
			if err != nil && st.warned.CompareAndSwap(false, true) {
				// Once per provider, not per keystroke: this runs on every character typed after `@`.
				fmt.Fprintf(os.Stderr, "[pi-subagents] the autocomplete provider below us failed; showing agent rows only: %v\n", err)
			}
			return mergeSuggestions(mine, theirs), nil
		},
		ApplyCompletion: func(ctx sdk.Context, lines []string, cursorLine, cursorCol int, item sdk.AutocompleteItem, prefix string) (sdk.AutocompleteCompletion, error) {
			return current.ApplyCompletion(ctx, lines, cursorLine, cursorCol, item, prefix)
		},
		ShouldTriggerFileCompletion: func(ctx sdk.Context, lines []string, cursorLine, cursorCol int) (bool, error) {
			if current.ShouldTriggerFileCompletion == nil {
				return true, nil
			}
			return current.ShouldTriggerFileCompletion(ctx, lines, cursorLine, cursorCol)
		},
	}
}

// suggestBelow asks the wrapped provider, turning a panic into an error.
func suggestBelow(current *sdk.AutocompleteProvider, ctx sdk.Context, lines []string, line, col int, force bool) (res *sdk.AutocompleteSuggestions, err error) {
	defer func() {
		if v := recover(); v != nil {
			res, err = nil, fmt.Errorf("%v", v)
		}
	}()
	res, err = current.GetSuggestions(ctx, lines, line, col, force)
	if err != nil {
		return nil, err
	}
	return res, nil
}

func mergeSuggestions(mine, theirs *sdk.AutocompleteSuggestions) *sdk.AutocompleteSuggestions {
	if mine == nil {
		return theirs
	}
	if theirs == nil {
		return mine
	}
	// Agents first: a handful against pi's 20 file rows. The prefix is ours by the span argument in the header.
	items := make([]sdk.AutocompleteItem, 0, len(mine.Items)+len(theirs.Items))
	items = append(append(items, mine.Items...), theirs.Items...)
	return &sdk.AutocompleteSuggestions{Items: items, Prefix: mine.Prefix}
}
