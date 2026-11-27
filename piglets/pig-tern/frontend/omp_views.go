package frontend

import (
	"bytes"
	"fmt"
	"image"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	fe "github.com/MichaelKinsy/PiG/extensions/sdk/frontend"
	"github.com/stencil-hq/tern-sdk/go/tern"
)

// ompViewRole strips an optional `#<index>` suffix from a ViewNode.ID and
// returns the `omp.*` role name when the ID carries one.
func ompViewRole(id string) (string, bool) {
	if !strings.HasPrefix(id, "omp.") {
		return "", false
	}
	if i := strings.IndexByte(id, '#'); i >= 0 {
		id = id[:i]
	}
	if len(id) <= len("omp.") {
		return "", false
	}
	return id, true
}

// ompViewHeadPatch holds result-phase tool head updates derived from a tool's
// structured ResultView (e.g. todo `closed/total`, `open:true`, `preview:"none"`;
// ask answered `frame:"card"`, first question & question count; task `N agent(s)`
// and `background` badge; wait title/tone/frame; web_search error tone/frame;
// grep/glob/ast_edit counts & badges).
type ompViewHeadPatch struct {
	title      any
	target     string
	targetKind string
	meta       []any
	metaSet    bool
	badges     []any
	frame      string
	tone       string
	exit       *int
	preview    any
	open       bool
}

// ompViewBodyChildren walks a ToolCard's native ResultView when ompRoles is
// active. Native views carry omp.* role IDs with raw payloads and no terminal
// borders or chrome; role-less nodes keep pig-tern's full viewer layout, style,
// and theme handling via viewer.ompMap.
//
// When the view has no theme overrides and its root is a tool container role
// (such as `omp.tool.bash`, `omp.tool.eval`, `omp.tool.task`, etc. from
// internal/ansi/VIEW-IDS.md), the root's children are unwrapped directly under
// the `tool` node (dropping any redundant `omp.tool.head` duplicate), so Tern
// child selectors match omp's tree directly.
func (s *session) ompViewBodyChildren(id string, tool fe.ToolCard, v *fe.View, lines []string) []tern.WireNode {
	children, _ := s.ompViewBodyChildrenWithHead(id, tool, v, lines)
	return children
}

func (s *session) ompViewBodyChildrenWithHead(id string, tool fe.ToolCard, v *fe.View, lines []string) ([]tern.WireNode, ompViewHeadPatch) {
	s.dropViewRules(id)
	s.viewsShown = true
	b := &viewer{
		s:      s,
		lines:  lines,
		tokens: s.viewTokens(nil, v.Theme),
		theme:  v.Theme,
	}
	ctx := &ompViewCtx{s: s, tool: tool}
	if pigID, ok := strings.CutPrefix(id, "n."); ok {
		if e := s.sent[pigID]; e != nil {
			ctx.entry = e
			ctx.startedAt = e.startedAt
		}
	}
	ctx.bashOutput = ompExtractBashOutput(b, v, lines, tool)
	b.ompMap = ctx.mapNode

	viewID := id + ".view"
	themeRole := s.themeRole(viewID, v.Theme, v.Theme, &v.Root)

	rootRole, hasRootRole := ompViewRole(v.Root.ID)
	// If there are view-level theme overrides, wrap in one themed col so CSS
	// variables scope cleanly; otherwise emit directly under the tool node.
	if themeRole != viewRole {
		root := b.node(&v.Root, viewID+".0", region{0, 0, v.Root.Rows, v.Root.Width}, []int{})
		ctx.finalizeHeadPatch(rootRole)
		if root.Kind == "" {
			return nil, ctx.headPatch
		}
		return []tern.WireNode{
			wireNode(viewID, tern.Node{Kind: tern.KindCol, Props: map[string]any{"role": themeRole}, Children: []tern.Element{root}}),
		}, ctx.headPatch
	}

	// Unwrap internal/ansi top-level tool container (e.g. `omp.tool.bash`) so
	// its body children sit directly under `tool` as omp's describeResult does.
	if hasRootRole && isOmpToolRootWrapper(rootRole) && len(v.Root.Children) > 0 {
		// Special case: ask ResultView wraps answered sections inside an
		// `omp.ask.card.body` VStack whose children are `omp.ask.fold#<i>`.
		// Upstream tools/ask.ts:187-209 emits one `section` per answer directly
		// under `tool[omp.tool.ask]` (with frame:"card"). Unwrap those sections
		// directly so Tern's DOM nesting matches omp.
		if rootRole == "omp.tool.ask" && ompAskResultHasAnsweredBody(&v.Root) {
			out := ompBuildAskAnswerSections(b, ctx, &v.Root, id)
			if len(out) > 0 {
				ctx.finalizeHeadPatch(rootRole)
				return out, ctx.headPatch
			}
		}

		out := make([]tern.WireNode, 0, len(v.Root.Children))
		consumedHead := false
		row := 0
		for i := range v.Root.Children {
			child := &v.Root.Children[i]
			childRegion := region{row, 0, child.Rows, v.Root.Width}
			row += child.Rows + v.Root.Gap*boolInt(v.Root.Kind == fe.ViewKindVStack)
			if cr, ok := ompViewRole(child.ID); ok {
				if cr == "omp.tool.head" {
					// The tool card head already renders the title/target/meta.
					consumedHead = true
					continue
				}
				if ctx.consumeHeadNode(b, cr, child, childRegion) {
					consumedHead = true
					continue
				}
			}
			cid := id + ".v" + strconv.Itoa(i)
			el := b.node(child, cid, childRegion, []int{i})
			if el.Kind == "" {
				consumedHead = true
				continue
			}
			out = append(out, wireNode(cid, el))
		}
		if len(out) > 0 || consumedHead {
			ctx.finalizeHeadPatch(rootRole)
			return out, ctx.headPatch
		}
	}

	root := b.node(&v.Root, viewID, region{0, 0, v.Root.Rows, v.Root.Width}, []int{})
	ctx.finalizeHeadPatch(rootRole)
	if root.Kind == "" {
		return nil, ctx.headPatch
	}
	return []tern.WireNode{wireNode(viewID, root)}, ctx.headPatch
}
func isOmpToolRootWrapper(role string) bool {
	if !strings.HasPrefix(role, "omp.tool.") {
		return false
	}
	rest := strings.TrimPrefix(role, "omp.tool.")
	// Single-segment tool wrapper like omp.tool.bash, omp.tool.eval, omp.tool.task,
	// or `.call` variant like omp.tool.bash.call.
	if !strings.Contains(rest, ".") {
		switch rest {
		case "head", "error", "notice", "stats", "context", "diagnostics", "diagnostic",
			"file", "files", "chip", "answer", "sources", "source", "excerpt", "args", "output":
			return false
		default:
			return true
		}
	}
	return strings.HasSuffix(rest, ".call")
}

type ompShellMemo struct {
	cmd      string
	running  bool
	hasExit  bool
	exitCode int
	output   string
	pres     map[string]any
}

type ompViewCtx struct {
	s          *session
	tool       fe.ToolCard
	entry      *mainEntry
	startedAt  time.Time
	bashOutput string
	// localStopped tracks per-cell (`omp.run.foot`) or per-job (`omp.job.tile.head`)
	// settled state while recursing into their children, so a finished cell/job
	// inside a still-running eval/wait freezes its elapsed timer (fixes N4).
	localStopped bool
	// inJobTileHead is true while walking children of `omp.job.tile.head`: omp's
	// `describeJobTile` (tools/job-widget.ts:409,450) emits a bare `elapsed` node
	// with no `role` prop, whereas `runFoot` (tools/native-view.ts:263) sets
	// `role: "omp.run.time"`.
	inJobTileHead  bool
	headPatch      ompViewHeadPatch
	taskAgentCount int
	taskBackground bool
	hasErrorChild  bool
	// wait tile status counts + agent rows.
	waitCompleted int
	waitFailed    int
	waitCancelled int
	waitRunning   int
	waitAgents    int
	waitInterrupt bool
	// grep / glob / ast_edit counts.
	grepFiles       int
	grepMatches     int
	curSearchFile   string
	curSearchMeta   string
	globFiles       int
	astEditFiles    int
	astEditChanges  int
	astEditProposed bool
	explicitHead    ompViewHeadPatch
	hasExpMeta      bool
	hasExpBadges    bool
}

// consumeHeadNode consumes an explicit `omp.head.*` metadata child emitted by
// internal/ansi native builders so result-phase head facts (counts, scope,
// badges, tone, frame, exit code) reach Tern without ToolCard.Details.
func (c *ompViewCtx) consumeHeadNode(b *viewer, role string, n *fe.ViewNode, r region) bool {
	if !strings.HasPrefix(role, "omp.head.") {
		return false
	}
	val := ompViewLeafPlainText(b, n, r)
	switch role {
	case "omp.head.target":
		c.explicitHead.target = val
	case "omp.head.target_kind":
		c.explicitHead.targetKind = val
	case "omp.head.meta":
		c.hasExpMeta = true
		if val != "" {
			c.explicitHead.meta = append(c.explicitHead.meta, val)
		}
	case "omp.head.badge":
		c.hasExpBadges = true
		if val != "" {
			if text, tone, hasTone := strings.Cut(val, ":"); hasTone {
				c.explicitHead.badges = append(c.explicitHead.badges, map[string]any{"text": text, "tone": tone})
			} else {
				c.explicitHead.badges = append(c.explicitHead.badges, map[string]any{"text": val})
			}
		}
	case "omp.head.tone":
		c.explicitHead.tone = val
	case "omp.head.frame":
		c.explicitHead.frame = val
	case "omp.head.exit":
		if code, err := strconv.Atoi(val); err == nil {
			c.explicitHead.exit = &code
		}
	}
	return true
}

// stripOmpHeadResultLines removes rows belonging to `omp.head.*` (or
// `omp.tool.head`) nodes from tool.Result when a ResultView is attached but
// rendering falls back to plain lines (e.g. validView==false or read error
// fallback). Also extracts head facts into headPatch so even on row/view
// mismatch the tool head still receives them and head facts never leak into
// body lines.
func stripOmpHeadResultLines(v *fe.View, lines []string) ([]string, ompViewHeadPatch) {
	var patch ompViewHeadPatch
	if v == nil || len(lines) == 0 {
		return lines, patch
	}
	ctx := &ompViewCtx{}
	b := &viewer{lines: lines}
	drop := make([]bool, len(lines))
	var walk func(n *fe.ViewNode, row int)
	walk = func(n *fe.ViewNode, row int) {
		if role, ok := ompViewRole(n.ID); ok && (strings.HasPrefix(role, "omp.head.") || role == "omp.tool.head") {
			r := region{row, 0, n.Rows, n.Width}
			if strings.HasPrefix(role, "omp.head.") {
				ctx.consumeHeadNode(b, role, n, r)
			}
			if n.Rows > 0 {
				for idx := row; idx < row+n.Rows && idx < len(lines); idx++ {
					if idx >= 0 {
						drop[idx] = true
					}
				}
			}
			return
		}
		if len(n.Children) == 0 {
			return
		}
		cur := row
		if n.Kind == fe.ViewKindBox {
			cur += n.PaddingY
		}
		for i := range n.Children {
			ch := &n.Children[i]
			if n.Kind == fe.ViewKindHStack {
				walk(ch, cur)
			} else {
				walk(ch, cur)
				cur += ch.Rows + n.Gap*boolInt(n.Kind == fe.ViewKindVStack)
			}
		}
	}
	walk(&v.Root, 0)
	ctx.applyExplicitHead()
	anyDropped := false
	for _, d := range drop {
		if d {
			anyDropped = true
			break
		}
	}
	if !anyDropped {
		return lines, ctx.headPatch
	}
	out := make([]string, 0, len(lines))
	for i, l := range lines {
		if !drop[i] {
			out = append(out, l)
		}
	}
	return out, ctx.headPatch
}

func (c *ompViewCtx) finalizeHeadPatch(rootRole string) {
	defer c.applyExplicitHead()
	// General error tone rule (chat/tool-execution.ts:1497): if the ResultView
	// carries an `omp.tool.error` body node (even when PiG's ToolCard.Status is
	// ToolDone, e.g. web_search soft errors), tint the tool card tone="error".
	if c.hasErrorChild && c.tool.Name != "bash" && c.headPatch.tone == "" {
		c.headPatch.tone = "error"
	}
	switch rootRole {
	case "omp.tool.task", "omp.tool.subagent":
		if c.taskAgentCount > 0 {
			if c.taskAgentCount == 1 {
				c.headPatch.target = "1 agent"
			} else {
				c.headPatch.target = fmt.Sprintf("%d agents", c.taskAgentCount)
			}
			c.headPatch.targetKind = "text"
			c.headPatch.preview = "none"
			if c.taskBackground {
				c.headPatch.badges = []any{map[string]any{"text": "background"}}
			}
		}

	case "omp.tool.wait":
		// upstream: tools/wait.ts:803-905
		if c.hasErrorChild {
			c.headPatch.frame = "card"
			c.headPatch.tone = "error"
			return
		}
		if c.waitInterrupt {
			c.headPatch.frame = "inline"
			c.headPatch.tone = "info"
			return
		}
		totalJobs := c.waitCompleted + c.waitFailed + c.waitCancelled + c.waitRunning
		c.headPatch.frame = "card"
		if totalJobs == 0 && c.waitAgents == 0 {
			c.headPatch.tone = "warning"
			return
		}
		c.headPatch.preview = "children"
		noun := "jobs"
		if totalJobs == 1 {
			noun = "job"
		}
		var titleStr string
		switch {
		case totalJobs == 0:
			agentNoun := "agents"
			if c.waitAgents == 1 {
				agentNoun = "agent"
			}
			titleStr = fmt.Sprintf("%d running %s — no jobs", c.waitAgents, agentNoun)
		case c.waitRunning > 0:
			if c.waitRunning == totalJobs {
				titleStr = fmt.Sprintf("waiting on %d %s", totalJobs, noun)
			} else {
				titleStr = fmt.Sprintf("waiting on %d of %d %s", c.waitRunning, totalJobs, noun)
			}
		default:
			titleStr = fmt.Sprintf("%d %s settled", totalJobs, noun)
		}
		titleSpans := []any{map[string]any{"t": titleStr, "s": "toolTitle strong"}}
		if c.waitCompleted > 0 {
			titleSpans = append(titleSpans, map[string]any{"t": fmt.Sprintf(" %d done", c.waitCompleted), "s": "success"})
		}
		if c.waitFailed > 0 {
			titleSpans = append(titleSpans, map[string]any{"t": fmt.Sprintf(" %d failed", c.waitFailed), "s": "error"})
		}
		if c.waitCancelled > 0 {
			titleSpans = append(titleSpans, map[string]any{"t": fmt.Sprintf(" %d cancelled", c.waitCancelled), "s": "warning"})
		}
		c.headPatch.title = titleSpans
		switch {
		case c.waitFailed > 0:
			c.headPatch.tone = "warning"
		case c.waitRunning > 0 || c.waitAgents > 0:
			c.headPatch.tone = "info"
		default:
			c.headPatch.tone = "success"
		}

	case "omp.tool.web_search":
		// upstream: tools/web-search.ts:334-401 — describeResult always omits `inline` (frame:"card").
		c.headPatch.frame = "card"
		if c.hasErrorChild {
			c.headPatch.tone = "error"
		}

	case "omp.tool.write":
		// upstream: tools/write.ts:770, 806 — NATIVE_WRITE_PREVIEW is {lines: 8}.
		c.headPatch.preview = map[string]any{"lines": 8}

	case "omp.tool.grep":
		// upstream: tools/grep.ts:327-370 — head.meta = [scope, "<M> matches · <F> files"], preview:{lines:12}.
		c.headPatch.preview = map[string]any{"lines": 12}
		if c.hasErrorChild {
			return
		}
		scope := firstStringArg(c.tool.Arguments, "path")
		if scope == "" {
			scope = "."
		}
		if c.grepFiles > 0 || c.grepMatches > 0 {
			mNoun := "matches"
			if c.grepMatches == 1 {
				mNoun = "match"
			}
			fNoun := "files"
			if c.grepFiles == 1 {
				fNoun = "file"
			}
			c.headPatch.meta = []any{"in " + scope, fmt.Sprintf("%d %s · %d %s", c.grepMatches, mNoun, c.grepFiles, fNoun)}
			c.headPatch.metaSet = true
		}

	case "omp.tool.glob":
		// upstream: tools/glob.ts:345-376 — head.meta = ["<N> files", "in <scope>"], preview:{lines:8}.
		c.headPatch.preview = map[string]any{"lines": 8}
		if c.hasErrorChild {
			return
		}
		if c.globFiles > 0 {
			fNoun := "files"
			if c.globFiles == 1 {
				fNoun = "file"
			}
			scope := "."
			if raw := firstStringArg(c.tool.Arguments, "path"); raw != "" {
				if !strings.ContainsAny(raw, "*?[") {
					scope = raw
				}
			}
			c.headPatch.meta = []any{fmt.Sprintf("%d %s", c.globFiles, fNoun), "in " + scope}
			c.headPatch.metaSet = true
		}

	case "omp.tool.lsp":
		// upstream: tools/lsp.ts:1100, 1125 — fallback body preview is 4 lines.
		c.headPatch.preview = map[string]any{"lines": 4}

	case "omp.tool.ast_edit":
		// upstream: tools/ast-edit.ts:385-401 — target "N file(s)", badge "proposed" when dry-run,
		// meta ["N replacement(s)"], preview:{lines:6}.
		c.headPatch.preview = map[string]any{"lines": 6}
		if c.hasErrorChild {
			return
		}
		if c.astEditFiles > 0 || c.astEditChanges > 0 {
			fc := c.astEditFiles
			if fc == 0 {
				fc = 1
			}
			fNoun := "files"
			if fc == 1 {
				fNoun = "file"
			}
			c.headPatch.target = fmt.Sprintf("%d %s", fc, fNoun)
			c.headPatch.targetKind = "text"
			if c.astEditChanges > 0 {
				rNoun := "replacements"
				if c.astEditChanges == 1 {
					rNoun = "replacement"
				}
				c.headPatch.meta = []any{fmt.Sprintf("%d %s", c.astEditChanges, rNoun)}
				c.headPatch.metaSet = true
			}
			if c.astEditProposed {
				c.headPatch.badges = []any{map[string]any{"text": "proposed", "tone": "warning"}}
			}
		}
	}
}

// applyExplicitHead overlays any explicit `omp.head.*` nodes emitted by
// internal/ansi onto the body-derived headPatch.
func (c *ompViewCtx) applyExplicitHead() {
	if c.explicitHead.target != "" {
		c.headPatch.target = c.explicitHead.target
		if c.explicitHead.targetKind != "" {
			c.headPatch.targetKind = c.explicitHead.targetKind
		} else if c.headPatch.targetKind == "" {
			c.headPatch.targetKind = "text"
		}
	} else if c.explicitHead.targetKind != "" {
		c.headPatch.targetKind = c.explicitHead.targetKind
	}
	if c.hasExpMeta {
		c.headPatch.meta = c.explicitHead.meta
		c.headPatch.metaSet = true
	}
	if c.hasExpBadges {
		c.headPatch.badges = c.explicitHead.badges
	}
	if c.explicitHead.tone != "" {
		c.headPatch.tone = c.explicitHead.tone
	}
	if c.explicitHead.frame != "" {
		c.headPatch.frame = c.explicitHead.frame
	}
	if c.explicitHead.exit != nil {
		c.headPatch.exit = c.explicitHead.exit
	}
}

// ompAskResultHasAnsweredBody reports whether an `omp.tool.ask` ResultView
// carries an `omp.ask.card.body` container of `omp.ask.fold` answer sections
// (as emitted by internal/ansi/native_structured.go:653).
func ompAskResultHasAnsweredBody(root *fe.ViewNode) bool {
	for i := range root.Children {
		if r, ok := ompViewRole(root.Children[i].ID); ok && r == "omp.ask.card.body" && len(root.Children[i].Children) > 0 {
			return true
		}
	}
	return false
}

// ompBuildAskAnswerSections converts `omp.tool.ask` ResultView's
// `omp.ask.card.body > omp.ask.fold#<i>` children into omp's direct
// `section(text "<question>", row[omp.tool.answer](icon check, text "<answer>"), ...notes)`
// nodes under `tool[omp.tool.ask]` (upstream: packages/tui/src/tools/ask.ts:169-209).
func ompBuildAskAnswerSections(b *viewer, ctx *ompViewCtx, root *fe.ViewNode, id string) []tern.WireNode {
	out := make([]tern.WireNode, 0, len(root.Children))
	row := 0
	var firstQuestion string
	answerCount := 0
	secIdx := 0
	for i := range root.Children {
		child := &root.Children[i]
		childRegion := region{row, 0, child.Rows, root.Width}
		row += child.Rows + root.Gap*boolInt(root.Kind == fe.ViewKindVStack)
		cr, ok := ompViewRole(child.ID)
		if ok && cr == "omp.tool.head" {
			continue
		}
		if ok && ctx.consumeHeadNode(b, cr, child, childRegion) {
			continue
		}
		if ok && cr == "omp.ask.card.body" && len(child.Children) > 0 {
			subRow := childRegion.row
			for j := range child.Children {
				fold := &child.Children[j]
				foldRegion := region{subRow, childRegion.col, fold.Rows, childRegion.width}
				subRow += fold.Rows + child.Gap*boolInt(child.Kind == fe.ViewKindVStack)
				fr, _ := ompViewRole(fold.ID)
				if fr != "omp.ask.fold" {
					cid := id + ".v" + strconv.Itoa(secIdx)
					if el := b.node(fold, cid, foldRegion, []int{i, j}); el.Kind != "" {
						secIdx++
						out = append(out, wireNode(cid, el))
					}
					continue
				}
				secChildren := make([]tern.Element, 0, len(fold.Children))
				fRow := foldRegion.row
				for k := range fold.Children {
					gc := &fold.Children[k]
					gcRegion := region{fRow, foldRegion.col, gc.Rows, foldRegion.width}
					fRow += gc.Rows + fold.Gap*boolInt(fold.Kind == fe.ViewKindVStack)
					gcr, _ := ompViewRole(gc.ID)
					if gcr == "omp.ask.fold.question" {
						qText := ompViewLeafPlainText(b, gc, gcRegion)
						if firstQuestion == "" && qText != "" {
							firstQuestion = qText
						}
						// upstream: tools/ask.ts:184 — `text([span(oneLine(answer.question))], { wrap: "word" })`
						secChildren = append(secChildren, tern.Node{
							Kind: tern.KindText,
							Props: map[string]any{
								"wrap":  "word",
								"spans": []any{map[string]any{"t": qText}},
							},
						})
						continue
					}
					cid := id + "." + strconv.Itoa(secIdx) + "." + strconv.Itoa(k)
					if el := b.node(gc, cid, gcRegion, []int{i, j, k}); el.Kind != "" {
						secChildren = append(secChildren, el)
					}
				}
				answerCount++
				cid := id + "." + strconv.Itoa(secIdx)
				secIdx++
				out = append(out, wireNode(cid, tern.Node{
					Kind:     tern.KindSection,
					Props:    map[string]any{},
					Children: secChildren,
				}))
			}
			continue
		}
		cid := id + ".v" + strconv.Itoa(secIdx)
		if el := b.node(child, cid, childRegion, []int{i}); el.Kind != "" {
			secIdx++
			out = append(out, wireNode(cid, el))
		}
	}
	if answerCount > 0 {
		ctx.headPatch.frame = "card"
		if firstQuestion != "" {
			ctx.headPatch.target = firstQuestion
			ctx.headPatch.targetKind = "text"
		}
		if answerCount > 1 {
			ctx.headPatch.meta = []any{fmt.Sprintf("%d questions", answerCount)}
		}
	}
	return out
}

// ompViewChildren maps the children of any container node (Container, VStack,
// HStack, Box) while preserving gap, box padding, stack flex props, and
// recursing through b.node so nested omp.* roles are never dropped.
func ompViewChildren(b *viewer, n *fe.ViewNode, id string, r region, path []int) []tern.Element {
	if len(n.Children) == 0 {
		return nil
	}
	children := make([]tern.Element, 0, len(n.Children))
	switch n.Kind {
	case fe.ViewKindHStack:
		col := r.col
		for i := range n.Children {
			child := &n.Children[i]
			if el := b.stackChild(child, id+"."+strconv.Itoa(i), region{r.row, col, r.rows, child.Width}, n, true, childPath(path, i)); el.Kind != "" {
				children = append(children, el)
			}
			col += child.Width + n.Gap
		}
	case fe.ViewKindBox:
		inner := region{r.row + n.PaddingY, r.col + n.PaddingX, max(r.rows-2*n.PaddingY, 0), max(r.width-2*n.PaddingX, 0)}
		row := inner.row
		for i := range n.Children {
			child := &n.Children[i]
			if el := b.node(child, id+"."+strconv.Itoa(i), region{row, inner.col, child.Rows, inner.width}, childPath(path, i)); el.Kind != "" {
				children = append(children, el)
			}
			row += child.Rows
		}
	default:
		row := r.row
		for i := range n.Children {
			child := &n.Children[i]
			if el := b.stackChild(child, id+"."+strconv.Itoa(i), region{row, r.col, child.Rows, r.width}, n, false, childPath(path, i)); el.Kind != "" {
				children = append(children, el)
			}
			row += child.Rows + n.Gap*boolInt(n.Kind == fe.ViewKindVStack)
		}
	}
	return children
}

// ompViewLeafPlainText returns the sanitized plain text of a leaf ViewNode.
// For `Lines` nodes (which carry `n.Lines` or fall back to `b.cut(r)`), it
// reads `n.Lines` first before falling back to `b.cut(r)`.
func ompViewLeafPlainText(b *viewer, n *fe.ViewNode, r region) string {
	return strings.TrimSpace(stripANSI(ompViewLeafRawText(b, n, r)))
}

// ompViewLeafCodeText returns the ANSI-stripped code/diff payload with leading
// indentation (including literal tabs) preserved and trailing whitespace trimmed.
func ompViewLeafCodeText(b *viewer, n *fe.ViewNode, r region) string {
	return strings.TrimRight(stripANSI(ompViewLeafRawText(b, n, r)), " \t\r\n")
}
func ompViewLeafRawText(b *viewer, n *fe.ViewNode, r region) string {
	if n.Text != "" {
		return n.Text
	}
	if len(n.Lines) > 0 {
		return strings.Join(n.Lines, "\n")
	}
	return strings.Join(b.cut(r), "\n")
}

// ompViewSectionHeadAndBody separates a section's head line from its body
// children without swallowing the entire subtree text into head (fixes B3).
func ompViewSectionHeadAndBody(b *viewer, n *fe.ViewNode, id string, r region, path []int) (string, []tern.Element) {
	if len(n.Children) == 0 {
		return ompViewLeafPlainText(b, n, r), nil
	}
	// If the first child is a dedicated head text (e.g. `omp.tool.edit.file.head`
	// or a plain Text child), use it as the section head and render the rest as body.
	first := &n.Children[0]
	firstRole, _ := ompViewRole(first.ID)
	useFirstAsHead := strings.HasSuffix(firstRole, ".head") ||
		(firstRole == "" && (first.Kind == fe.ViewKindText || first.Kind == fe.ViewKindTruncatedText))

	startIdx := 0
	head := ""
	row := r.row
	if n.Kind == fe.ViewKindBox {
		row += n.PaddingY
	}
	innerCol := r.col
	innerWidth := r.width
	if n.Kind == fe.ViewKindBox {
		innerCol += n.PaddingX
		innerWidth = max(r.width-2*n.PaddingX, 0)
	}
	if useFirstAsHead {
		head = ompViewLeafPlainText(b, first, region{row, innerCol, first.Rows, innerWidth})
		row += first.Rows + n.Gap*boolInt(n.Kind == fe.ViewKindVStack)
		startIdx = 1
	}
	body := make([]tern.Element, 0, len(n.Children)-startIdx)
	for i := startIdx; i < len(n.Children); i++ {
		child := &n.Children[i]
		if el := b.stackChild(child, id+"."+strconv.Itoa(i), region{row, innerCol, child.Rows, innerWidth}, n, false, childPath(path, i)); el.Kind != "" {
			body = append(body, el)
		}
		row += child.Rows + n.Gap*boolInt(n.Kind == fe.ViewKindVStack)
	}
	return head, body
}

// ompViewDurationMs parses a short duration string like "1.2s", "450ms", "2m 5s",
// or "⏱ 1.2s/5.0s" into milliseconds for `elapsed` nodes.
var ompDurationPartRe = regexp.MustCompile(`(\d+(?:\.\d+)?)\s*(ms|s|m|h)\b`)

func ompParseDurationMs(s string) (int64, bool) {
	// Stop at `/` if formatted like `1.2s/5.0s` (elapsed/timeout).
	if cut, _, ok := strings.Cut(s, "/"); ok {
		s = cut
	}
	matches := ompDurationPartRe.FindAllStringSubmatch(s, -1)
	if len(matches) == 0 {
		return 0, false
	}
	var totalMs float64
	for _, m := range matches {
		val, err := strconv.ParseFloat(m[1], 64)
		if err != nil {
			continue
		}
		switch m[2] {
		case "ms":
			totalMs += val
		case "s":
			totalMs += val * 1000
		case "m":
			totalMs += val * 60_000
		case "h":
			totalMs += val * 3_600_000
		}
	}
	return int64(totalMs + 0.5), true
}

func (c *ompViewCtx) mapNode(b *viewer, n *fe.ViewNode, id string, r region, path []int) (tern.Node, bool) {
	role, isOmp := ompViewRole(n.ID)
	if !isOmp {
		return tern.Node{}, false
	}
	if c.consumeHeadNode(b, role, n, r) {
		// Return empty-Kind node so every caller strips metadata nodes from output.
		return tern.Node{}, true
	}

	switch role {
	case "omp.run":
		// Drop redundant omp.tool.head child inside omp.run (internal/ansi emits it for terminal mode).
		rawChildren := ompViewChildren(b, n, id, r, path)
		children := make([]tern.Element, 0, len(rawChildren))
		for i, ch := range rawChildren {
			if i < len(n.Children) {
				if cr, ok := ompViewRole(n.Children[i].ID); ok && cr == "omp.tool.head" {
					continue
				}
			}
			children = append(children, ch)
		}
		props := map[string]any{"role": role, "gap": "none"}
		if c.tool.Status == fe.ToolError {
			props["tone"] = "error"
		}
		return tern.Node{Kind: tern.KindCol, Props: hiddenIf(props, n.Rows == 0), Children: children}, true

	case "omp.run.foot":
		// upstream: tools/native-view.ts:268-277
		if len(n.Children) > 0 {
			prev := c.localStopped
			c.localStopped = ompFootSettled(b, n, r)
			children := ompViewChildren(b, n, id, r, path)
			c.localStopped = prev
			return tern.Node{
				Kind:     tern.KindRow,
				Props:    hiddenIf(map[string]any{"role": role, "gap": "sm", "align": "center"}, n.Rows == 0),
				Children: children,
			}, true
		}
		// Single-text foot (e.g. eval cell foot emitted as `*kit.Text`).
		plain := ompViewLeafPlainText(b, n, r)
		return tern.Node{
			Kind:  tern.KindRow,
			Props: hiddenIf(map[string]any{"role": role, "gap": "sm", "align": "center"}, n.Rows == 0 || plain == ""),
			Children: []tern.Element{
				tern.Node{Kind: tern.KindText, Props: map[string]any{"role": "omp.run.state", "tone": ompRunStateTone(plain), "spans": []any{map[string]any{"t": plain}}}},
			},
		}, true

	case "omp.run.state":
		// upstream: tools/native-view.ts:272
		plain := ompViewLeafPlainText(b, n, r)
		return tern.Node{
			Kind: tern.KindText,
			Props: map[string]any{
				"role":  role,
				"tone":  ompRunStateTone(plain),
				"spans": []any{map[string]any{"t": plain}},
			},
		}, true

	case "omp.run.time":
		// upstream: tools/native-view.ts:257-267 (`runFoot`, role:"omp.run.time")
		// vs tools/job-widget.ts:409,450 (`describeJobTile`, bare `elapsed`, no role).
		plain := ompViewLeafPlainText(b, n, r)
		ms, ok := ompParseDurationMs(plain)
		if !ok && c.tool.Elapsed > 0 {
			ms = c.tool.Elapsed.Milliseconds()
		}
		props := map[string]any{
			"age":    ms,
			"format": "short",
		}
		if !c.inJobTileHead {
			props["role"] = role
		}
		toolLive := c.tool.Status == fe.ToolPending || c.tool.Status == fe.ToolRunning
		if !toolLive || c.localStopped {
			props["stopped"] = ms
		}
		return tern.Node{Kind: tern.KindElapsed, Props: props}, true
	case "omp.run.facts":
		// upstream: tools/native-view.ts:274
		plain := ompViewLeafPlainText(b, n, r)
		return tern.Node{
			Kind: tern.KindText,
			Props: map[string]any{
				"role":  role,
				"wrap":  "word",
				"spans": []any{map[string]any{"t": plain, "s": "muted"}},
			},
		}, true

	case "omp.run.caption":
		// upstream: tools/eval.ts:917
		props := map[string]any{"role": role}
		b.setRich(props, "text", "spans", ompViewLeafRawText(b, n, r))
		return tern.Node{Kind: tern.KindText, Props: props}, true

	case "omp.run.status":
		// upstream: tools/eval.ts:874-879
		return tern.Node{
			Kind:     tern.KindCol,
			Props:    hiddenIf(map[string]any{"role": role, "gap": "none"}, n.Rows == 0),
			Children: ompViewChildren(b, n, id, r, path),
		}, true

	// ── Bash steps, command & output (tools/bash.ts:503-541, 1051-1060; tools/tool-commands.ts:232-236) ──
	case "omp.tool.bash.command":
		// upstream: tools/bash.ts:503, 950-951, 1036-1051, native-view.ts:281-286
		displayCmd := ompViewLeafCodeText(b, n, r)
		rawCmd, _ := c.tool.Arguments["command"].(string)
		if c.s != nil && c.s.buildShellPresentationFn != nil && strings.TrimSpace(rawCmd) != "" {
			running := c.tool.Status == fe.ToolRunning
			var exitCode *int
			hasExit := false
			exitVal := 0
			if c.explicitHead.exit != nil {
				code := *c.explicitHead.exit
				exitCode = &code
				hasExit = true
				exitVal = code
			}
			var pres map[string]any
			if c.entry != nil && c.entry.shellMemo != nil &&
				c.entry.shellMemo.cmd == rawCmd &&
				c.entry.shellMemo.running == running &&
				c.entry.shellMemo.hasExit == hasExit &&
				c.entry.shellMemo.exitCode == exitVal &&
				c.entry.shellMemo.output == c.bashOutput {
				pres = c.entry.shellMemo.pres
			} else {
				pres = c.s.buildShellPresentationFn(rawCmd, running, exitCode, c.bashOutput)
				if c.entry != nil && pres != nil {
					c.entry.shellMemo = &ompShellMemo{
						cmd:      rawCmd,
						running:  running,
						hasExit:  hasExit,
						exitCode: exitVal,
						output:   c.bashOutput,
						pres:     pres,
					}
				}
			}
			if pres != nil {
				props := map[string]any{
					"role":         role,
					"presentation": pres,
					"lang":         "bash",
					"preview":      map[string]any{"lines": 6},
					"replayEpoch":  0,
					"liveOrigin":   !c.startedAt.IsZero(),
				}
				if workdir := c.ompBashDisplayWorkdir(); workdir != "" {
					props["cwd"] = workdir
				}
				return tern.Node{Kind: tern.KindShell, Props: props}, true
			}
		}
		return tern.Node{
			Kind: tern.KindCode,
			Props: map[string]any{
				"role":    role,
				"lang":    "bash",
				"wrap":    true,
				"preview": map[string]any{"lines": 6},
				"text":    displayCmd,
			},
		}, true

	case "omp.tool.bash.output":
		// upstream: tools/bash.ts:537, 1058, native-view.ts:289-291
		return tern.Node{
			Kind: tern.KindAnsi,
			Props: map[string]any{
				"role":    role,
				"follow":  true,
				"preview": map[string]any{"lines": structuredPreviewLines},
				"text":    ompViewLeafRawText(b, n, r),
			},
		}, true

	case "omp.tool.bash.command-tool":
		// upstream: tools/tool-commands.ts:232
		return tern.Node{
			Kind:     tern.KindCol,
			Props:    hiddenIf(map[string]any{"role": role, "gap": "sm"}, n.Rows == 0),
			Children: ompViewChildren(b, n, id, r, path),
		}, true

	case "omp.tool.bash.command-tool.head":
		// upstream: tools/tool-commands.ts:236
		props := map[string]any{"role": role}
		b.setRich(props, "text", "spans", ompViewLeafRawText(b, n, r))
		return tern.Node{Kind: tern.KindText, Props: props}, true

	case "omp.tool.bash.step":
		// upstream: tools/bash.ts:522-531
		if len(n.Children) > 0 {
			return tern.Node{
				Kind:     tern.KindRow,
				Props:    hiddenIf(map[string]any{"role": role, "gap": "sm", "align": "center"}, n.Rows == 0),
				Children: ompViewChildren(b, n, id, r, path),
			}, true
		}
		plain := ompViewLeafPlainText(b, n, r)
		return tern.Node{
			Kind:  tern.KindRow,
			Props: hiddenIf(map[string]any{"role": role, "gap": "sm", "align": "center"}, n.Rows == 0),
			Children: []tern.Element{
				tern.Node{Kind: tern.KindText, Props: map[string]any{"role": "omp.tool.bash.step.label", "wrap": "none", "truncate": "end", "text": plain}},
			},
		}, true

	case "omp.tool.bash.step.label":
		// upstream: tools/bash.ts:524
		return tern.Node{
			Kind: tern.KindText,
			Props: map[string]any{
				"role":     role,
				"wrap":     "none",
				"truncate": "end",
				"text":     ompViewLeafPlainText(b, n, r),
			},
		}, true

	case "omp.tool.bash.step.note":
		// upstream: tools/bash.ts:526
		return tern.Node{
			Kind: tern.KindText,
			Props: map[string]any{
				"role":     role,
				"wrap":     "none",
				"truncate": "end",
				"spans":    []any{map[string]any{"t": ompViewLeafPlainText(b, n, r), "s": "mono"}},
			},
		}, true

	case "omp.tool.bash.nooutput":
		// upstream: tools/bash.ts:535 — plain text (no muted span wrapper).
		return tern.Node{
			Kind: tern.KindText,
			Props: map[string]any{
				"role": role,
				"text": ompViewLeafPlainText(b, n, r),
			},
		}, true

	// ── Ask form card & answered rows (native/ask-card.ts:72-227, tools/ask.ts:170-209) ──
	case "omp.ask.card":
		return tern.Node{
			Kind:     tern.KindCol,
			Props:    hiddenIf(map[string]any{"role": role, "gap": "md", "transition": "layout"}, n.Rows == 0),
			Children: ompViewChildren(b, n, id, r, path),
		}, true

	case "omp.ask.card.head":
		return tern.Node{
			Kind:     tern.KindRow,
			Props:    hiddenIf(map[string]any{"role": role, "gap": "sm", "align": "baseline", "justify": "between"}, n.Rows == 0),
			Children: ompViewChildren(b, n, id, r, path),
		}, true

	case "omp.ask.card.tag":
		return tern.Node{
			Kind: tern.KindText,
			Props: map[string]any{
				"role":  role,
				"spans": []any{map[string]any{"t": ompViewLeafPlainText(b, n, r), "s": "accent"}},
			},
		}, true

	case "omp.ask.card.progress":
		return tern.Node{
			Kind: tern.KindText,
			Props: map[string]any{
				"role":  role,
				"spans": []any{map[string]any{"t": ompViewLeafPlainText(b, n, r), "s": "muted"}},
			},
		}, true

	case "omp.ask.card.body":
		if len(n.Children) > 0 {
			return tern.Node{
				Kind:     tern.KindCol,
				Props:    hiddenIf(map[string]any{"role": role, "gap": "lg"}, n.Rows == 0),
				Children: ompViewChildren(b, n, id, r, path),
			}, true
		}
		return tern.Node{
			Kind: tern.KindText,
			Props: map[string]any{
				"role":  role,
				"spans": []any{map[string]any{"t": ompViewLeafPlainText(b, n, r), "s": "muted"}},
			},
		}, true

	case "omp.ask.skip":
		// upstream: native/ask-card.ts:98
		return tern.Node{
			Kind: tern.KindText,
			Props: map[string]any{
				"role":  role,
				"wrap":  "word",
				"spans": []any{map[string]any{"t": ompViewLeafPlainText(b, n, r), "s": "warning"}},
			},
		}, true

	case "omp.ask.open":
		return tern.Node{
			Kind:     tern.KindCol,
			Props:    hiddenIf(map[string]any{"role": role, "gap": "sm"}, n.Rows == 0),
			Children: ompViewChildren(b, n, id, r, path),
		}, true

	case "omp.ask.fold":
		return tern.Node{
			Kind:     tern.KindCol,
			Props:    hiddenIf(map[string]any{"role": role, "gap": "xs"}, n.Rows == 0),
			Children: ompViewChildren(b, n, id, r, path),
		}, true

	case "omp.ask.fold.head":
		return tern.Node{
			Kind:     tern.KindRow,
			Props:    hiddenIf(map[string]any{"role": role, "gap": "sm", "align": "baseline"}, n.Rows == 0),
			Children: ompViewChildren(b, n, id, r, path),
		}, true

	case "omp.ask.fold.mark":
		plain := ompViewLeafPlainText(b, n, r)
		return tern.Node{
			Kind: tern.KindText,
			Props: map[string]any{
				"role":  role,
				"spans": []any{map[string]any{"t": plain, "s": ompAskMarkToken(plain)}},
			},
		}, true

	case "omp.ask.fold.question":
		return tern.Node{
			Kind: tern.KindText,
			Props: map[string]any{
				"role": role,
				"wrap": "word",
				"text": ompViewLeafPlainText(b, n, r),
			},
		}, true

	case "omp.ask.fold.answer":
		return tern.Node{
			Kind: tern.KindText,
			Props: map[string]any{
				"role":     role,
				"truncate": "end",
				"lines":    1,
				"spans":    []any{map[string]any{"t": ompViewLeafPlainText(b, n, r), "s": "muted"}},
			},
		}, true

	case "omp.ask.fold.well":
		return tern.Node{
			Kind: tern.KindText,
			Props: map[string]any{
				"role":     role,
				"truncate": "end",
				"lines":    1,
				"spans":    []any{map[string]any{"t": ompViewLeafPlainText(b, n, r), "s": "dim"}},
			},
		}, true

	case "omp.ask.fold.note":
		// upstream: native/ask-card.ts:225-227 — raw note payload.
		return tern.Node{
			Kind: tern.KindText,
			Props: map[string]any{
				"role":     role,
				"truncate": "end",
				"lines":    1,
				"spans":    []any{map[string]any{"t": "✎ ", "s": "dim"}, map[string]any{"t": ompViewLeafPlainText(b, n, r), "s": "em"}},
			},
		}, true

	case "omp.ask.pills":
		// upstream: native/ask-card.ts:221
		return tern.Node{
			Kind:     tern.KindRow,
			Props:    hiddenIf(map[string]any{"role": role, "gap": "xs", "align": "center", "wrap": true}, n.Rows == 0),
			Children: ompViewChildren(b, n, id, r, path),
		}, true

	case "omp.ask.pill", "omp.ask.pill.more":
		// PiG's frontend contract has no pending-dialog form callback (gap 2),
		// so pills draw as static text rather than dead buttons (fixes B5).
		return tern.Node{
			Kind: tern.KindText,
			Props: map[string]any{
				"role":  role,
				"spans": []any{map[string]any{"t": ompViewLeafPlainText(b, n, r), "s": "muted"}},
			},
		}, true

	case "omp.ask.error":
		return tern.Node{
			Kind: tern.KindText,
			Props: map[string]any{
				"role":  role,
				"spans": []any{map[string]any{"t": ompViewLeafPlainText(b, n, r), "s": "error"}},
			},
		}, true

	case "omp.tool.answer":
		// upstream: tools/ask.ts:171-176 — raw answer text with native check icon.
		return tern.Node{
			Kind: tern.KindRow,
			Props: map[string]any{
				"role":  role,
				"gap":   "sm",
				"align": "baseline",
			},
			Children: []tern.Element{
				tern.Node{Kind: tern.KindIcon, Props: map[string]any{"name": "check", "tone": "success"}},
				tern.Node{Kind: tern.KindText, Props: map[string]any{"wrap": "word", "spans": []any{map[string]any{"t": ompViewLeafPlainText(b, n, r), "s": "toolOutput"}}}},
			},
		}, true

	case "omp.tool.answer.off":
		// upstream: tools/ask.ts:177
		return tern.Node{
			Kind: tern.KindText,
			Props: map[string]any{
				"role":  role,
				"spans": []any{map[string]any{"t": ompViewLeafPlainText(b, n, r), "s": "dim"}},
			},
		}, true

	case "omp.tool.answer.note":
		// upstream: tools/ask.ts:178-183 — raw note payload.
		return tern.Node{
			Kind: tern.KindText,
			Props: map[string]any{
				"role":  role,
				"wrap":  "word",
				"spans": []any{map[string]any{"t": "✎ ", "s": "dim"}, map[string]any{"t": ompViewLeafPlainText(b, n, r), "s": "em"}},
			},
		}, true
	// ── Eval cells & status tree (tools/eval.ts:735-934) ──
	case "omp.tool.eval.input":
		// upstream: tools/eval.ts:922-927 — wrap:false, lang python/javascript, preview:{lines:6}.
		lang := ompEvalHighlightLang(c.tool.Arguments)
		props := map[string]any{
			"role":    role,
			"wrap":    false,
			"preview": map[string]any{"lines": 6},
			"text":    ompViewLeafCodeText(b, n, r),
		}
		if lang != "" {
			props["lang"] = lang
		}
		return tern.Node{Kind: tern.KindCode, Props: props}, true

	case "omp.tool.eval.output":
		// upstream: tools/eval.ts:842-846 — follow:true, preview:{lines:10}.
		return tern.Node{
			Kind: tern.KindAnsi,
			Props: map[string]any{
				"role":    role,
				"follow":  true,
				"preview": map[string]any{"lines": structuredPreviewLines},
				"text":    ompViewLeafRawText(b, n, r),
			},
		}, true

	case "omp.tool.eval.markdown":
		// internal/ansi VIEW-IDS.md:64, upstream tools/eval.ts:853
		return tern.Node{
			Kind:  tern.KindMd,
			Props: map[string]any{"role": role, "text": ompViewLeafPlainText(b, n, r)},
		}, true

	case "omp.tool.eval.status":
		// upstream: tools/eval.ts:735
		props := map[string]any{"role": role, "truncate": "end"}
		b.setRich(props, "text", "spans", ompViewLeafRawText(b, n, r))
		return tern.Node{Kind: tern.KindText, Props: props}, true

	case "omp.tool.eval.status.event":
		// upstream: tools/eval.ts:740
		return tern.Node{
			Kind:     tern.KindCol,
			Props:    hiddenIf(map[string]any{"role": role, "gap": "none"}, n.Rows == 0),
			Children: ompViewChildren(b, n, id, r, path),
		}, true

	case "omp.tool.eval.status.preview":
		// upstream: tools/eval.ts:743-748 — kind `code`, wrap:false, preview:{lines:3}.
		return tern.Node{
			Kind: tern.KindCode,
			Props: map[string]any{
				"role":    role,
				"wrap":    false,
				"preview": map[string]any{"lines": 3},
				"text":    ompViewLeafCodeText(b, n, r),
			},
		}, true

	// ── Task / Agent rows (tools/task.ts:1812-2041, VIEW-IDS.md:262-283) ──
	case "omp.task.assignment":
		return tern.Node{
			Kind:  tern.KindMd,
			Props: map[string]any{"role": role, "text": ompViewLeafPlainText(b, n, r)},
		}, true

	case "omp.task.output":
		if n.Kind == fe.ViewKindLines {
			return tern.Node{
				Kind:  tern.KindAnsi,
				Props: map[string]any{"role": role, "text": ompViewLeafRawText(b, n, r)},
			}, true
		}
		return tern.Node{
			Kind:  tern.KindMd,
			Props: map[string]any{"role": role, "text": ompViewLeafPlainText(b, n, r)},
		}, true
	case "omp.task.agent":
		// upstream: packages/tui/src/tools/task.ts:1890-2041 — build a native
		// Tern `agent` node from structured child nodes emitted by native_structured.go.
		return c.mapTaskAgentNode(b, n, id, r, path), true

	case "omp.task.agent.row", "omp.task.meta":
		props := map[string]any{"role": role, "wrap": "word"}
		b.setRich(props, "text", "spans", ompViewLeafRawText(b, n, r))
		return tern.Node{Kind: tern.KindText, Props: props}, true

	// ── Todo (tools/todo.ts:550-595) ──
	case "omp.todo.checklist":
		// upstream: packages/tui/src/tools/todo.ts:565-594 — emit ONE native
		// Tern `checklist` node (`mode:"full"`, `phases:[...]`), set head target
		// to `"<closed>/<total>"`, `collapsed:false`, `preview:"none"`.
		return c.mapTodoChecklistNode(b, n, r), true

	case "omp.todo.phase":
		return tern.Node{
			Kind:     tern.KindCol,
			Props:    hiddenIf(map[string]any{"role": role, "gap": "none"}, n.Rows == 0),
			Children: ompViewChildren(b, n, id, r, path),
		}, true

	case "omp.todo.phase.head", "omp.todo.item", "omp.todo.item.status", "omp.todo.more":
		props := map[string]any{"role": role, "wrap": "word"}
		b.setRich(props, "text", "spans", ompViewLeafRawText(b, n, r))
		return tern.Node{Kind: tern.KindText, Props: props}, true
	// ── Wait (tools/wait.ts:838-863, VIEW-IDS.md:307-318) ──
	case "omp.wait.more":
		// upstream: tools/wait.ts:842
		return tern.Node{
			Kind: tern.KindText,
			Props: map[string]any{
				"role":  role,
				"spans": []any{map[string]any{"t": ompViewLeafPlainText(b, n, r), "s": "dim"}},
			},
		}, true

	case "omp.wait.agent":
		// upstream: tools/wait.ts:849-861
		c.waitAgents++
		if len(n.Children) > 0 {
			return tern.Node{
				Kind:     tern.KindRow,
				Props:    hiddenIf(map[string]any{"role": "omp.wait.agent", "gap": "sm"}, n.Rows == 0),
				Children: ompViewChildren(b, n, id, r, path),
			}, true
		}
		props := map[string]any{"role": "omp.wait.agent", "gap": "sm"}
		textProps := map[string]any{"truncate": "end", "grow": 1}
		b.setRich(textProps, "text", "spans", ompViewLeafRawText(b, n, r))
		return tern.Node{
			Kind:     tern.KindRow,
			Props:    props,
			Children: []tern.Element{tern.Node{Kind: tern.KindText, Props: textProps}},
		}, true
	// ── Job tiles (tools/wait.ts:838-863, tools/job-widget.ts:389-608, VIEW-IDS.md:293-310) ──
	case "omp.job.tiles":
		// upstream: tools/job-widget.ts:389-394 — wrapping row of equal tiles.
		return tern.Node{
			Kind:     tern.KindRow,
			Props:    hiddenIf(map[string]any{"role": role, "gap": "none", "wrap": true}, n.Rows == 0),
			Children: ompViewChildren(b, n, id, r, path),
		}, true

	case "omp.job.tile":
		// upstream: tools/job-widget.ts:455-467 — col gap xs, tone from status glyph.
		tileTone := ompJobTileTone(b, n, r)
		switch tileTone {
		case "success":
			c.waitCompleted++
		case "error":
			c.waitFailed++
		case "warning":
			c.waitCancelled++
		default:
			c.waitRunning++
		}
		props := map[string]any{"role": role, "gap": "xs"}
		if tileTone != "" {
			props["tone"] = tileTone
		}
		return tern.Node{
			Kind:     tern.KindCol,
			Props:    hiddenIf(props, n.Rows == 0),
			Children: ompViewChildren(b, n, id, r, path),
		}, true

	case "omp.job.tile.head":
		// upstream: tools/job-widget.ts:418-453 — row gap xs align center.
		prevStopped := c.localStopped
		prevTileHead := c.inJobTileHead
		c.localStopped = ompTileHeadSettled(b, n, r)
		c.inJobTileHead = true
		children := ompViewChildren(b, n, id, r, path)
		c.localStopped = prevStopped
		c.inJobTileHead = prevTileHead
		return tern.Node{
			Kind:     tern.KindRow,
			Props:    hiddenIf(map[string]any{"role": role, "gap": "xs", "align": "center"}, n.Rows == 0),
			Children: children,
		}, true
	case "omp.job.tile.dot":
		// upstream: tools/job-widget.ts:420-423
		dot := ompViewLeafPlainText(b, n, r)
		dotTone := ompJobDotTone(dot)
		spanObj := map[string]any{"t": dot}
		if dotTone != "" {
			spanObj["s"] = dotTone
		}
		return tern.Node{
			Kind:  tern.KindText,
			Props: map[string]any{"role": role, "wrap": "none", "spans": []any{spanObj}},
		}, true

	case "omp.job.tile.title":
		// upstream: tools/job-widget.ts:424-429
		return tern.Node{
			Kind:  tern.KindText,
			Props: map[string]any{"role": role, "wrap": "none", "truncate": "end", "grow": 1, "spans": []any{map[string]any{"t": ompViewLeafPlainText(b, n, r)}}},
		}, true
	case "omp.job.tile.id":
		// upstream: tools/job-widget.ts:432-435
		return tern.Node{
			Kind:  tern.KindText,
			Props: map[string]any{"role": role, "wrap": "none", "spans": []any{map[string]any{"t": ompViewLeafPlainText(b, n, r), "s": "dim"}}},
		}, true

	case "omp.job.tile.ring":
		// upstream: tools/job-widget.ts:440-448 — meter style="ring" size="sm".
		val := 0.0
		if n.Progress != nil && n.Progress.Max > 0 {
			val = min(max(n.Progress.Value/n.Progress.Max, 0), 1)
		}
		pct := int(val*100 + 0.5)
		return tern.Node{
			Kind: tern.KindMeter,
			Props: map[string]any{
				"role":  role,
				"style": "ring",
				"size":  "sm",
				"value": val,
				"label": strconv.Itoa(pct) + "%",
			},
		}, true

	case "omp.job.tile.figure":
		// upstream: tools/job-widget.ts:413-417
		return tern.Node{
			Kind:  tern.KindText,
			Props: map[string]any{"role": role, "wrap": "none", "truncate": "end", "text": ompViewLeafPlainText(b, n, r)},
		}, true

	case "omp.job.tile.chart":
		// upstream: tools/job-widget.ts:498-502 — native chart when spark series present,
		// else plain text summary fallback.
		if chartNode, ok := ompJobTileChartNode(b, n, r, role); ok {
			return chartNode, true
		}
		return tern.Node{
			Kind:  tern.KindText,
			Props: map[string]any{"role": role, "wrap": "none", "truncate": "end", "text": ompViewLeafPlainText(b, n, r)},
		}, true

	case "omp.job.tile.meter":
		// upstream: tools/job-widget.ts:554-562 — meter size="sm" grow=1.
		val := 0.0
		if n.Progress != nil && n.Progress.Max > 0 {
			val = min(max(n.Progress.Value/n.Progress.Max, 0), 1)
		}
		return tern.Node{
			Kind: tern.KindMeter,
			Props: map[string]any{
				"role":  role,
				"value": val,
				"size":  "sm",
				"grow":  1,
			},
		}, true

	case "omp.job.tile.bars":
		// upstream: tools/job-widget.ts:534 — col gap xs.
		return tern.Node{
			Kind:     tern.KindCol,
			Props:    hiddenIf(map[string]any{"role": role, "gap": "xs"}, n.Rows == 0),
			Children: ompViewChildren(b, n, id, r, path),
		}, true

	case "omp.job.tile.bar":
		// upstream: tools/job-widget.ts:521 — row gap sm align center.
		return tern.Node{
			Kind:     tern.KindRow,
			Props:    hiddenIf(map[string]any{"role": role, "gap": "sm", "align": "center"}, n.Rows == 0),
			Children: ompViewChildren(b, n, id, r, path),
		}, true

	case "omp.job.tile.key":
		// upstream: tools/job-widget.ts:510-514
		return tern.Node{
			Kind:  tern.KindText,
			Props: map[string]any{"role": role, "wrap": "none", "truncate": "end", "spans": []any{map[string]any{"t": ompViewLeafPlainText(b, n, r), "s": "dim"}}},
		}, true

	case "omp.job.tile.num":
		// upstream: tools/job-widget.ts:516-519
		return tern.Node{
			Kind:  tern.KindText,
			Props: map[string]any{"role": role, "wrap": "none", "text": ompViewLeafPlainText(b, n, r)},
		}, true

	case "omp.job.tile.more":
		// upstream: tools/job-widget.ts:529-532
		return tern.Node{
			Kind:  tern.KindText,
			Props: map[string]any{"role": role, "wrap": "none", "spans": []any{map[string]any{"t": ompViewLeafPlainText(b, n, r), "s": "dim"}}},
		}, true

	case "omp.job.tile.grid":
		// upstream: tools/job-widget.ts:547
		return tern.Node{
			Kind:  tern.KindText,
			Props: map[string]any{"role": role, "wrap": "word", "text": ompViewLeafPlainText(b, n, r)},
		}, true

	case "omp.job.tile.lead":
		// upstream: tools/job-widget.ts:604
		return tern.Node{
			Kind:  tern.KindText,
			Props: map[string]any{"role": role, "wrap": "word", "text": ompViewLeafPlainText(b, n, r)},
		}, true

	case "omp.job.tile.stage", "omp.job.tile.stats", "omp.job.tile.note":
		// upstream: tools/job-widget.ts:579-605
		return tern.Node{
			Kind:  tern.KindText,
			Props: map[string]any{"role": role, "wrap": "none", "truncate": "end", "spans": []any{map[string]any{"t": ompViewLeafPlainText(b, n, r), "s": "dim"}}},
		}, true
	// ── Proc://, Cfg://, IRC internal URL cards (tools/proc-render.ts:347-525, tools/cfg-render.ts:98-142) ──
	case "omp.tool.proc.table":
		// upstream: tools/proc-render.ts:524 — list of `item` nodes.
		return ompViewItemList(b, n, id, r, path, role, "No background jobs or services."), true

	case "omp.tool.proc.cancelled":
		// upstream: tools/proc-render.ts:427-447 — list of `item` nodes.
		return ompViewItemList(b, n, id, r, path, role, ""), true

	case "omp.tool.irc.receipts":
		// upstream: tools/proc-render.ts:386-402 — list of `item` nodes.
		return ompViewItemList(b, n, id, r, path, role, ""), true

	case "omp.tool.proc.log":
		// upstream: tools/proc-render.ts:475, 481
		return tern.Node{
			Kind: tern.KindAnsi,
			Props: map[string]any{
				"role": role,
				"text": ompViewLeafRawText(b, n, r),
			},
		}, true

	case "omp.tool.proc.preview":
		// upstream: tools/proc-render.ts:347-350, 484 — toolOutput on read, muted on write.
		token := "muted"
		if c.tool.Name == "read" {
			token = "toolOutput"
		}
		return tern.Node{
			Kind: tern.KindText,
			Props: map[string]any{
				"role":  role,
				"wrap":  "word",
				"spans": []any{map[string]any{"t": ompViewLeafPlainText(b, n, r), "s": token}},
			},
		}, true

	case "omp.tool.cfg.listing":
		// upstream: tools/cfg-render.ts:101 — code {lang:"yaml"}.
		return tern.Node{
			Kind: tern.KindCode,
			Props: map[string]any{
				"role": role,
				"lang": "yaml",
				"text": ompViewLeafCodeText(b, n, r),
			},
		}, true

	case "omp.tool.cfg.change":
		// upstream: tools/cfg-render.ts:122-129
		props := map[string]any{"role": role, "wrap": "word"}
		b.setRich(props, "text", "spans", ompViewLeafRawText(b, n, r))
		return tern.Node{Kind: tern.KindText, Props: props}, true

	// ── Web search (tools/web-search.ts:308-391, VIEW-IDS.md:236-249) ──
	case "omp.tool.web_search.answer":
		return tern.Node{
			Kind:  tern.KindMd,
			Props: map[string]any{"role": role, "text": ompViewLeafPlainText(b, n, r)},
		}, true

	case "omp.tool.sources":
		// upstream: tools/web-search.ts:386-389
		return tern.Node{
			Kind:     tern.KindCol,
			Props:    hiddenIf(map[string]any{"role": role}, n.Rows == 0),
			Children: ompViewChildren(b, n, id, r, path),
		}, true

	case "omp.tool.source":
		// upstream: tools/web-search.ts:319-331
		return tern.Node{
			Kind:     tern.KindRow,
			Props:    hiddenIf(map[string]any{"role": role, "gap": "sm", "align": "baseline"}, n.Rows == 0),
			Children: ompViewChildren(b, n, id, r, path),
		}, true

	case "omp.tool.source.head":
		props := map[string]any{"role": role, "lines": 2, "truncate": "end"}
		b.setRich(props, "text", "spans", ompViewLeafRawText(b, n, r))
		return tern.Node{Kind: tern.KindText, Props: props}, true

	case "omp.tool.excerpt":
		// upstream: tools/web-search.ts:327
		return tern.Node{
			Kind: tern.KindText,
			Props: map[string]any{
				"role":  role,
				"wrap":  "word",
				"lines": 3,
				"spans": []any{map[string]any{"t": ompViewLeafPlainText(b, n, r), "s": "muted"}},
			},
		}, true

	// ── Files, search, glob, find (tools/native-view.ts:73-105, tools/glob.ts:106-129, tools/find.ts:249) ──
	case "omp.tool.files":
		// upstream: tools/glob.ts:106-129 — col gap none up to 6 files; row gap xs wrap true above 6.
		// AnsiNative emits VStack of omp.tool.file (<=6) or HStack of omp.tool.chip (>6);
		// recurse through ompViewChildren so omp.tool.chip / omp.tool.stats keep their roles.
		if n.Kind == fe.ViewKindHStack || len(n.Children) > 6 {
			hasChip := false
			for i := range n.Children {
				if cr, ok := ompViewRole(n.Children[i].ID); ok && cr == "omp.tool.chip" {
					hasChip = true
					break
				}
			}
			if hasChip {
				return tern.Node{
					Kind:     tern.KindRow,
					Props:    hiddenIf(map[string]any{"role": role, "gap": "xs", "wrap": true}, n.Rows == 0),
					Children: ompViewChildren(b, n, id, r, path),
				}, true
			}
			// Legacy VStack of >6 omp.tool.file children: convert to chip row.
			chips := make([]tern.Element, 0, len(n.Children))
			row := r.row
			for i := range n.Children {
				child := &n.Children[i]
				childR := region{row, r.col, child.Rows, r.width}
				row += child.Rows + n.Gap*boolInt(n.Kind == fe.ViewKindVStack)
				file := ompViewLeafPlainText(b, child, childR)
				iconName := "file"
				isDir := strings.HasSuffix(file, "/")
				if isDir {
					iconName = "folder"
				}
				name := ompBaseName(file)
				spanObj := map[string]any{"t": name, "s": "strong"}
				if href := c.ompFileItemHref(file); href != "" {
					spanObj["href"] = href
				}
				chips = append(chips, tern.Node{
					Kind: tern.KindRow,
					Props: map[string]any{
						"role":  "omp.tool.chip",
						"gap":   "xs",
						"align": "center",
						"title": file,
					},
					Children: []tern.Element{
						tern.Node{Kind: tern.KindIcon, Props: map[string]any{"name": iconName}},
						tern.Node{Kind: tern.KindText, Props: map[string]any{"wrap": "none", "spans": []any{spanObj}}},
					},
				})
			}
			return tern.Node{
				Kind:     tern.KindRow,
				Props:    hiddenIf(map[string]any{"role": role, "gap": "xs", "wrap": true}, n.Rows == 0),
				Children: chips,
			}, true
		}
		return tern.Node{
			Kind:     tern.KindCol,
			Props:    hiddenIf(map[string]any{"role": role, "gap": "none"}, n.Rows == 0),
			Children: ompViewChildren(b, n, id, r, path),
		}, true
	case "omp.tool.chip":
		// upstream: tools/glob.ts:118-124
		if c.tool.Name == "glob" {
			c.globFiles++
		}
		if len(n.Children) > 0 {
			return tern.Node{
				Kind:     tern.KindRow,
				Props:    hiddenIf(map[string]any{"role": role, "gap": "xs", "align": "center"}, n.Rows == 0),
				Children: ompViewChildren(b, n, id, r, path),
			}, true
		}
		file := ompViewLeafPlainText(b, n, r)
		iconName := "file"
		if strings.HasSuffix(file, "/") {
			iconName = "folder"
		}
		spanObj := map[string]any{"t": ompBaseName(file), "s": "strong"}
		if href := c.ompFileItemHref(file); href != "" {
			spanObj["href"] = href
		}
		return tern.Node{
			Kind:  tern.KindRow,
			Props: map[string]any{"role": role, "gap": "xs", "align": "center", "title": file},
			Children: []tern.Element{
				tern.Node{Kind: tern.KindIcon, Props: map[string]any{"name": iconName}},
				tern.Node{Kind: tern.KindText, Props: map[string]any{"wrap": "none", "spans": []any{spanObj}}},
			},
		}, true

	case "omp.tool.file":
		// upstream: tools/native-view.ts:73-105 — raw file path, no terminal tree prefix stripping.
		if c.tool.Name == "glob" {
			c.globFiles++
		}
		if len(n.Children) > 0 {
			return tern.Node{
				Kind:     tern.KindRow,
				Props:    hiddenIf(map[string]any{"role": role}, n.Rows == 0),
				Children: ompViewChildren(b, n, id, r, path),
			}, true
		}
		file := ompViewLeafPlainText(b, n, r)
		iconName := "file"
		if strings.HasSuffix(file, "/") {
			iconName = "folder"
		}
		return tern.Node{
			Kind:  tern.KindRow,
			Props: map[string]any{"role": role},
			Children: []tern.Element{
				tern.Node{Kind: tern.KindIcon, Props: map[string]any{"name": iconName}},
				tern.Node{Kind: tern.KindText, Props: map[string]any{"lines": 1, "truncate": "middle", "shrink": 1, "title": file, "spans": ompFileRowSpans(file, c.ompFileItemHref(file))}},
			},
		}, true
	case "omp.tool.search.file":
		// upstream: tools/grouped-file-output.ts:743 — col[omp.tool.search.file](row[omp.tool.file](..., badge), code)
		c.grepFiles++
		prevFile := c.curSearchFile
		prevMeta := c.curSearchMeta
		c.curSearchFile = ""
		c.curSearchMeta = ""
		var filePath string
		var metaSpec string
		var lineCount int
		scanRow := r.row
		for i := range n.Children {
			ch := &n.Children[i]
			chR := region{scanRow, r.col, ch.Rows, r.width}
			scanRow += ch.Rows + n.Gap*boolInt(n.Kind == fe.ViewKindVStack)
			cr, _ := ompViewRole(ch.ID)
			switch cr {
			case "omp.tool.file":
				filePath = ompViewLeafPlainText(b, ch, chR)
			case "omp.tool.search.meta":
				metaSpec = ompViewLeafPlainText(b, ch, chR)
			case "omp.tool.search.lines":
				txt := ompViewLeafCodeText(b, ch, chR)
				if txt != "" {
					lineCount += strings.Count(txt, "\n") + 1
				}
			}
		}
		// When omp.tool.search.meta carries match markers (`*N`), count only marked lines
		// for the per-file badge and head meta (upstream: grouped-file-output.ts:735-740).
		matchCount := lineCount
		if _, marks, ok := parseOmpSearchMeta(metaSpec); ok {
			matchCount = len(marks)
		}
		c.curSearchFile = filePath
		c.curSearchMeta = metaSpec
		c.grepMatches += matchCount
		// Render all non-meta children in order so `omp.tool.search.meta` stays hidden
		// metadata and never leaks as a visible text node under Tern.
		children := make([]tern.Element, 0, len(n.Children))
		row := r.row
		for i := range n.Children {
			ch := &n.Children[i]
			chR := region{row, r.col, ch.Rows, r.width}
			row += ch.Rows + n.Gap*boolInt(n.Kind == fe.ViewKindVStack)
			if cr, ok := ompViewRole(ch.ID); ok && cr == "omp.tool.search.meta" {
				continue
			}
			if el := b.stackChild(ch, id+"."+strconv.Itoa(i), chR, n, false, childPath(path, i)); el.Kind != "" {
				children = append(children, el)
			}
		}
		if matchCount > 0 && len(children) > 0 {
			if fr, ok := children[0].(tern.Node); ok && fr.Kind == tern.KindRow && fr.Props["role"] == "omp.tool.file" {
				title := fmt.Sprintf("%d matches", matchCount)
				if matchCount == 1 {
					title = "1 match"
				}
				fr.Children = append(fr.Children, tern.Node{
					Kind:  tern.KindBadge,
					Props: map[string]any{"text": strconv.Itoa(matchCount), "title": title},
				})
				children[0] = fr
			}
		}
		c.curSearchFile = prevFile
		c.curSearchMeta = prevMeta
		return tern.Node{
			Kind:     tern.KindCol,
			Props:    hiddenIf(map[string]any{"role": role, "gap": "xs"}, n.Rows == 0),
			Children: children,
		}, true

	case "omp.tool.find.hit":
		// upstream: tools/find.ts:249
		return tern.Node{
			Kind:     tern.KindCol,
			Props:    hiddenIf(map[string]any{"role": role, "gap": "xs"}, n.Rows == 0),
			Children: ompViewChildren(b, n, id, r, path),
		}, true

	case "omp.tool.search.lines":
		// upstream: tools/grouped-file-output.ts:670-697, 724-728 — grep/ast_grep emits
		// `code` (without a custom role) carrying `numbers:true`, `path`, `start`/`nums`,
		// and `marks:[{line, tone:"accent"}]`.
		codeText := ompViewLeafCodeText(b, n, r)
		props := map[string]any{
			"numbers": true,
			"text":    codeText,
		}
		if c.curSearchFile != "" {
			props["path"] = c.curSearchFile
		}
		if nums, marks, ok := parseOmpSearchMeta(c.curSearchMeta); ok && len(nums) > 0 {
			contiguous := true
			for i := 1; i < len(nums); i++ {
				if nums[i] != nums[i-1]+1 {
					contiguous = false
					break
				}
			}
			if contiguous {
				props["start"] = nums[0]
			} else {
				rawNums := make([]any, len(nums))
				for i, num := range nums {
					rawNums[i] = num
				}
				props["nums"] = rawNums
			}
			if len(marks) > 0 {
				rawMarks := make([]any, len(marks))
				for i, lineNum := range marks {
					rawMarks[i] = map[string]any{"line": lineNum, "tone": "accent"}
				}
				props["marks"] = rawMarks
			}
		}
		return tern.Node{Kind: tern.KindCode, Props: props}, true
	case "omp.tool.find.range":
		// upstream: tools/find.ts — hit range snippet row.
		props := map[string]any{"role": role, "wrap": "word"}
		b.setRich(props, "text", "spans", ompViewLeafRawText(b, n, r))
		return tern.Node{Kind: tern.KindText, Props: props}, true

	case "omp.tool.lsp.body":
		// upstream: tools/lsp.ts:1100, 1124 — bare `md` or `ansi` (no custom role).
		if n.Kind == fe.ViewKindMarkdown {
			return tern.Node{
				Kind:  tern.KindMd,
				Props: map[string]any{"text": ompViewLeafPlainText(b, n, r)},
			}, true
		}
		return tern.Node{
			Kind:  tern.KindAnsi,
			Props: map[string]any{"text": ompViewLeafRawText(b, n, r)},
		}, true

	case "omp.tool.github.body", "omp.tool.yield.data":
		// VIEW-IDS.md:336,351 — Markdown when emitted as ViewKindMarkdown, else Ansi.
		if n.Kind == fe.ViewKindMarkdown {
			return tern.Node{
				Kind:  tern.KindMd,
				Props: map[string]any{"role": role, "text": ompViewLeafPlainText(b, n, r)},
			}, true
		}
		return tern.Node{
			Kind:  tern.KindAnsi,
			Props: map[string]any{"role": role, "text": ompViewLeafRawText(b, n, r)},
		}, true

	case "omp.tool.debug.snapshot":
		// upstream: tools/debug.ts:134-174, VIEW-IDS.md:339 — KV pairs from Lines.List.
		if n.List != nil && len(n.List.Items) > 0 {
			pairs := make([]any, 0, len(n.List.Items))
			for _, item := range n.List.Items {
				val := item.Detail
				if val == "" && len(item.Columns) > 0 {
					val = strings.Join(item.Columns, " · ")
				}
				pairs = append(pairs, map[string]any{"k": item.Label, "v": val})
			}
			return tern.Node{
				Kind:  tern.KindKV,
				Props: map[string]any{"role": role, "items": pairs},
			}, true
		}
		return tern.Node{
			Kind:  tern.KindAnsi,
			Props: map[string]any{"role": role, "text": ompViewLeafRawText(b, n, r)},
		}, true

	case "omp.tool.fetch.content", "omp.tool.github.log", "omp.tool.debug.output", "omp.tool.args", "omp.tool.output":
		// Special case: ast_edit preview fallback (tools/ast-edit.ts:396) emits
		// bare `noteText(kept.join("\n"))` (`text{wrap:"word", spans:[{t, s:"muted"}]}`),
		// and carries replacement lines (`-N│...` / `+N│...`) from which we count replacements.
		if role == "omp.tool.output" && c.tool.Name == "ast_edit" {
			raw := ompViewLeafRawText(b, n, r)
			c.astEditChanges += ompCountAstEditReplacements(raw)
			if c.astEditFiles == 0 {
				c.astEditFiles = 1
			}
			if apply, ok := c.tool.Arguments["apply"].(bool); !ok || !apply {
				c.astEditProposed = true
			}
			return tern.Node{
				Kind: tern.KindText,
				Props: map[string]any{
					"wrap":  "word",
					"spans": []any{map[string]any{"t": raw, "s": "muted"}},
				},
			}, true
		}
		// Special case: bash nested tool-command body (tools/tool-commands.ts:223-245)
		// emits bare muted text without a custom `omp.tool.output` role.
		if role == "omp.tool.output" && c.tool.Name == "bash" {
			raw := ompViewLeafPlainText(b, n, r)
			return tern.Node{
				Kind: tern.KindText,
				Props: map[string]any{
					"wrap":  "word",
					"spans": []any{map[string]any{"t": raw, "s": "muted"}},
				},
			}, true
		}
		if n.Kind == fe.ViewKindText || n.Kind == fe.ViewKindTruncatedText {
			return tern.Node{
				Kind:  tern.KindText,
				Props: map[string]any{"role": role, "wrap": "word", "text": ompViewLeafPlainText(b, n, r)},
			}, true
		}
		return tern.Node{
			Kind:  tern.KindAnsi,
			Props: map[string]any{"role": role, "text": ompViewLeafRawText(b, n, r)},
		}, true
	case "omp.tool.github.watch":
		return tern.Node{
			Kind:     tern.KindCol,
			Props:    hiddenIf(map[string]any{"role": role, "gap": "xs"}, n.Rows == 0),
			Children: ompViewChildren(b, n, id, r, path),
		}, true

	case "omp.tool.github.job", "omp.memory.item":
		props := map[string]any{"role": role, "wrap": "word"}
		b.setRich(props, "text", "spans", ompViewLeafRawText(b, n, r))
		return tern.Node{Kind: tern.KindText, Props: props}, true

	case "omp.tool.edit.diff", "omp.tool.ast_edit.diff":
		// upstream: tools/edit.ts:1442, tools/ast-edit.ts:381 — use n.Diff.Text/Path when ViewKindDiff,
		// emitting structured `hunks` when unified diff parses cleanly (chrome/diff.ts:121-162).
		diffText := ""
		diffPath := ""
		if n.Diff != nil {
			diffText = n.Diff.Text
			diffPath = n.Diff.Path
		}
		if diffText == "" {
			diffText = ompViewLeafCodeText(b, n, r)
		}
		if diffPath != "" && c.s != nil {
			diffPath = c.s.ompEditDisplayPath(diffPath)
		}
		props := ompDiffProps(diffText, diffPath)
		props["role"] = role
		return tern.Node{Kind: tern.KindDiff, Props: props}, true
	case "omp.tool.eval.table":
		// upstream: tools/eval.ts:807-825 — native table when List.Items has header + >=1 rows.
		if tableNode, ok := ompEvalTableNode(n, role); ok {
			return tableNode, true
		}
		return ompViewItemList(b, n, id, r, path, role, ""), true

	// ── Edit, Write, Diagnostics, Read, Memory, Think ──
	case "omp.tool.edit.file", "omp.tool.ast_edit.file":
		// upstream: tools/native-view.ts:50-60 — head from first child, body from remaining (fixes B3).
		head, body := ompViewSectionHeadAndBody(b, n, id, r, path)
		props := map[string]any{"role": role}
		if head != "" {
			props["head"] = []any{map[string]any{"t": head, "s": "path"}}
		}
		return tern.Node{Kind: tern.KindSection, Props: hiddenIf(props, n.Rows == 0), Children: body}, true

	case "omp.tool.edit.change", "omp.tool.write.figure", "omp.memory.reflect":
		// upstream: tools/edit.ts:274, tools/write.ts:145, tools/memory.ts:292
		return tern.Node{
			Kind:  tern.KindMd,
			Props: map[string]any{"role": role, "text": ompViewLeafPlainText(b, n, r)},
		}, true

	case "omp.think":
		// upstream: tools/think.ts:39
		return tern.Node{
			Kind:  tern.KindMd,
			Props: map[string]any{"role": role, "tone": "muted", "text": ompViewLeafPlainText(b, n, r)},
		}, true

	case "omp.tool.diagnostics":
		// upstream: tools/native-view.ts:380-389 — tone is "error" if any error row is present, else "warning".
		head, body := ompViewSectionHeadAndBody(b, n, id, r, path)
		if head == "" {
			head = "Diagnostics"
		}
		tone := "warning"
		if ompDiagnosticsHasError(b, n, r) {
			tone = "error"
		}
		return tern.Node{
			Kind: tern.KindSection,
			Props: hiddenIf(map[string]any{
				"role": role,
				"head": []any{map[string]any{"t": head}},
				"tone": tone,
			}, n.Rows == 0),
			Children: body,
		}, true
	case "omp.tool.diagnostic":
		// upstream: tools/native-view.ts:309-344, tools/lsp.ts:926-935 — per-severity icon and tone.
		if len(n.Children) > 0 {
			return tern.Node{
				Kind:     tern.KindRow,
				Props:    hiddenIf(map[string]any{"role": role}, n.Rows == 0),
				Children: ompViewChildren(b, n, id, r, path),
			}, true
		}
		plain := ompViewLeafPlainText(b, n, r)
		sev, iconName, iconTone := ompDiagnosticSeverity(plain)
		iconProps := map[string]any{"name": iconName, "tone": iconTone}
		if sev != "" {
			iconProps["aria"] = sev
		}
		return tern.Node{
			Kind:  tern.KindRow,
			Props: map[string]any{"role": role},
			Children: []tern.Element{
				tern.Node{Kind: tern.KindIcon, Props: iconProps},
				tern.Node{Kind: tern.KindText, Props: map[string]any{"wrap": "word", "grow": 1, "text": plain}},
			},
		}, true
	case "omp.tool.read.code":
		// upstream: tools/read.ts:864, chat/read-tool-group.ts:939 — bare `code` node
		// (no custom role), carrying `numbers:true`, `wrap:true`, and `path` when known.
		props := map[string]any{
			"numbers": true,
			"wrap":    true,
			"text":    ompViewLeafCodeText(b, n, r),
		}
		if raw := firstStringArg(c.tool.Arguments, "path", "file_path"); raw != "" {
			base, _ := splitOmpReadPath(raw)
			if base != "" {
				props["path"] = base
			}
		}
		return tern.Node{Kind: tern.KindCode, Props: props}, true
	case "omp.tool.write.source":
		// upstream: tools/write.ts:152-154 — bare `code` node (no custom role) with `path`, `numbers:true`, `wrap:true`.
		// Preserve trailing newline from the written file (args.content or n.Lines).
		text := ompViewLeafCodeText(b, n, r)
		if raw, ok := c.tool.Arguments["content"].(string); ok && strings.HasSuffix(raw, "\n") && text != "" && !strings.HasSuffix(text, "\n") {
			text += "\n"
		}
		props := map[string]any{
			"numbers": true,
			"wrap":    true,
			"text":    text,
		}
		if p := firstStringArg(c.tool.Arguments, "file_path", "path"); p != "" {
			props["path"] = p
		}
		return tern.Node{Kind: tern.KindCode, Props: props}, true
	case "omp.tool.read.markdown":
		// upstream: tools/read.ts:863
		return tern.Node{
			Kind:  tern.KindMd,
			Props: map[string]any{"role": role, "text": ompViewLeafPlainText(b, n, r)},
		}, true

	case "omp.tool.read.image":
		// upstream: tools/read.ts:568 — guard MaxBlob (fixes B1) and include alt, w, h (fixes A10).
		if n.Image != nil && len(n.Image.Data) > 0 && len(n.Image.Data) <= tern.MaxBlob {
			alt := n.Image.Filename
			if alt == "" {
				alt = "image"
			}
			props := map[string]any{
				"role": role,
				"blob": c.s.blob(n.Image),
				"alt":  alt,
			}
			if cfg, _, err := image.DecodeConfig(bytes.NewReader(n.Image.Data)); err == nil {
				props["w"], props["h"] = cfg.Width, cfg.Height
			}
			return tern.Node{Kind: tern.KindImage, Props: props}, true
		}
		// Oversize or empty image falls back to viewer (ansi) so Apply never fails.
		return tern.Node{}, false

	// ── Shared tool body roles (tools/native-view.ts:152-202, tools/ast-edit.ts:395) ──
	case "omp.tool.error":
		// upstream: tools/native-view.ts:152-157 — JSON code block if multiline JSON, else error text.
		c.hasErrorChild = true
		body := ompViewLeafPlainText(b, n, r)
		if strings.Contains(body, "\n") && (strings.HasPrefix(body, "{") || strings.HasPrefix(body, "[")) {
			return tern.Node{
				Kind: tern.KindCode,
				Props: map[string]any{
					"role": role,
					"lang": "json",
					"tone": "error",
					"text": body,
				},
			}, true
		}
		return tern.Node{
			Kind: tern.KindText,
			Props: map[string]any{
				"role":  role,
				"wrap":  "word",
				"spans": []any{map[string]any{"t": body, "s": "error"}},
			},
		}, true

	case "omp.tool.stats":
		// upstream: tools/native-view.ts:176
		plain := ompViewLeafPlainText(b, n, r)
		return tern.Node{
			Kind: tern.KindText,
			Props: map[string]any{
				"role":  role,
				"wrap":  "word",
				"spans": []any{map[string]any{"t": plain, "s": "muted"}},
			},
		}, true

	case "omp.tool.notice":
		// upstream: tools/native-view.ts:185
		plain := ompViewLeafPlainText(b, n, r)
		if c.tool.Name == "wait" && strings.Contains(plain, "interrupted") {
			c.waitInterrupt = true
		}
		return tern.Node{
			Kind: tern.KindText,
			Props: map[string]any{
				"role":  role,
				"wrap":  "word",
				"spans": []any{map[string]any{"t": plain, "s": "warning"}},
			},
		}, true
	case "omp.tool.context":
		// upstream: tools/ast-edit.ts:395 (code span, 1 line, truncate end) vs tools/task.ts:1812 (muted, wrap word, 2 lines).
		plain := ompViewLeafPlainText(b, n, r)
		if c.tool.Name == "ast_edit" {
			return tern.Node{
				Kind: tern.KindText,
				Props: map[string]any{
					"role":     role,
					"lines":    1,
					"truncate": "end",
					"spans":    []any{map[string]any{"t": plain, "s": "code"}},
				},
			}, true
		}
		return tern.Node{
			Kind: tern.KindText,
			Props: map[string]any{
				"role":  role,
				"wrap":  "word",
				"lines": 2,
				"spans": []any{map[string]any{"t": plain, "s": "muted"}},
			},
		}, true

	default:
		// Any other omp.* role: map via viewer.kind (which recurses through
		// b.node -> ompMap for every child, fixing B2) and attach the role.
		el := b.kind(n, id, r, path)
		props := maps.Clone(el.Props)
		if props == nil {
			props = map[string]any{}
		}
		props["role"] = role
		el.Props = props
		return el, true
	}
}

// ompViewItemList maps a `proc://` / `irc` list role to kind `list` with
// `item` children (upstream: tools/proc-render.ts:386-402, 427-447, 524).
// Preserves `n.Gap` when advancing rows across child nodes (fixes B3).
func ompViewItemList(b *viewer, n *fe.ViewNode, id string, r region, path []int, role, empty string) tern.Node {
	props := map[string]any{"role": role}
	if empty != "" {
		props["empty"] = empty
	}
	var items []tern.Element
	switch {
	case n.List != nil && len(n.List.Items) > 0:
		for _, item := range n.List.Items {
			ip := map[string]any{}
			b.setRich(ip, "label", "label", item.Label)
			detail, value := item.Detail, ""
			if k := len(item.Columns); k > 0 {
				detail = joinNonEmpty(" · ", append([]string{item.Detail}, item.Columns[:k-1]...)...)
				value = item.Columns[k-1]
			}
			if detail != "" {
				b.setRich(ip, "detail", "detail", detail)
			}
			if value != "" {
				b.setRich(ip, "value", "value", value)
			}
			items = append(items, tern.Node{Kind: tern.KindItem, Props: ip})
		}
	case len(n.Items) > 0:
		for _, item := range n.Items {
			label := item.Label
			if label == "" {
				label = item.Value
			}
			ip := map[string]any{"label": []any{map[string]any{"t": label, "s": "toolOutput"}}}
			if item.Description != "" {
				ip["detail"] = []any{map[string]any{"t": item.Description, "s": "muted"}}
			}
			if item.CurrentValue != "" {
				ip["value"] = []any{map[string]any{"t": item.CurrentValue, "s": "muted"}}
			}
			items = append(items, tern.Node{Kind: tern.KindItem, Props: ip})
		}
	case len(n.Children) > 0:
		row := r.row
		for i := range n.Children {
			child := &n.Children[i]
			childR := region{row, r.col, child.Rows, r.width}
			row += child.Rows + n.Gap*boolInt(n.Kind == fe.ViewKindVStack)
			label := ompViewLeafPlainText(b, child, childR)
			if label == "" {
				continue
			}
			items = append(items, tern.Node{
				Kind:  tern.KindItem,
				Props: map[string]any{"label": []any{map[string]any{"t": label, "s": "toolOutput"}}},
			})
		}
	default:
		for line := range strings.SplitSeq(ompViewLeafRawText(b, n, r), "\n") {
			plain := strings.TrimSpace(stripANSI(line))
			if plain == "" {
				continue
			}
			items = append(items, tern.Node{
				Kind:  tern.KindItem,
				Props: map[string]any{"label": []any{map[string]any{"t": plain, "s": "toolOutput"}}},
			})
		}
	}
	return tern.Node{Kind: tern.KindList, Props: props, Children: items}
}

// ompExtractBashOutput extracts the bash tool's output text from a sibling
// `omp.tool.bash.output` view node (or falls back to tool.Output).
func ompExtractBashOutput(b *viewer, v *fe.View, lines []string, tool fe.ToolCard) string {
	if tool.Name != "bash" {
		return ""
	}
	if v != nil {
		if out, ok := findOmpBashOutputNode(b, &v.Root, region{0, 0, v.Root.Rows, v.Root.Width}); ok {
			return out
		}
	}
	return tool.Output
}

func findOmpBashOutputNode(b *viewer, n *fe.ViewNode, r region) (string, bool) {
	if n == nil {
		return "", false
	}
	if role, ok := ompViewRole(n.ID); ok && role == "omp.tool.bash.output" {
		return ompViewLeafRawText(b, n, r), true
	}
	row := r.row
	for i := range n.Children {
		child := &n.Children[i]
		childR := region{row, r.col, child.Rows, r.width}
		row += child.Rows + n.Gap*boolInt(n.Kind == fe.ViewKindVStack)
		if out, ok := findOmpBashOutputNode(b, child, childR); ok {
			return out, true
		}
	}
	return "", false
}

// ompBashDisplayWorkdir extracts a display working directory for a completed
// bash tool call, formatted like omp's formatToolWorkingDirectory:
//   - only emitted when a result exists (not pending/running);
//   - reads `cwd` from Arguments["cwd"];
//   - omits when equal to session cwd; relative when inside session cwd;
//     `~`-shortened when under $HOME outside session cwd.
func (c *ompViewCtx) ompBashDisplayWorkdir() string {
	if c.tool.Status == fe.ToolPending || c.tool.Status == fe.ToolRunning {
		return ""
	}
	var raw string
	if c.tool.Arguments != nil {
		if v, ok := c.tool.Arguments["cwd"].(string); ok {
			raw = strings.TrimSpace(v)
		}
	}
	if raw == "" {
		return ""
	}
	disp := raw
	var sessionCwd string
	if c.s != nil {
		sessionCwd = strings.TrimSpace(c.s.footer.Cwd)
		if c.s.displayPath != nil {
			disp = c.s.displayPath(raw)
			if sessionCwd != "" {
				sessionCwd = c.s.displayPath(sessionCwd)
			}
		}
	}
	cleanDisp := filepath.Clean(disp)
	if sessionCwd != "" {
		cleanSession := filepath.Clean(sessionCwd)
		if cleanDisp == cleanSession {
			return ""
		}
		if rel, err := filepath.Rel(cleanSession, cleanDisp); err == nil && rel != "." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != ".." {
			return rel
		}
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		cleanHome := filepath.Clean(home)
		if cleanDisp == cleanHome {
			return "~"
		}
		if strings.HasPrefix(cleanDisp, cleanHome+string(filepath.Separator)) {
			return "~" + cleanDisp[len(cleanHome):]
		}
	}
	return disp
}

// ompEvalTableNode converts an `omp.tool.eval.table` `*fe.ViewNode` carrying
// `List.Items` (item 0 = column headers, items 1..N = data rows) into Tern's
// native `table` node (upstream: packages/tui/src/tools/eval.ts:807-825).
// Honors optional `"align=start,end,..."` on `List.Items[0].Detail` and falls
// back to numeric auto-detection across data rows when absent.
func ompEvalTableNode(n *fe.ViewNode, role string) (tern.Node, bool) {
	if n == nil || n.List == nil || len(n.List.Items) < 2 {
		return tern.Node{}, false
	}
	headerItem := n.List.Items[0]
	headers := make([]string, 0, 1+len(headerItem.Columns))
	headers = append(headers, strings.TrimSpace(stripANSI(headerItem.Label)))
	for _, h := range headerItem.Columns {
		headers = append(headers, strings.TrimSpace(stripANSI(h)))
	}
	if len(headers) == 0 {
		return tern.Node{}, false
	}
	// Parse explicit alignment hints if provided in Items[0].Detail ("align=start,end,...").
	var explicitAlign []string
	if detail := strings.TrimSpace(stripANSI(headerItem.Detail)); strings.HasPrefix(detail, "align=") {
		for part := range strings.SplitSeq(strings.TrimPrefix(detail, "align="), ",") {
			explicitAlign = append(explicitAlign, strings.TrimSpace(part))
		}
	}
	dataItems := n.List.Items[1:]
	rows := make([]any, 0, len(dataItems))
	allNumeric := make([]bool, len(headers))
	hasNonEmpty := make([]bool, len(headers))
	for i := range allNumeric {
		allNumeric[i] = true
	}
	for rIdx, item := range dataItems {
		vals := make([]string, 0, 1+len(item.Columns))
		vals = append(vals, strings.TrimSpace(stripANSI(item.Label)))
		for _, colVal := range item.Columns {
			vals = append(vals, strings.TrimSpace(stripANSI(colVal)))
		}
		cells := make(map[string]any, len(headers))
		for cIdx := range headers {
			var v string
			if cIdx < len(vals) {
				v = vals[cIdx]
			}
			cells["c"+strconv.Itoa(cIdx)] = v
			if v != "" {
				hasNonEmpty[cIdx] = true
				if allNumeric[cIdx] && !isOmpNumericCell(v) {
					allNumeric[cIdx] = false
				}
			}
		}
		rows = append(rows, map[string]any{
			"id":    "r" + strconv.Itoa(rIdx),
			"cells": cells,
		})
	}
	cols := make([]any, 0, len(headers))
	for cIdx, head := range headers {
		col := map[string]any{
			"id":   "c" + strconv.Itoa(cIdx),
			"head": head,
		}
		if cIdx < len(explicitAlign) {
			if explicitAlign[cIdx] == "end" {
				col["align"] = "end"
			}
		} else if hasNonEmpty[cIdx] && allNumeric[cIdx] {
			col["align"] = "end"
		}
		cols = append(cols, col)
	}
	return tern.Node{
		Kind: tern.KindTable,
		Props: map[string]any{
			"role": role,
			"cols": cols,
			"rows": rows,
		},
	}, true
}

func isOmpNumericCell(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	s = strings.TrimPrefix(s, "$")
	s = strings.TrimSuffix(s, "%")
	s = strings.ReplaceAll(s, ",", "")
	_, err := strconv.ParseFloat(s, 64)
	return err == nil
}

// ompJobTileChartNode parses sparkline numeric values from `omp.job.tile.chart`
// (either a VStack containing `omp.job.tile.chart.summary` + `omp.job.tile.chart.series`
// CSV floats, or a leaf text carrying `spark:<csv>`) and emits Tern's native
// `chart` node (upstream: packages/tui/src/tools/job-widget.ts:498-502).
func ompJobTileChartNode(b *viewer, n *fe.ViewNode, r region, role string) (tern.Node, bool) {
	if n == nil {
		return tern.Node{}, false
	}
	var summary string
	var seriesCSV string
	var minVal, maxVal *float64
	token := "accent"
	if len(n.Children) > 0 {
		row := r.row
		for i := range n.Children {
			child := &n.Children[i]
			childR := region{row, r.col, child.Rows, r.width}
			row += child.Rows + n.Gap*boolInt(n.Kind == fe.ViewKindVStack)
			cr, _ := ompViewRole(child.ID)
			txt := ompViewLeafPlainText(b, child, childR)
			switch cr {
			case "omp.job.tile.chart.summary":
				summary = txt
			case "omp.job.tile.chart.series":
				seriesCSV = txt
			case "omp.job.tile.chart.min":
				if v, err := strconv.ParseFloat(strings.TrimSpace(txt), 64); err == nil {
					minVal = &v
				}
			case "omp.job.tile.chart.max":
				if v, err := strconv.ParseFloat(strings.TrimSpace(txt), 64); err == nil {
					maxVal = &v
				}
			case "omp.job.tile.chart.tone":
				if t := strings.TrimSpace(txt); t != "" {
					token = t
				}
			default:
				if strings.HasPrefix(txt, "spark:") {
					seriesCSV = strings.TrimPrefix(txt, "spark:")
				} else if summary == "" {
					summary = txt
				}
			}
		}
	} else {
		raw := ompViewLeafPlainText(b, n, r)
		if idx := strings.Index(raw, "spark:"); idx >= 0 {
			summary = strings.TrimSpace(raw[:idx])
			rest := strings.TrimSpace(raw[idx+len("spark:"):])
			if sp := strings.IndexByte(rest, '|'); sp >= 0 {
				seriesCSV = strings.TrimSpace(rest[:sp])
				if summary == "" {
					summary = strings.TrimSpace(rest[sp+1:])
				}
			} else {
				seriesCSV = rest
			}
		}
	}
	vals := parseOmpSparkSeries(seriesCSV)
	if len(vals) == 0 {
		return tern.Node{}, false
	}
	lo, hi := vals[0], vals[0]
	series := make([]any, len(vals))
	for i, v := range vals {
		if v < lo {
			lo = v
		}
		if v > hi {
			hi = v
		}
		series[i] = map[string]any{"value": v}
	}
	if minVal != nil {
		lo = *minVal
	}
	if maxVal != nil {
		hi = *maxVal
	}
	props := map[string]any{
		"role":   role,
		"kind":   "bars",
		"size":   "sm",
		"token":  token,
		"min":    lo,
		"max":    hi,
		"series": series,
	}
	if summary != "" {
		props["summary"] = summary
	}
	return tern.Node{Kind: tern.KindChart, Props: props}, true
}

func parseOmpSparkSeries(csv string) []float64 {
	csv = strings.TrimSpace(csv)
	if csv == "" {
		return nil
	}
	var out []float64
	for part := range strings.SplitSeq(csv, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		v, err := strconv.ParseFloat(part, 64)
		if err != nil {
			return nil
		}
		out = append(out, v)
	}
	return out
}

func ompBaseName(file string) string {
	isDir := strings.HasSuffix(file, "/")
	trimmed := strings.TrimSuffix(file, "/")
	if slash := strings.LastIndexByte(trimmed, '/'); slash >= 0 && slash+1 < len(trimmed) {
		trimmed = trimmed[slash+1:]
	}
	if isDir {
		return trimmed + "/"
	}
	return trimmed
}

// ompFileItemHref resolves a file:/// URI for `glob` and `find` file rows/chips
// (upstream: tools/glob.ts:100-108, tools/find.ts:235). Grep/ast_grep/lsp pass
// no href to fileRow (grouped-file-output.ts:741, lsp.ts:1009,1061).
func (c *ompViewCtx) ompFileItemHref(file string) string {
	if c == nil || c.s == nil || file == "" || strings.HasSuffix(file, "/") {
		return ""
	}
	if c.tool.Name != "glob" && c.tool.Name != "find" {
		return ""
	}
	if strings.HasPrefix(file, "http://") || strings.HasPrefix(file, "https://") {
		return ""
	}
	return c.s.ompFileHref(file)
}

func ompFileRowSpans(file, href string) []any {
	if file == "" {
		return []any{map[string]any{"t": "…", "s": "strong"}}
	}
	if strings.HasPrefix(file, "http://") || strings.HasPrefix(file, "https://") {
		return []any{map[string]any{"t": file, "s": "strong", "href": file}}
	}
	trimmed := strings.TrimSuffix(file, "/")
	slash := strings.LastIndexByte(trimmed, '/')
	if slash < 0 || slash+1 >= len(file) {
		sp := map[string]any{"t": file, "s": "strong"}
		if href != "" {
			sp["href"] = href
		}
		return []any{sp}
	}
	leaf := map[string]any{"t": file[slash+1:], "s": "strong"}
	if href != "" {
		leaf["href"] = href
	}
	return []any{
		map[string]any{"t": file[:slash+1], "s": "dim"},
		leaf,
	}
}
func ompEvalHighlightLang(args map[string]any) string {
	switch strings.ToLower(firstStringArg(args, "language")) {
	case "py", "python":
		return "python"
	case "js", "javascript", "ts", "typescript":
		return "javascript"
	default:
		return ""
	}
}

// parseOmpSearchMeta parses `omp.tool.search.meta` payloads emitted by
// `internal/ansi/native_search.go`: comma-separated `[*]<lineNum>` tokens
// (e.g. `"*1,*2"` or `"10,*11,12"`). Returns ordered line numbers and marked
// match line numbers (upstream: tools/grouped-file-output.ts:674-684).
func parseOmpSearchMeta(spec string) (nums []int, marks []int, ok bool) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return nil, nil, false
	}
	for tok := range strings.SplitSeq(spec, ",") {
		tok = strings.TrimSpace(tok)
		if tok == "" {
			continue
		}
		marked := false
		if strings.HasPrefix(tok, "*") {
			marked = true
			tok = tok[1:]
		}
		n, err := strconv.Atoi(tok)
		if err != nil || n <= 0 {
			continue
		}
		nums = append(nums, n)
		if marked {
			marks = append(marks, n)
		}
	}
	return nums, marks, len(nums) > 0
}

func ompRunStateTone(word string) string {
	lower := strings.ToLower(strings.TrimSpace(word))
	switch {
	case lower == "done" || lower == "completed":
		return "success"
	case lower == "running" || lower == "in background":
		return "pending"
	case lower == "timed out":
		return "warning"
	case lower == "cancelled":
		return "muted"
	case lower == "failed" || strings.HasPrefix(lower, "exit "):
		return "error"
	default:
		return "muted"
	}
}

func ompAskMarkToken(mark string) string {
	switch strings.TrimSpace(mark) {
	case "✓":
		return "success"
	case "!":
		return "error"
	default:
		return "muted"
	}
}

// ompFootSettled reports whether an `omp.run.foot` node carries a settled
// sibling `omp.run.state` (anything other than Running / In background).
func ompFootSettled(b *viewer, n *fe.ViewNode, r region) bool {
	col := r.col
	for i := range n.Children {
		child := &n.Children[i]
		childR := region{r.row, col, r.rows, child.Width}
		col += child.Width + n.Gap
		if cr, ok := ompViewRole(child.ID); ok && cr == "omp.run.state" {
			word := strings.ToLower(strings.TrimSpace(ompViewLeafPlainText(b, child, childR)))
			return word != "" && word != "running" && word != "in background"
		}
	}
	return false
}

// ompTileHeadSettled reports whether an `omp.job.tile.head` node carries a
// settled `omp.job.tile.dot` glyph (`✓`, `✕`, `■`; running is `●`).
func ompTileHeadSettled(b *viewer, n *fe.ViewNode, r region) bool {
	col := r.col
	for i := range n.Children {
		child := &n.Children[i]
		childR := region{r.row, col, r.rows, child.Width}
		col += child.Width + n.Gap
		if cr, ok := ompViewRole(child.ID); ok && cr == "omp.job.tile.dot" {
			glyph := strings.TrimSpace(ompViewLeafPlainText(b, child, childR))
			return glyph == "✓" || glyph == "✕" || glyph == "■"
		}
	}
	return false
}

// ompDiagnosticSeverity extracts the leading severity from a diagnostic row
// (`[error] ...`, `error ...`, etc.) and maps it to omp's icon & tone
// (upstream: packages/tui/src/tools/native-view.ts:309-325).
func ompDiagnosticSeverity(line string) (severity, iconName, tone string) {
	lower := strings.ToLower(strings.TrimSpace(line))
	switch {
	case strings.HasPrefix(lower, "[error]") || strings.HasPrefix(lower, "error:") || strings.HasPrefix(lower, "error "):
		return "error", "x-circle", "error"
	case strings.HasPrefix(lower, "[warning]") || strings.HasPrefix(lower, "[warn]") || strings.HasPrefix(lower, "warning:") || strings.HasPrefix(lower, "warning "):
		return "warning", "warn", "warning"
	case strings.HasPrefix(lower, "[info]") || strings.HasPrefix(lower, "info:") || strings.HasPrefix(lower, "info "):
		return "info", "info", "info"
	case strings.HasPrefix(lower, "[hint]") || strings.HasPrefix(lower, "hint:") || strings.HasPrefix(lower, "hint "):
		return "hint", "lightbulb", "muted"
	default:
		return "warning", "warn", "warning"
	}
}

// ompDiagnosticsHasError reports whether any `omp.tool.diagnostic` child of an
// `omp.tool.diagnostics` section has severity "error".
func ompDiagnosticsHasError(b *viewer, n *fe.ViewNode, r region) bool {
	row := r.row
	for i := range n.Children {
		child := &n.Children[i]
		childR := region{row, r.col, child.Rows, r.width}
		row += child.Rows + n.Gap*boolInt(n.Kind == fe.ViewKindVStack)
		if cr, ok := ompViewRole(child.ID); ok && cr == "omp.tool.diagnostic" {
			if sev, _, _ := ompDiagnosticSeverity(ompViewLeafPlainText(b, child, childR)); sev == "error" {
				return true
			}
		}
	}
	return false
}

// mapTodoChecklistNode converts `omp.todo.checklist` (containing `omp.todo.phase#<p>`
// with `omp.todo.phase.head#<p>`, `omp.todo.item#<p>.<t>`, and optional
// `omp.todo.item.status#<p>.<t>`) into one native Tern `checklist` node matching
// omp's `describeTodoResult` (packages/tui/src/tools/todo.ts:550-595).
func (c *ompViewCtx) mapTodoChecklistNode(b *viewer, n *fe.ViewNode, r region) tern.Node {
	phases := make([]any, 0, len(n.Children))
	totalItems := 0
	closedItems := 0
	row := r.row
	pIdx := 0
	for i := range n.Children {
		ph := &n.Children[i]
		phRegion := region{row, r.col, ph.Rows, r.width}
		row += ph.Rows + n.Gap*boolInt(n.Kind == fe.ViewKindVStack)
		pr, _ := ompViewRole(ph.ID)
		if pr != "omp.todo.phase" {
			continue
		}
		var title string
		type rawItem struct {
			id     string
			text   string
			status string
		}
		items := make([]rawItem, 0, len(ph.Children))
		statusBySuffix := map[string]string{}
		// First pass: collect explicit `omp.todo.item.status#<p>.<t>` nodes.
		subRow := phRegion.row
		for j := range ph.Children {
			ch := &ph.Children[j]
			chRegion := region{subRow, phRegion.col, ch.Rows, phRegion.width}
			subRow += ch.Rows + ph.Gap*boolInt(ph.Kind == fe.ViewKindVStack)
			cr, _ := ompViewRole(ch.ID)
			if cr == "omp.todo.item.status" {
				if _, suf, has := strings.Cut(ch.ID, "#"); has {
					statusBySuffix[suf] = strings.TrimSpace(ompViewLeafPlainText(b, ch, chRegion))
				}
			}
		}
		// Second pass: collect head and items.
		subRow = phRegion.row
		tIdx := 0
		for j := range ph.Children {
			ch := &ph.Children[j]
			chRegion := region{subRow, phRegion.col, ch.Rows, phRegion.width}
			subRow += ch.Rows + ph.Gap*boolInt(ph.Kind == fe.ViewKindVStack)
			cr, _ := ompViewRole(ch.ID)
			switch cr {
			case "omp.todo.phase.head":
				title = ompViewLeafPlainText(b, ch, chRegion)
			case "omp.todo.item":
				rawText := ompViewLeafPlainText(b, ch, chRegion)
				status := ""
				if _, suf, has := strings.Cut(ch.ID, "#"); has {
					status = statusBySuffix[suf]
				}
				glyphStatus, cleanText := ompSplitTodoItemGlyph(rawText)
				if status == "" {
					status = glyphStatus
				}
				items = append(items, rawItem{
					id:     fmt.Sprintf("p%d.%d", pIdx, tIdx),
					text:   cleanText,
					status: status,
				})
				tIdx++
			}
		}
		if len(items) == 0 {
			continue
		}
		phaseClosed := 0
		wireItems := make([]any, 0, len(items))
		for _, it := range items {
			if it.status == "done" || it.status == "dropped" {
				phaseClosed++
				closedItems++
			}
			totalItems++
			wireItems = append(wireItems, map[string]any{
				"id":     it.id,
				"text":   it.text,
				"status": it.status,
			})
		}
		phases = append(phases, map[string]any{
			"id":        fmt.Sprintf("p%d", pIdx),
			"title":     title,
			"items":     wireItems,
			"collapsed": len(items) > 0 && phaseClosed == len(items),
		})
		pIdx++
	}

	if totalItems > 0 {
		c.headPatch.target = fmt.Sprintf("%d/%d", closedItems, totalItems)
		c.headPatch.targetKind = "text"
		c.headPatch.open = true
		c.headPatch.preview = "none"
	}
	return tern.Node{
		Kind: tern.KindChecklist,
		Props: map[string]any{
			"mode":   "full",
			"phases": phases,
		},
	}
}

// ompSplitTodoItemGlyph strips a leading checkbox glyph (`☑`, `◐`, `☒`, `☐`)
// from an `omp.todo.item` text row and returns its Tern checklist status and
// clean text.
func ompSplitTodoItemGlyph(raw string) (status, text string) {
	trimmed := strings.TrimSpace(raw)
	switch {
	case strings.HasPrefix(trimmed, "☑"):
		return "done", strings.TrimSpace(strings.TrimPrefix(trimmed, "☑"))
	case strings.HasPrefix(trimmed, "◐"):
		return "active", strings.TrimSpace(strings.TrimPrefix(trimmed, "◐"))
	case strings.HasPrefix(trimmed, "☒"):
		return "dropped", strings.TrimSpace(strings.TrimPrefix(trimmed, "☒"))
	case strings.HasPrefix(trimmed, "☐"):
		return "pending", strings.TrimSpace(strings.TrimPrefix(trimmed, "☐"))
	default:
		return "pending", trimmed
	}
}

// mapTaskAgentNode converts an `omp.task.agent` VStack (carrying structured
// `omp.task.agent.*` metadata nodes plus body children such as
// `omp.task.assignment`, `omp.task.output`, `omp.tool.error`, nested
// `omp.task.agent`) into a native Tern `agent` node matching omp's
// `describeResultAgent` / `describeProgressAgent` (packages/tui/src/tools/task.ts:1890-2041).
func (c *ompViewCtx) mapTaskAgentNode(b *viewer, n *fe.ViewNode, id string, r region, path []int) tern.Node {
	var (
		name          string
		agentKind     string
		status        string
		taskSummary   string
		model         string
		thinking      string
		requests      int
		tokens        int
		toolCount     int
		contextTokens int
		contextWindow int
		cost          float64
		ageMs         int64
		hasAge        bool
		tookMs        int64
		hasTook       bool
		curTool       string
		curToolIntent string
		badgesRaw     string
		depth         int
		rowFallback   string
		assignmentTxt string
		hasStructMeta bool
	)

	bodyChildren := make([]tern.Element, 0, len(n.Children))
	row := r.row
	for i := range n.Children {
		ch := &n.Children[i]
		chRegion := region{row, r.col, ch.Rows, r.width}
		row += ch.Rows + n.Gap*boolInt(n.Kind == fe.ViewKindVStack)
		cr, ok := ompViewRole(ch.ID)
		if ok {
			switch cr {
			case "omp.task.agent.row":
				rowFallback = ompViewLeafPlainText(b, ch, chRegion)
				continue
			case "omp.task.agent.name":
				name = ompViewLeafPlainText(b, ch, chRegion)
				hasStructMeta = true
				continue
			case "omp.task.agent.kind":
				agentKind = ompViewLeafPlainText(b, ch, chRegion)
				hasStructMeta = true
				continue
			case "omp.task.agent.status":
				status = ompViewLeafPlainText(b, ch, chRegion)
				hasStructMeta = true
				continue
			case "omp.task.agent.task":
				taskSummary = ompViewLeafPlainText(b, ch, chRegion)
				hasStructMeta = true
				continue
			case "omp.task.agent.model":
				model = ompViewLeafPlainText(b, ch, chRegion)
				hasStructMeta = true
				continue
			case "omp.task.agent.thinking":
				thinking = ompViewLeafPlainText(b, ch, chRegion)
				hasStructMeta = true
				continue
			case "omp.task.agent.requests":
				requests, _ = strconv.Atoi(ompViewLeafPlainText(b, ch, chRegion))
				hasStructMeta = true
				continue
			case "omp.task.agent.tokens":
				tokens, _ = strconv.Atoi(ompViewLeafPlainText(b, ch, chRegion))
				hasStructMeta = true
				continue
			case "omp.task.agent.tools":
				toolCount, _ = strconv.Atoi(ompViewLeafPlainText(b, ch, chRegion))
				hasStructMeta = true
				continue
			case "omp.task.agent.context_tokens":
				contextTokens, _ = strconv.Atoi(ompViewLeafPlainText(b, ch, chRegion))
				hasStructMeta = true
				continue
			case "omp.task.agent.context_window":
				contextWindow, _ = strconv.Atoi(ompViewLeafPlainText(b, ch, chRegion))
				hasStructMeta = true
				continue
			case "omp.task.agent.cost":
				cost, _ = strconv.ParseFloat(ompViewLeafPlainText(b, ch, chRegion), 64)
				hasStructMeta = true
				continue
			case "omp.task.agent.age":
				if v, err := strconv.ParseInt(ompViewLeafPlainText(b, ch, chRegion), 10, 64); err == nil {
					ageMs = v
					hasAge = true
				}
				hasStructMeta = true
				continue
			case "omp.task.agent.took":
				if v, err := strconv.ParseInt(ompViewLeafPlainText(b, ch, chRegion), 10, 64); err == nil {
					tookMs = v
					hasTook = true
				}
				hasStructMeta = true
				continue
			case "omp.task.agent.tool":
				curTool = ompViewLeafPlainText(b, ch, chRegion)
				hasStructMeta = true
				continue
			case "omp.task.agent.tool_intent":
				curToolIntent = ompViewLeafPlainText(b, ch, chRegion)
				hasStructMeta = true
				continue
			case "omp.task.agent.badges":
				badgesRaw = ompViewLeafPlainText(b, ch, chRegion)
				hasStructMeta = true
				continue
			case "omp.task.agent.depth":
				depth, _ = strconv.Atoi(ompViewLeafPlainText(b, ch, chRegion))
				hasStructMeta = true
				continue
			case "omp.task.assignment":
				assignmentTxt = ompViewLeafPlainText(b, ch, chRegion)
			}
		}
		cid := id + "." + strconv.Itoa(i)
		if el := b.node(ch, cid, chRegion, childPath(path, i)); el.Kind != "" {
			bodyChildren = append(bodyChildren, el)
		}
	}
	// Fallback parsing if a caller/test passed only legacy `omp.task.agent.row`.
	if !hasStructMeta && rowFallback != "" {
		parts := strings.Split(rowFallback, " · ")
		if len(parts) > 0 {
			head := strings.TrimSpace(parts[0])
			if open := strings.LastIndex(head, " ("); open > 0 && strings.HasSuffix(head, ")") {
				name = strings.TrimSpace(head[:open])
				agentKind = strings.TrimSpace(head[open+2 : len(head)-1])
			} else {
				name = head
			}
		}
		for _, part := range parts[1:] {
			p := strings.TrimSpace(part)
			switch p {
			case "pending", "running", "done", "failed", "aborted":
				status = p
			case "completed":
				status = "done"
			case "background", "isolated":
				if badgesRaw != "" {
					badgesRaw += "," + p
				} else {
					badgesRaw = p
				}
			}
		}
	}

	if depth == 0 {
		c.taskAgentCount++
	}
	if status == "" {
		status = "pending"
	}
	if status == "completed" {
		status = "done"
	}
	if name == "" {
		name = "agent"
	}

	props := map[string]any{
		"name":        name,
		"status":      status,
		"collapsible": true,
		"collapsed":   !c.tool.Expanded,
	}
	if agentKind != "" {
		props["agent"] = agentKind
	}
	if taskSummary != "" {
		props["task"] = taskSummary
	}
	if model != "" {
		props["model"] = model
	}
	if thinking != "" {
		props["thinking"] = thinking
	}

	stats := map[string]any{}
	if toolCount > 0 {
		stats["tools"] = toolCount
	}
	if requests > 0 {
		stats["requests"] = requests
	}
	if tokens > 0 {
		stats["tokens"] = tokens
	}
	if contextTokens > 0 {
		if contextWindow > 0 {
			frac := float64(contextTokens) / float64(contextWindow)
			if frac > 1 {
				frac = 1
			}
			stats["context"] = frac
			stats["contextLabel"] = ompCompactNumber(contextTokens) + " / " + ompCompactNumber(contextWindow)
		} else {
			stats["contextLabel"] = ompCompactNumber(contextTokens)
		}
	}
	if cost > 0 {
		stats["cost"] = cost
	}
	if status == "running" && hasAge {
		stats["age"] = ageMs
	} else if hasTook {
		stats["took"] = tookMs
	}
	if len(stats) > 0 {
		props["stats"] = stats
	}
	if status == "running" && curTool != "" {
		t := map[string]any{"name": curTool}
		if curToolIntent != "" {
			t["intent"] = curToolIntent
		}
		props["tool"] = t
	}
	if badgesRaw != "" {
		var badges []any
		for rawBadge := range strings.SplitSeq(badgesRaw, ",") {
			rawBadge = strings.TrimSpace(rawBadge)
			if rawBadge == "" {
				continue
			}
			if text, tone, hasTone := strings.Cut(rawBadge, ":"); hasTone {
				badges = append(badges, map[string]any{"text": text, "tone": tone})
			} else {
				if rawBadge == "background" && depth == 0 {
					c.taskBackground = true
				}
				badges = append(badges, map[string]any{"text": rawBadge})
			}
		}
		if len(badges) > 0 {
			props["badges"] = badges
		}
	}
	if depth > 0 {
		props["depth"] = depth
	}
	if assignmentTxt != "" {
		// upstream: tools/task.ts:1896 — hovercard carries the assignment md node.
		props["hovercard"] = map[string]any{
			"k": "md",
			"p": map[string]any{
				"role": "omp.task.assignment",
				"text": assignmentTxt,
			},
		}
	}
	return tern.Node{
		Kind:     tern.KindAgent,
		Props:    props,
		Children: bodyChildren,
	}
}

func ompCompactNumber(n int) string {
	if n >= 1_000_000 {
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	}
	if n >= 1_000 {
		return fmt.Sprintf("%.0fK", float64(n)/1_000)
	}
	return strconv.Itoa(n)
}

// ompJobDotTone maps omp's job tile glyph (`✓`/`✕`/`■`/`●`) to its TSP tone
// (upstream: tools/job-widget.ts:370-375, TILE_GLYPH / TILE_TONE).
func ompJobDotTone(dot string) string {
	switch strings.TrimSpace(dot) {
	case "✓":
		return "success"
	case "✕":
		return "error"
	case "■":
		return "warning"
	case "●":
		return "info"
	default:
		return ""
	}
}

// ompJobTileTone inspects an `omp.job.tile`'s head dot glyph to derive its tone.
func ompJobTileTone(b *viewer, tile *fe.ViewNode, r region) string {
	row := r.row
	for i := range tile.Children {
		ch := &tile.Children[i]
		chR := region{row, r.col, ch.Rows, r.width}
		row += ch.Rows + tile.Gap*boolInt(tile.Kind == fe.ViewKindVStack)
		if cr, ok := ompViewRole(ch.ID); ok && cr == "omp.job.tile.head" {
			subRow := chR.row
			for j := range ch.Children {
				gc := &ch.Children[j]
				gcR := region{subRow, chR.col, gc.Rows, chR.width}
				subRow += gc.Rows + ch.Gap*boolInt(ch.Kind == fe.ViewKindVStack)
				if gcr, ok := ompViewRole(gc.ID); ok && gcr == "omp.job.tile.dot" {
					return ompJobDotTone(ompViewLeafPlainText(b, gc, gcR))
				}
			}
		}
	}
	return ""
}

// ompCountAstEditReplacements counts replacement hunks in ast_edit fallback output
// (lines prefixed `-N│` or `+N│`).
func ompCountAstEditReplacements(raw string) int {
	removed, added := 0, 0
	for line := range strings.SplitSeq(raw, "\n") {
		if strings.HasPrefix(line, "-") && strings.Contains(line, "│") {
			removed++
		} else if strings.HasPrefix(line, "+") && strings.Contains(line, "│") {
			added++
		}
	}
	if removed > 0 {
		return removed
	}
	return added
}

// ompDockWidgets holds the recognized omp dock widgets extracted from PiG's
// `Lines("dock").View` (`omp.errorBanner` and `omp.hud.todo`) plus any other
// above-editor dock views, so `ompView` can place each into omp's
// `nativeDock` slot containers (upstream:
// packages/coding-agent/src/modes/interactive-mode.ts:2820-2835).
type ompDockWidgets struct {
	errorBanner *tern.Node
	other       tern.Nodes
	todo        *tern.Node // checklist[omp.hud.todo] mounted inside row[omp.hud.activity]
	predictions []string   // composer prompt predictions from hidden omp.composer.prediction widget
}

func (s *session) ompDockWidgetParts(tid string, v *fe.View, lines []string) (ompDockWidgets, bool) {
	if v == nil {
		return ompDockWidgets{}, false
	}
	type widgetPart struct {
		node *fe.ViewNode
		r    region
		path []int
	}
	var parts []widgetPart
	if role, ok := ompViewRole(v.Root.ID); ok && (role == "omp.hud.todo" || role == "omp.errorBanner" || role == "omp.composer.prediction") {
		parts = append(parts, widgetPart{node: &v.Root, r: region{0, 0, v.Root.Rows, v.Root.Width}, path: []int{}})
	} else if v.Root.Kind == fe.ViewKindContainer && v.Root.ID == "" && len(v.Root.Children) > 0 {
		row := 0
		for i := range v.Root.Children {
			ch := &v.Root.Children[i]
			parts = append(parts, widgetPart{node: ch, r: region{row, 0, ch.Rows, ch.Width}, path: []int{i}})
			row += ch.Rows
		}
	} else {
		return ompDockWidgets{}, false
	}
	hasOmpWidget := false
	for _, p := range parts {
		if role, ok := ompViewRole(p.node.ID); ok && (role == "omp.hud.todo" || role == "omp.errorBanner" || role == "omp.composer.prediction") {
			hasOmpWidget = true
			break
		}
	}
	if !hasOmpWidget {
		return ompDockWidgets{}, false
	}

	s.dropViewRules(tid)
	s.viewsShown = true
	b := &viewer{
		s:      s,
		lines:  lines,
		tokens: s.viewTokens(nil, v.Theme),
		theme:  v.Theme,
	}
	ctx := &ompViewCtx{s: s}
	b.ompMap = ctx.mapNode

	var (
		out       ompDockWidgets
		sawBanner bool
	)
	for i, p := range parts {
		role, ok := ompViewRole(p.node.ID)
		if ok && role == "omp.errorBanner" {
			sawBanner = true
			if banner, visible := s.ompErrorBannerNode(b, p.node, p.r); visible {
				built := buildNode(banner)
				out.errorBanner = &built
			}
			continue
		}
		if ok && role == "omp.hud.todo" {
			if checklist, visible := ompHudTodoChecklistNode(b, p.node, p.r); visible {
				built := buildNode(checklist)
				out.todo = &built
			}
			continue
		}
		if ok && role == "omp.composer.prediction" {
			out.predictions = ompExtractPredictionChoices(b, p.node, p.r)
			continue
		}
		// Skip blank non-widget spacer lines in dock.
		if p.node.Kind == fe.ViewKindLines && blank(ompViewLeafRawText(b, p.node, p.r)) {
			continue
		}
		if p.node.Rows == 0 {
			continue
		}
		el := b.node(p.node, fmt.Sprintf("%s.w%d", tid, i), p.r, p.path)
		if el.Kind != "" {
			props := maps.Clone(el.Props)
			if props == nil {
				props = map[string]any{}
			}
			props["key"] = fmt.Sprintf("w%d", i)
			el.Props = props
			out.other = append(out.other, buildNode(el))
		}
	}
	if !sawBanner {
		s.errorBannerMsg = ""
		s.errorBannerExpanded = false
		s.errorBannerDismissed = false
	}
	return out, true
}

// ompExtractPredictionChoices extracts the ordered prompt prediction strings
// from a hidden `omp.composer.prediction` dock widget containing
// `omp.composer.prediction.choice#<i>` text children.
func ompExtractPredictionChoices(b *viewer, n *fe.ViewNode, r region) []string {
	var choices []string
	var walk func(cur *fe.ViewNode, reg region)
	walk = func(cur *fe.ViewNode, reg region) {
		if cur == nil {
			return
		}
		if role, ok := ompViewRole(cur.ID); ok && role == "omp.composer.prediction.choice" {
			text := strings.TrimSpace(cur.Text)
			if text == "" {
				text = ompViewLeafPlainText(b, cur, reg)
			}
			if text != "" {
				choices = append(choices, text)
			}
			return
		}
		row := reg.row
		for i := range cur.Children {
			ch := &cur.Children[i]
			walk(ch, region{row, reg.col, ch.Rows, ch.Width})
			row += ch.Rows
		}
	}
	walk(n, r)
	return choices
}

// ompHudTodoChecklistNode builds omp's dock todo HUD `checklist[omp.hud.todo]`
// (upstream: packages/coding-agent/src/modes/interactive-mode.ts:5675-5705).
// `ompView` mounts it inside `row[omp.hud.activity]` alongside any live
// `col[omp.hud.status]` (working row).
func ompHudTodoChecklistNode(b *viewer, n *fe.ViewNode, r region) (tern.Node, bool) {
	phases := make([]any, 0, len(n.Children))
	row := r.row
	pIdx := 0
	for i := range n.Children {
		ph := &n.Children[i]
		phRegion := region{row, r.col, ph.Rows, r.width}
		row += ph.Rows + n.Gap*boolInt(n.Kind == fe.ViewKindVStack)
		pr, _ := ompViewRole(ph.ID)
		if pr != "omp.todo.phase" {
			continue
		}
		var title string
		type hudItem struct {
			id     string
			text   string
			status string
			note   string
		}
		items := make([]hudItem, 0, len(ph.Children))
		statusBySuffix := map[string]string{}
		noteBySuffix := map[string]string{}
		subRow := phRegion.row
		for j := range ph.Children {
			ch := &ph.Children[j]
			chRegion := region{subRow, phRegion.col, ch.Rows, phRegion.width}
			subRow += ch.Rows + ph.Gap*boolInt(ph.Kind == fe.ViewKindVStack)
			cr, _ := ompViewRole(ch.ID)
			switch cr {
			case "omp.todo.item.status":
				if _, suf, has := strings.Cut(ch.ID, "#"); has {
					statusBySuffix[suf] = strings.TrimSpace(ompViewLeafPlainText(b, ch, chRegion))
				}
			case "omp.todo.item.note":
				if _, suf, has := strings.Cut(ch.ID, "#"); has {
					noteBySuffix[suf] = strings.TrimSpace(ompViewLeafPlainText(b, ch, chRegion))
				}
			}
		}
		subRow = phRegion.row
		tIdx := 0
		for j := range ph.Children {
			ch := &ph.Children[j]
			chRegion := region{subRow, phRegion.col, ch.Rows, phRegion.width}
			subRow += ch.Rows + ph.Gap*boolInt(ph.Kind == fe.ViewKindVStack)
			cr, _ := ompViewRole(ch.ID)
			switch cr {
			case "omp.todo.phase.head":
				title = ompViewLeafPlainText(b, ch, chRegion)
			case "omp.todo.item":
				rawText := ompViewLeafPlainText(b, ch, chRegion)
				status := ""
				note := ""
				if _, suf, has := strings.Cut(ch.ID, "#"); has {
					status = statusBySuffix[suf]
					note = noteBySuffix[suf]
				}
				glyphStatus, cleanText := ompSplitTodoItemGlyph(rawText)
				if status == "" {
					status = glyphStatus
				}
				items = append(items, hudItem{
					id:     fmt.Sprintf("%d.%d", pIdx, tIdx),
					text:   cleanText,
					status: status,
					note:   note,
				})
				tIdx++
			}
		}
		if len(items) == 0 {
			continue
		}
		phaseClosed := 0
		wireItems := make([]any, 0, len(items))
		for _, it := range items {
			if it.status == "done" || it.status == "dropped" {
				phaseClosed++
			}
			itemObj := map[string]any{
				"id":     it.id,
				"text":   it.text,
				"status": it.status,
			}
			if it.note != "" {
				itemObj["note"] = it.note
			}
			wireItems = append(wireItems, itemObj)
		}
		phaseObj := map[string]any{
			"id":    strconv.Itoa(pIdx),
			"title": title,
			"items": wireItems,
		}
		// upstream: interactive-mode.ts:5679 — `collapsed: phase.tasks.every(isClosedTodo) || undefined`
		if phaseClosed == len(items) {
			phaseObj["collapsed"] = true
		}
		phases = append(phases, phaseObj)
		pIdx++
	}
	if len(phases) == 0 {
		return tern.Node{}, false
	}
	return tern.Node{
		Kind: tern.KindChecklist,
		Props: map[string]any{
			"key":    "todo",
			"role":   "omp.hud.todo",
			"mode":   "hud",
			"phases": phases,
		},
	}, true
}

// ompErrorBannerNode builds omp's pinned dock error banner
// (`row[omp.errorBanner]`, upstream:
// packages/tui/src/overlays/error-banner.ts:60-93,
// packages/tui/src/native/overlay.ts:119-143).
func (s *session) ompErrorBannerNode(b *viewer, n *fe.ViewNode, r region) (tern.Node, bool) {
	var msg string
	if len(n.Children) > 0 {
		row := r.row
		for i := range n.Children {
			ch := &n.Children[i]
			chR := region{row, r.col, ch.Rows, r.width}
			row += ch.Rows + n.Gap*boolInt(n.Kind == fe.ViewKindVStack)
			if cr, ok := ompViewRole(ch.ID); ok && cr == "omp.errorBanner.message" {
				msg = ompViewLeafPlainText(b, ch, chR)
				break
			}
		}
	}
	if msg == "" {
		msg = ompViewLeafPlainText(b, n, r)
	}
	msg = strings.TrimSpace(msg)
	if msg == "" {
		return tern.Node{}, false
	}
	if msg != s.errorBannerMsg {
		s.errorBannerMsg = msg
		s.errorBannerExpanded = false
		s.errorBannerDismissed = false
	}
	if s.errorBannerDismissed {
		return tern.Node{}, false
	}
	headline := ""
	for line := range strings.SplitSeq(msg, "\n") {
		if t := strings.TrimSpace(line); t != "" {
			headline = t
			break
		}
	}
	if headline == "" {
		headline = "Error"
	}
	multiline := msg != headline
	var textNode tern.Node
	if s.errorBannerExpanded {
		textNode = tern.Node{
			Kind: tern.KindText,
			Props: map[string]any{
				"key":   "msg",
				"wrap":  "word",
				"grow":  1,
				"spans": []any{map[string]any{"t": msg, "s": "error"}},
			},
		}
	} else {
		textNode = tern.Node{
			Kind: tern.KindText,
			Props: map[string]any{
				"key":      "msg",
				"truncate": "end",
				"lines":    1,
				"grow":     1,
				"title":    msg,
				"spans":    []any{map[string]any{"t": headline, "s": "error"}},
			},
		}
	}
	children := []tern.Element{textNode}
	if multiline || len([]rune(headline)) > 80 {
		label := "Details"
		if s.errorBannerExpanded {
			label = "Less"
		}
		children = append(children, tern.Node{
			Kind: tern.KindButton,
			Props: map[string]any{
				"key":     "details",
				"role":    "omp.btn",
				"text":    label,
				"actions": map[string]any{"click": "details"},
				"title":   label,
			},
			Handlers: tern.Handlers{
				OnClick: func(*tern.Action) {
					s.toggleOmpErrorBanner(false)
				},
			},
		})
	}
	children = append(children, tern.Node{
		Kind: tern.KindButton,
		Props: map[string]any{
			"key":     "dismiss",
			"role":    "omp.btn",
			"text":    "Dismiss",
			"actions": map[string]any{"click": "dismiss"},
			"title":   "Dismiss",
		},
		Handlers: tern.Handlers{
			OnClick: func(*tern.Action) {
				s.toggleOmpErrorBanner(true)
			},
		},
	})
	align := "center"
	if s.errorBannerExpanded {
		align = "start"
	}
	return tern.Node{
		Kind: tern.KindRow,
		Props: map[string]any{
			"key":   "errorBanner",
			"role":  "omp.errorBanner",
			"tone":  "error",
			"gap":   "sm",
			"align": align,
		},
		Children: children,
	}, true
}

func (s *session) toggleOmpErrorBanner(dismiss bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.fellBack {
		return
	}
	if dismiss {
		s.errorBannerDismissed = true
	} else {
		s.errorBannerExpanded = !s.errorBannerExpanded
	}
	for _, e := range s.dock {
		if e.id == "dock" {
			s.rebuild(e, e.node, true)
			break
		}
	}
	_ = s.flushLocked()
}
