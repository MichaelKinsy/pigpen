package tintinweb_subagents

import (
	"errors"
	"sort"
	"strings"
	"sync/atomic"
	"testing"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// Twins of test/agent-mention-provider.test.ts.

type fakeRoster struct {
	records []mentionRecord
	tombs   []tombstone
}

func (f fakeRoster) listMentionRecords() []mentionRecord {
	out := append([]mentionRecord{}, f.records...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].StartedAt > out[j].StartedAt }) // newest first, like the manager
	return out
}
func (f fakeRoster) listTombstones() []tombstone {
	out := append([]tombstone{}, f.tombs...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].CompletedAt.After(out[j].CompletedAt) })
	return out
}

func rec(handle string, over func(*mentionRecord)) mentionRecord {
	r := mentionRecord{Handle: handle, Type: "Explore", Description: "find flaky tests", Status: "running", StartedAt: 1000}
	if over != nil {
		over(&r)
	}
	return r
}

func managerWith(rs ...mentionRecord) fakeRoster { return fakeRoster{records: rs} }

func tomb(over func(*tombstone)) tombstone {
	t := tombstone{Handle: "explore", ID: "t1", Type: "Explore", Description: "audit the RPC path", SessionFile: "/sessions/explore.jsonl", CompletedAt: timeMs(5000)}
	if over != nil {
		over(&t)
	}
	return t
}

var fileSuggestions = &sdk.AutocompleteSuggestions{Items: []sdk.AutocompleteItem{{Value: "@src/index.ts", Label: "src/index.ts"}}, Prefix: "@src/"}

// builtIn stands in for pi's provider and records what it was asked.
type builtIn struct {
	p      *sdk.AutocompleteProvider
	calls  atomic.Int32
	lastIn atomic.Pointer[[]string]
	result func() (*sdk.AutocompleteSuggestions, error)
}

func newBuiltIn() *builtIn {
	b := &builtIn{result: func() (*sdk.AutocompleteSuggestions, error) { return fileSuggestions, nil }}
	b.p = &sdk.AutocompleteProvider{
		TriggerCharacters: []string{"@", "#"},
		GetSuggestions: func(_ sdk.Context, lines []string, _, _ int, _ bool) (*sdk.AutocompleteSuggestions, error) {
			b.calls.Add(1)
			b.lastIn.Store(&lines)
			return b.result()
		},
		ApplyCompletion: func(_ sdk.Context, _ []string, _, _ int, _ sdk.AutocompleteItem, _ string) (sdk.AutocompleteCompletion, error) {
			return sdk.AutocompleteCompletion{Lines: []string{"applied"}, CursorLine: 0, CursorCol: 7}, nil
		},
		ShouldTriggerFileCompletion: func(sdk.Context, []string, int, int) (bool, error) { return false, nil },
	}
	return b
}

func providerOver(src rosterSource, types []typeInfo, enabled bool, cur *sdk.AutocompleteProvider) *sdk.AutocompleteProvider {
	return newMentionProvider(cur, func() []mentionTarget { return mentionRoster(src, types, nil) }, func() bool { return enabled })
}

func suggest(p *sdk.AutocompleteProvider, line string) *sdk.AutocompleteSuggestions {
	res, err := p.GetSuggestions(sdk.Context{}, []string{line}, 0, len([]rune(line)), false)
	if err != nil {
		panic(err)
	}
	return res
}

func agentRows(res *sdk.AutocompleteSuggestions) []sdk.AutocompleteItem {
	var out []sdk.AutocompleteItem
	if res == nil {
		return out
	}
	for _, it := range res.Items {
		if it.Value != "@src/index.ts" {
			out = append(out, it)
		}
	}
	return out
}

func values(items []sdk.AutocompleteItem) []string {
	var out []string
	for _, i := range items {
		out = append(out, i.Value)
	}
	return out
}

func TestMentionProviderTwins(t *testing.T) {
	const f = "agent-mention-provider"
	explore := func(over func(*mentionRecord)) fakeRoster { return managerWith(rec("explore", over)) }
	row := sdk.AutocompleteItem{Value: "@explore", Label: "@explore", Description: "send message · running · find flaky tests"}

	tw(t, f, "lists matching handles above pi's files rather than instead of them", func(t *testing.T) {
		cur := newBuiltIn()
		p := providerOver(explore(nil), nil, true, cur.p)
		got := suggest(p, "@ex")
		eq(t, got, &sdk.AutocompleteSuggestions{Items: append([]sdk.AutocompleteItem{row}, fileSuggestions.Items...), Prefix: "@ex"})
		// The inner provider sees the same line, cursor and options the editor handed us.
		eq(t, *cur.lastIn.Load(), []string{"@ex"})
	})
	tw(t, f, "offers every agent on a bare @, and still offers files", func(t *testing.T) {
		p := providerOver(managerWith(rec("explore", nil), rec("plan", func(r *mentionRecord) { r.StartedAt = 2000 })), nil, true, newBuiltIn().p)
		eq(t, values(suggest(p, "@").Items), []string{"@explore", "@plan", "@src/index.ts"})
	})
	tw(t, f, "offers agents alone when no file matched", func(t *testing.T) {
		cur := newBuiltIn()
		cur.result = func() (*sdk.AutocompleteSuggestions, error) { return nil, nil }
		p := providerOver(explore(nil), nil, true, cur.p)
		eq(t, suggest(p, "@ex"), &sdk.AutocompleteSuggestions{Items: []sdk.AutocompleteItem{row}, Prefix: "@ex"})
	})
	tw(t, f, "still offers agents when the wrapped provider throws", func(t *testing.T) {
		cur := newBuiltIn()
		cur.result = func() (*sdk.AutocompleteSuggestions, error) { return nil, errors.New("inner provider exploded") }
		eq(t, suggest(providerOver(explore(nil), nil, true, cur.p), "@ex"), &sdk.AutocompleteSuggestions{Items: []sdk.AutocompleteItem{row}, Prefix: "@ex"})
	})
	tw(t, f, "still offers agents when the wrapped provider throws synchronously", func(t *testing.T) {
		cur := newBuiltIn()
		cur.p.GetSuggestions = func(sdk.Context, []string, int, int, bool) (*sdk.AutocompleteSuggestions, error) { panic("sync boom") }
		eq(t, suggest(providerOver(explore(nil), nil, true, cur.p), "@ex"), &sdk.AutocompleteSuggestions{Items: []sdk.AutocompleteItem{row}, Prefix: "@ex"})
	})
	tw(t, f, "warns once about a failing inner provider, not once per keystroke", func(t *testing.T) {
		cur := newBuiltIn()
		cur.result = func() (*sdk.AutocompleteSuggestions, error) { return nil, errors.New("inner provider exploded") }
		p := providerOver(explore(nil), nil, true, cur.p)
		out := captureStderr(t, func() {
			for _, line := range []string{"@e", "@ex", "@exp"} {
				suggest(p, line)
			}
		})
		eq(t, strings.Count(out, "[pi-subagents]"), 1)
	})
	tw(t, f, "returns pi's list untouched when no agent exists at all", func(t *testing.T) {
		got := suggest(providerOver(managerWith(), nil, true, newBuiltIn().p), "@ex")
		if got != fileSuggestions {
			t.Fatalf("not pi's own list: %#v", got)
		}
	})
	tw(t, f, "puts steerable agents first, then earliest-launched", func(t *testing.T) {
		p := providerOver(managerWith(
			rec("explore", func(r *mentionRecord) { r.Status = "completed"; r.StartedAt = 1000 }),
			rec("explore-3", func(r *mentionRecord) { r.StartedAt = 3000 }),
			rec("explore-2", func(r *mentionRecord) { r.StartedAt = 2000 })), nil, true, newBuiltIn().p)
		eq(t, values(agentRows(suggest(p, "@ex"))), []string{"@explore-2", "@explore-3", "@explore"})
	})
	tw(t, f, "matches case-insensitively", func(t *testing.T) {
		eq(t, values(agentRows(suggest(providerOver(explore(nil), nil, true, newBuiltIn().p), "@EX"))), []string{"@explore"})
	})
	tw(t, f, "completes a mention typed mid-message", func(t *testing.T) {
		eq(t, suggest(providerOver(explore(nil), nil, true, newBuiltIn().p), "ask @ex").Prefix, "@ex")
	})

	types := []typeInfo{{"Explore", "Fast codebase exploration. Read-only, medium breadth."}, {"code-review", "Reviews a diff."}}
	tw(t, f, "offers a registered type with no live instance, and says it will start one", func(t *testing.T) {
		got := suggest(providerOver(managerWith(), types, true, newBuiltIn().p), "@ex")
		eq(t, agentRows(got), []sdk.AutocompleteItem{{Value: "@explore", Label: "@explore", Description: "start agent · Fast codebase exploration."}})
		// The span the rows are inserted over is the typed token, not the stub's "@src/".
		eq(t, got.Prefix, "@ex")
	})
	tw(t, f, "lets a live agent own its handle instead of listing the type twice", func(t *testing.T) {
		got := suggest(providerOver(explore(nil), types, true, newBuiltIn().p), "@ex")
		eq(t, values(agentRows(got)), []string{"@explore"})
		eq(t, agentRows(got)[0].Description, "send message · running · find flaky tests")
	})
	tw(t, f, "still offers the type once its only instance has finished, as a resume", func(t *testing.T) {
		got := suggest(providerOver(explore(func(r *mentionRecord) { r.Status = "completed" }), types, true, newBuiltIn().p), "@ex")
		eq(t, got.Items[0].Description, "resume · completed · find flaky tests")
	})
	tw(t, f, "lists live agents before startable types", func(t *testing.T) {
		got := suggest(providerOver(managerWith(rec("plan", nil)), types, true, newBuiltIn().p), "@")
		eq(t, values(agentRows(got)), []string{"@plan", "@explore", "@code-review"})
	})

	tw(t, f, "delegates when no handle matches the typed prefix", func(t *testing.T) {
		cur := newBuiltIn()
		if got := suggest(providerOver(explore(nil), nil, true, cur.p), "@zz"); got != fileSuggestions {
			t.Fatal("not pi's list")
		}
		eq(t, cur.calls.Load(), int32(1))
	})
	tw(t, f, "delegates a path-shaped token even when its first segment names an agent", func(t *testing.T) {
		cur := newBuiltIn()
		if got := suggest(providerOver(explore(nil), nil, true, cur.p), "@explore/notes.md"); got != fileSuggestions {
			t.Fatal("not pi's list")
		}
		eq(t, cur.calls.Load(), int32(1))
	})
	tw(t, f, "delegates an @ that is not at a token boundary", func(t *testing.T) {
		if got := suggest(providerOver(explore(nil), nil, true, newBuiltIn().p), "mail@ex"); got != fileSuggestions {
			t.Fatal("not pi's list")
		}
	})
	tskip(t, f, "never claims nested children — nothing can address them",
		"nested delegation is not part of this port yet: there is no parent on a record to filter on (the nested slice adds the filter beside the field); the roster lists top-level agents only, which is every agent here")
	tw(t, f, "delegates everything while mentions are disabled", func(t *testing.T) {
		if got := suggest(providerOver(explore(nil), nil, false, newBuiltIn().p), "@ex"); got != fileSuggestions {
			t.Fatal("not pi's list")
		}
	})

	// A foreign wrapper that owns `#` and delegates everything else.
	hash := func(cur *sdk.AutocompleteProvider) *sdk.AutocompleteProvider {
		return &sdk.AutocompleteProvider{
			TriggerCharacters: []string{"#"},
			GetSuggestions: func(c sdk.Context, lines []string, l, col int, force bool) (*sdk.AutocompleteSuggestions, error) {
				if strings.HasPrefix(utf16Prefix(lines[l], col), "#") {
					return &sdk.AutocompleteSuggestions{Items: []sdk.AutocompleteItem{{Value: "#general", Label: "#general"}}, Prefix: "#"}, nil
				}
				return cur.GetSuggestions(c, lines, l, col, force)
			},
			ApplyCompletion: cur.ApplyCompletion,
		}
	}
	one := []typeInfo{{"Explore", "Fast codebase exploration."}}
	tw(t, f, "delegates to a provider registered before us", func(t *testing.T) {
		p := providerOver(managerWith(), one, true, hash(newBuiltIn().p))
		eq(t, values(agentRows(suggest(p, "@ex"))), []string{"@explore"})
		eq(t, values(suggest(p, "#gen").Items), []string{"#general"})
		if suggest(p, "@src/") != fileSuggestions {
			t.Fatal("files no longer reach the base")
		}
	})
	tw(t, f, "is still reachable through a provider registered after us", func(t *testing.T) {
		outer := hash(providerOver(managerWith(), one, true, newBuiltIn().p))
		eq(t, values(agentRows(suggest(outer, "@ex"))), []string{"@explore"})
		eq(t, values(suggest(outer, "#g").Items), []string{"#general"})
	})
	tw(t, f, "lets pi union the chain's trigger characters, as it already does", func(t *testing.T) {
		provider := newBuiltIn().p
		var collected []string
		for _, wrap := range []func(*sdk.AutocompleteProvider) *sdk.AutocompleteProvider{
			hash, func(c *sdk.AutocompleteProvider) *sdk.AutocompleteProvider {
				return providerOver(managerWith(), one, true, c)
			},
		} {
			provider = wrap(provider)
			collected = append(collected, provider.TriggerCharacters...)
		}
		eq(t, provider.TriggerCharacters, []string{"@"})
		eq(t, collected, []string{"#", "@"})
	})
	tw(t, f, "can be rebuilt from the same factory without accumulating state", func(t *testing.T) {
		src := explore(nil)
		first := providerOver(src, nil, true, newBuiltIn().p)
		second := providerOver(src, nil, true, newBuiltIn().p)
		eq(t, second.TriggerCharacters, first.TriggerCharacters)
	})

	// The original hands our items to pi-tui's real CombinedAutocompleteProvider. A Go extension cannot construct it: the
	// editor's own provider lives in the host, and these cases are about what it does with a prefix. The contract is held
	// from this side by the cases above (one prefix, the typed token, agents first) and by the delegation of applyCompletion.
	tskip(t, f, "inserts the handle and a trailing space at the start of a line",
		"needs pi-tui's real CombinedAutocompleteProvider.applyCompletion, which runs in the host; this port delegates applyCompletion to it unchanged (see 'hands applyCompletion to pi')")
	tskip(t, f, "replaces only the mention token when it sits mid-line",
		"needs pi-tui's real CombinedAutocompleteProvider.applyCompletion, which runs in the host; the prefix this port hands it is the typed token, asserted by 'completes a mention typed mid-message'")
	tskip(t, f, "keeps text after the cursor intact",
		"needs pi-tui's real CombinedAutocompleteProvider.applyCompletion, which runs in the host")
	tskip(t, f, "inserts a FILE-shaped row from OUR prefix, character for character",
		"needs pi-tui's real CombinedAutocompleteProvider.applyCompletion, which runs in the host")
	tw(t, f, "declares a trigger character the editor will actually accept", func(t *testing.T) {
		for _, c := range providerOver(managerWith(), nil, true, newBuiltIn().p).TriggerCharacters {
			eq(t, len([]rune(c)), 1)
			eq(t, c == "/", false)
			eq(t, strings.TrimSpace(c), c)
		}
	})

	tw(t, f, "hands applyCompletion to pi — its @-branch already inserts value plus a space", func(t *testing.T) {
		p := providerOver(managerWith(), nil, true, newBuiltIn().p)
		got, err := p.ApplyCompletion(sdk.Context{}, []string{"@ex"}, 0, 3, sdk.AutocompleteItem{Value: "@explore", Label: "@explore"}, "@ex")
		eq(t, err, nil)
		eq(t, got, sdk.AutocompleteCompletion{Lines: []string{"applied"}, CursorLine: 0, CursorCol: 7})
	})
	tw(t, f, "keeps pi's file-completion gate rather than forcing it open", func(t *testing.T) {
		p := providerOver(managerWith(), nil, true, newBuiltIn().p)
		got, _ := p.ShouldTriggerFileCompletion(sdk.Context{}, []string{"/model"}, 0, 6)
		eq(t, got, false)
	})
	tw(t, f, "declares @ and nothing else", func(t *testing.T) {
		eq(t, providerOver(managerWith(), nil, true, newBuiltIn().p).TriggerCharacters, []string{"@"})
	})

	named := rec("explore", func(r *mentionRecord) { r.Alias = "auth-audit"; r.Description = "audit the auth flow" })
	tw(t, f, "lists a named agent once, under its alias, and says what type it is", func(t *testing.T) {
		got := agentRows(suggest(providerOver(managerWith(named), nil, true, newBuiltIn().p), "@"))
		eq(t, got, []sdk.AutocompleteItem{{Value: "@auth-audit", Label: "@auth-audit", Description: "send message · Explore · running · audit the auth flow"}})
	})
	tw(t, f, "leaves an unnamed agent's row free of a redundant type", func(t *testing.T) {
		eq(t, suggest(providerOver(explore(nil), nil, true, newBuiltIn().p), "@").Items[0].Description, "send message · running · find flaky tests")
	})
	handles := func(ts []mentionTarget) []string {
		var out []string
		for _, x := range ts {
			out = append(out, x.Handle)
		}
		return out
	}
	tw(t, f, "still resolves the unlisted type handle of a named agent", func(t *testing.T) {
		roster := mentionRoster(managerWith(named), []typeInfo{{"Explore", "search"}}, nil)
		eq(t, handles(roster), []string{"auth-audit"})
	})
	tw(t, f, "offers an evicted agent as a resume, after the live ones", func(t *testing.T) {
		got := agentRows(suggest(providerOver(fakeRoster{tombs: []tombstone{tomb(nil)}}, nil, true, newBuiltIn().p), "@"))
		eq(t, got, []sdk.AutocompleteItem{{Value: "@explore", Label: "@explore", Description: "resume · Explore · audit the RPC path"}})
	})
	tw(t, f, "puts live agents ahead of resumable ones", func(t *testing.T) {
		src := fakeRoster{records: []mentionRecord{rec("plan", func(r *mentionRecord) { r.Type = "Plan" })}, tombs: []tombstone{tomb(nil)}}
		eq(t, values(agentRows(suggest(providerOver(src, nil, true, newBuiltIn().p), "@"))), []string{"@plan", "@explore"})
	})
	tw(t, f, "keeps an aliased tombstone's type handle reserved too", func(t *testing.T) {
		roster := mentionRoster(fakeRoster{tombs: []tombstone{tomb(func(x *tombstone) { x.Alias = "auth-audit" })}}, []typeInfo{{"Explore", "search"}}, nil)
		eq(t, handles(roster), []string{"auth-audit"})
	})
	tw(t, f, "does not offer a startable type whose handle a tombstone still holds", func(t *testing.T) {
		roster := mentionRoster(fakeRoster{tombs: []tombstone{tomb(nil)}}, []typeInfo{{"Explore", "search"}}, nil)
		eq(t, len(roster), 1)
		eq(t, roster[0].Kind, "tombstone")
	})

	label := func(typ string) string {
		if typ == "Explore" {
			return "Auth Auditor"
		}
		return typ
	}
	withLabel := func(src rosterSource) *sdk.AutocompleteProvider {
		return newMentionProvider(newBuiltIn().p, func() []mentionTarget { return mentionRoster(src, nil, label) }, func() bool { return true })
	}
	tw(t, f, "names an aliased agent by its label", func(t *testing.T) {
		eq(t, suggest(withLabel(managerWith(named)), "@").Items[0].Description, "send message · Auth Auditor · running · audit the auth flow")
	})
	tw(t, f, "names a resumable agent by its label", func(t *testing.T) {
		eq(t, suggest(withLabel(fakeRoster{tombs: []tombstone{tomb(nil)}}), "@").Items[0].Description, "resume · Auth Auditor · audit the RPC path")
	})
	tw(t, f, "falls back to the raw type when no resolver is supplied", func(t *testing.T) {
		got := suggest(providerOver(fakeRoster{tombs: []tombstone{tomb(nil)}}, nil, true, newBuiltIn().p), "@")
		eq(t, strings.Contains(got.Items[0].Description, "· Explore ·"), true)
	})
}
