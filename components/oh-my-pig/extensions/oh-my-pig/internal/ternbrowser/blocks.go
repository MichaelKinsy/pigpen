package ternbrowser

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/ternwire"
)

// BlockEntry is one browser block of a Tern window.
// upstream: packages/coding-agent/src/tools/browser/tern/blocks.ts:13
type BlockEntry struct {
	Block  int    `json:"block"`
	URL    string `json:"url"`
	Title  string `json:"title"`
	Owner  *int   `json:"owner"`
	Shown  *bool  `json:"shown,omitempty"`
	Driven *bool  `json:"driven,omitempty"`
}

// Target describes a Tern browser block as browser.targets() lists it.
// upstream: packages/coding-agent/src/tools/browser/tern/blocks.ts:27
type Target struct {
	Target string `json:"target"`
	Title  string `json:"title"`
	URL    string `json:"url"`
	Shown  *bool  `json:"shown,omitempty"`
	Docked bool   `json:"docked"`
	Driven *bool  `json:"driven,omitempty"`
	Tab    string `json:"tab,omitempty"`
}

var allDigitsRE = regexp.MustCompile(`^\d+$`)

// jsonQuote matches JS JSON.stringify(s) without HTML-escaping &, <, >.
// Fixes A15: Go's json.Marshal escapes &, <, > as \u0026 etc.
func jsonQuote(s string) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s)
	return strings.TrimSuffix(buf.String(), "\n")
}

func oldTernNote(blocks []BlockEntry) string {
	for _, entry := range blocks {
		if entry.Driven == nil {
			return "\nThis Tern lists only the blocks scripts drive: update Tern to adopt the user's own."
		}
	}
	if len(blocks) == 0 {
		return "\nIf the user has browser blocks open, this Tern predates block adoption: update Tern."
	}
	return ""
}

// TernTargets converts window blocks into Targets, annotating blocks held by taken.
// upstream: packages/coding-agent/src/tools/browser/tern/blocks.ts:58
func TernTargets(blocks []BlockEntry, taken map[int]string) []Target {
	out := make([]Target, 0, len(blocks))
	for _, entry := range blocks {
		t := Target{
			Target: fmt.Sprintf("%d", entry.Block),
			Title:  entry.Title,
			URL:    entry.URL,
			Shown:  entry.Shown,
			Docked: entry.Owner == nil,
			Driven: entry.Driven,
		}
		if tab, ok := taken[entry.Block]; ok {
			t.Tab = tab
		}
		out = append(out, t)
	}
	return out
}

// DescribeTernBlock formats one block for the agent.
// upstream: packages/coding-agent/src/tools/browser/tern/blocks.ts:132
func DescribeTernBlock(entry BlockEntry, takenBy string) string {
	var facts []string
	if entry.Owner == nil {
		facts = append(facts, "docked")
	} else {
		facts = append(facts, fmt.Sprintf("picture-in-picture over block %d", *entry.Owner))
	}
	if entry.Shown != nil {
		if *entry.Shown {
			facts = append(facts, "on screen")
		} else {
			facts = append(facts, "not on screen")
		}
	}
	if takenBy != "" {
		facts = append(facts, fmt.Sprintf("driven by omp tab %s", jsonQuote(takenBy)))
	} else if entry.Driven != nil && *entry.Driven {
		facts = append(facts, "driven by a script")
	}
	return fmt.Sprintf("%d %s %s (%s)", entry.Block, jsonQuote(entry.Title), entry.URL, strings.Join(facts, ", "))
}

// DescribeTernTargets formats the browser.targets() listing.
// upstream: packages/coding-agent/src/tools/browser/tern/blocks.ts:75
func DescribeTernTargets(blocks []BlockEntry, taken map[int]string) string {
	update := oldTernNote(blocks)
	if len(blocks) == 0 {
		return "This Tern window lists no browser blocks." + update
	}
	lines := make([]string, 0, len(blocks))
	for _, entry := range blocks {
		lines = append(lines, "- "+DescribeTernBlock(entry, taken[entry.Block]))
	}
	return "Tern browser blocks (adopt one with target set to its id):\n" + strings.Join(lines, "\n") + update
}

// ListTernBlocks queries the daemon's "blocks" op.
// upstream: packages/coding-agent/src/tools/browser/tern/blocks.ts:83
func ListTernBlocks(ctx context.Context, client *ternwire.Client) ([]BlockEntry, error) {
	var ans struct {
		Blocks []json.RawMessage `json:"blocks"`
	}
	if err := client.Request(ctx, "browser", opBlocksReq{Op: "blocks"}, &ans); err != nil {
		return nil, err
	}
	var out []BlockEntry
	for _, raw := range ans.Blocks {
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil {
			continue
		}
		blockNum, ok := m["block"].(float64)
		if !ok {
			continue
		}
		entry := BlockEntry{
			Block: int(blockNum),
			URL:   "about:blank",
		}
		if u, ok := m["url"].(string); ok {
			entry.URL = u
		}
		if t, ok := m["title"].(string); ok {
			entry.Title = t
		}
		if o, ok := m["owner"].(float64); ok {
			ov := int(o)
			entry.Owner = &ov
		}
		if s, ok := m["shown"].(bool); ok {
			entry.Shown = &s
		}
		if d, ok := m["driven"].(bool); ok {
			entry.Driven = &d
		}
		out = append(out, entry)
	}
	return out, nil
}

// PickTernBlock picks the block matching target (exact digit ID, else
// case-insensitive URL/title substring ranked by shown > user-owned > first).
// upstream: packages/coding-agent/src/tools/browser/tern/blocks.ts:110
func PickTernBlock(blocks []BlockEntry, target string, taken map[int]string) *BlockEntry {
	needle := strings.TrimSpace(target)
	var free []BlockEntry
	for _, b := range blocks {
		if _, isTaken := taken[b.Block]; !isTaken {
			free = append(free, b)
		}
	}
	if allDigitsRE.MatchString(needle) {
		for i := range free {
			if fmt.Sprintf("%d", free[i].Block) == needle {
				return &free[i]
			}
		}
	}
	lower := strings.ToLower(needle)
	rank := func(e BlockEntry) int {
		r := 0
		if e.Shown != nil && *e.Shown {
			r += 2
		}
		if e.Driven != nil && !*e.Driven {
			r += 1
		}
		return r
	}
	var best *BlockEntry
	bestRank := -1
	for i := range free {
		e := free[i]
		if !strings.Contains(strings.ToLower(e.URL), lower) && !strings.Contains(strings.ToLower(e.Title), lower) {
			continue
		}
		r := rank(e)
		if best == nil || r > bestRank {
			best = &free[i]
			bestRank = r
		}
	}
	return best
}

// NoTernBlockError returns the error when no block matches target.
// upstream: packages/coding-agent/src/tools/browser/tern/blocks.ts:141
func NoTernBlockError(blocks []BlockEntry, target string, taken map[int]string) error {
	update := oldTernNote(blocks)
	if len(blocks) == 0 {
		return fmt.Errorf(
			"No Tern browser block to adopt for target %s: this Tern window lists no browser blocks. Ask the user to open one, or omit target to open a picture-in-picture over this pane. browser.targets() lists the blocks you can adopt.%s",
			jsonQuote(target), update,
		)
	}
	lines := make([]string, 0, len(blocks))
	for _, entry := range blocks {
		lines = append(lines, "- "+DescribeTernBlock(entry, taken[entry.Block]))
	}
	return fmt.Errorf(
		"No Tern browser block matches target %s (a block id, or a URL/title substring). Browser blocks in this Tern window:\n%s\nRetry with target set to one of these ids; browser.targets() lists them before adopting.%s",
		jsonQuote(target), strings.Join(lines, "\n"), update,
	)
}
