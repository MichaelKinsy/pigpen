// Package commit ports omp's selective-commit eval namespace and the change
// journal behind it.
//
// upstream: packages/coding-agent/src/commit/selective/attribution.ts,
//
//	crates/diff/src/lib.rs, crates/natives/src/diff.rs
package commit

import "math"

// Pseudo-actors that stand for "no recorded edit explains this".
// upstream: packages/coding-agent/src/commit/selective/attribution.ts:25-35
const (
	// PRE marks lines already in the file (relative to HEAD) before its first recorded edit.
	PRE = "~pre"
	// GAP marks lines changed between two recorded edits by nobody on record.
	GAP = "~gap"
	// TAIL marks lines changed after the last recorded edit by nobody on record.
	TAIL = "~tail"
	// UNRECORDED marks lines of a file no record covers at all.
	UNRECORDED = "~none"
	// REORDERED marks a net change whose ownership the edit replay cannot separate from already committed lines.
	REORDERED = "~reordered"

	actorHead = "~head"

	// pairingBudget is the largest HEAD-side * worktree-side item product a cancellation window aligns.
	pairingBudget = 250_000
)

// IsPseudoActor reports whether actor stands for "no recorded edit explains this".
// upstream: attribution.ts:40
func IsPseudoActor(actor string) bool {
	return len(actor) > 0 && actor[0] == '~'
}

// ToLines splits file content into lines; nil (absent file) has no lines, "" has one empty line.
// upstream: attribution.ts:45
func ToLines(text *string) []string {
	if text == nil {
		return nil
	}
	return splitLinesExact(*text)
}

// FromLines is the inverse of ToLines.
// upstream: attribution.ts:50
func FromLines(lines []string) *string {
	if len(lines) == 0 {
		return nil
	}
	s := joinLinesExact(lines)
	return &s
}

func splitLinesExact(text string) []string {
	count := 1
	for i := 0; i < len(text); i++ {
		if text[i] == '\n' {
			count++
		}
	}
	out := make([]string, 0, count)
	start := 0
	for i := 0; i < len(text); i++ {
		if text[i] == '\n' {
			out = append(out, text[start:i])
			start = i + 1
		}
	}
	out = append(out, text[start:])
	return out
}

func joinLinesExact(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	total := len(lines) - 1
	for _, l := range lines {
		total += len(l)
	}
	buf := make([]byte, 0, total)
	for i, l := range lines {
		if i > 0 {
			buf = append(buf, '\n')
		}
		buf = append(buf, l...)
	}
	return string(buf)
}

// Step is one content transition in a file's history. Before must equal the previous step's After.
// upstream: attribution.ts:55
type Step struct {
	Actor  string
	Before []string
	After  []string
}

// ChangeOp is a change line of the file's HEAD -> worktree difference.
// upstream: attribution.ts:63
type ChangeOp struct {
	ID                    int
	Kind                  string // "+" or "-"
	Text                  string
	By                    string
	Over                  []string
	PlacementDependencies []string // nil when absent; empty slice [] when ordered insertion at exact gap
}

// Hunk is a contiguous region of change lines between unchanged HEAD lines.
// upstream: attribution.ts:77
type Hunk struct {
	Ops       []*ChangeOp
	HeadStart int
	HeadCount int
	WorkStart int
	WorkCount int
}

type itemSpan struct {
	hasStart              bool
	start                 int
	hasEnd                bool
	end                   int
	hasPlacementDeps      bool
	placementDependencies []string
}

type lineItem struct {
	text      string
	alive     bool
	hasHead   bool
	head      int
	span      *itemSpan
	by        string
	over      []string
	deletedBy string
	hasOp     bool
	op        int
}

// Attribution is the result of Attribute: ordered change ops plus the line order needed to rebuild content.
// upstream: attribution.ts:105
type Attribution struct {
	Ops   []*ChangeOp
	Hunks []*Hunk
	items []*lineItem
}

// Region is a changed region between two line lists: a[I1, I2) became b[J1, J2).
// upstream: attribution.ts:112
type Region struct {
	I1 int
	I2 int
	J1 int
	J2 int
}

// RecordedEdit is one recorded edit of a file, as content snapshots.
// upstream: attribution.ts:124
type RecordedEdit struct {
	Actor  string
	Before []string
	After  []string
}

func linesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// HistorySteps chains a file's recorded edits into contiguous Steps from head to work,
// filling unexplained stretches with PRE, GAP, and TAIL steps. A file with no edits
// becomes one UNRECORDED step. Edits up to the latest recorded snapshot equal to head
// are skipped.
// upstream: attribution.ts:136
func HistorySteps(head []string, edits []RecordedEdit, work []string) []Step {
	var steps []Step
	current := head
	start := firstUncommitted(head, edits)
	for index := start; index < len(edits); index++ {
		edit := edits[index]
		if !linesEqual(current, edit.Before) {
			actor := GAP
			if index == 0 {
				actor = PRE
			}
			steps = append(steps, Step{Actor: actor, Before: current, After: edit.Before})
		}
		if !linesEqual(edit.Before, edit.After) {
			steps = append(steps, Step{Actor: edit.Actor, Before: edit.Before, After: edit.After})
		}
		current = edit.After
	}
	if !linesEqual(current, work) {
		actor := TAIL
		if len(edits) == 0 {
			actor = UNRECORDED
		}
		steps = append(steps, Step{Actor: actor, Before: current, After: work})
	}
	return steps
}

// firstUncommitted returns the index of the first edit past the latest recorded snapshot equal to head; 0 when none is.
// upstream: attribution.ts:155
func firstUncommitted(head []string, edits []RecordedEdit) int {
	for index := len(edits) - 1; index >= 0; index-- {
		if linesEqual(edits[index].After, head) {
			return index + 1
		}
		if linesEqual(edits[index].Before, head) {
			return index
		}
	}
	return 0
}

// --- Myers O(ND) diff matching crates/diff/src/lib.rs (jsdiff v9 tie-breaking) ---

type diffRun struct {
	count   uint32
	added   bool
	removed bool
}

const noComponent uint32 = math.MaxUint32

type componentNode struct {
	count   uint32
	prev    uint32
	refs    uint32
	added   bool
	removed bool
}

type componentArena struct {
	nodes    []componentNode
	freeHead uint32
}

func newComponentArena(capacity int) componentArena {
	return componentArena{
		nodes:    make([]componentNode, 0, capacity),
		freeHead: noComponent,
	}
}

func (a *componentArena) alloc(count, prev uint32, added, removed bool) uint32 {
	if a.freeHead == noComponent {
		idx := uint32(len(a.nodes))
		a.nodes = append(a.nodes, componentNode{count: count, prev: prev, refs: 1, added: added, removed: removed})
		return idx
	}
	idx := a.freeHead
	slot := &a.nodes[idx]
	a.freeHead = slot.prev
	*slot = componentNode{count: count, prev: prev, refs: 1, added: added, removed: removed}
	return idx
}

func (a *componentArena) cloneRef(idx uint32) uint32 {
	if idx != noComponent {
		a.nodes[idx].refs++
	}
	return idx
}

func (a *componentArena) release(idx uint32) {
	for idx != noComponent {
		node := &a.nodes[idx]
		node.refs--
		if node.refs > 0 {
			break
		}
		prev := node.prev
		node.prev = a.freeHead
		a.freeHead = idx
		idx = prev
	}
}

type pathState struct {
	valid  bool
	oldPos int
	last   uint32
}

func extractCommon(arena *componentArena, path *pathState, newIDs, oldIDs []uint32, diagonal int) int {
	newLen := len(newIDs)
	oldLen := len(oldIDs)
	oldPos := path.oldPos
	newPos := oldPos - diagonal
	var common uint32
	for newPos+1 < newLen && oldPos+1 < oldLen && oldIDs[oldPos+1] == newIDs[newPos+1] {
		newPos++
		oldPos++
		common++
	}
	if common > 0 {
		path.last = arena.alloc(common, path.last, false, false)
	}
	path.oldPos = oldPos
	return newPos
}

func addToPath(arena *componentArena, path pathState, added, removed bool, oldPosInc int) pathState {
	lastIdx := path.last
	var newLast uint32
	if lastIdx == noComponent {
		newLast = arena.alloc(1, noComponent, added, removed)
	} else {
		last := arena.nodes[lastIdx]
		if last.added == added && last.removed == removed {
			if last.refs == 1 {
				arena.nodes[lastIdx].count = last.count + 1
				newLast = lastIdx
			} else {
				arena.nodes[lastIdx].refs--
				prev := arena.cloneRef(last.prev)
				newLast = arena.alloc(last.count+1, prev, added, removed)
			}
		} else {
			newLast = arena.alloc(1, lastIdx, added, removed)
		}
	}
	return pathState{valid: true, oldPos: path.oldPos + oldPosInc, last: newLast}
}

func buildRuns(arena *componentArena, last uint32) []diffRun {
	var runs []diffRun
	for cursor := last; cursor != noComponent; {
		c := arena.nodes[cursor]
		runs = append(runs, diffRun{count: c.count, added: c.added, removed: c.removed})
		cursor = c.prev
	}
	for i, j := 0, len(runs)-1; i < j; i, j = i+1, j-1 {
		runs[i], runs[j] = runs[j], runs[i]
	}
	return runs
}

// internStrings assigns dense uint32 IDs under exact string equality.
// upstream: crates/diff/src/lib.rs:273
func internStrings(oldTokens, newTokens []string) ([]uint32, []uint32) {
	ids := make(map[string]uint32, len(oldTokens)+len(newTokens))
	oldIDs := make([]uint32, len(oldTokens))
	for i, tok := range oldTokens {
		id, ok := ids[tok]
		if !ok {
			id = uint32(len(ids))
			ids[tok] = id
		}
		oldIDs[i] = id
	}
	newIDs := make([]uint32, len(newTokens))
	for i, tok := range newTokens {
		id, ok := ids[tok]
		if !ok {
			id = uint32(len(ids))
			ids[tok] = id
		}
		newIDs[i] = id
	}
	return oldIDs, newIDs
}

// myersDiffWithin replicates crates/diff/src/lib.rs:188 (jsdiff v9 tie-breaking).
func myersDiffWithin(oldIDs, newIDs []uint32, maxEdits int) ([]diffRun, bool) {
	oldLen := len(oldIDs)
	newLen := len(newIDs)
	maxEdit := maxEdits
	if sum := oldLen + newLen; maxEdit > sum {
		maxEdit = sum
	}
	offset := maxEdit + 1
	best := make([]pathState, 2*maxEdit+3)
	capArena := oldLen + newLen
	if capArena < 16 {
		capArena = 16
	} else if capArena > 4096 {
		capArena = 4096
	}
	arena := newComponentArena(capArena)

	seed := pathState{valid: true, oldPos: -1, last: noComponent}
	seedNewPos := extractCommon(&arena, &seed, newIDs, oldIDs, 0)
	if seed.oldPos+1 >= oldLen && seedNewPos+1 >= newLen {
		return buildRuns(&arena, seed.last), true
	}
	best[offset] = seed

	minDiag := math.MinInt
	maxDiag := math.MaxInt
	for editLength := 1; editLength <= maxEdit; editLength++ {
		diagStart := -editLength
		if minDiag > diagStart {
			diagStart = minDiag
		}
		diagEnd := editLength
		if maxDiag < diagEnd {
			diagEnd = maxDiag
		}
		for diagonal := diagStart; diagonal <= diagEnd; diagonal += 2 {
			idx := diagonal + offset
			removePath := best[idx-1]
			best[idx-1] = pathState{}
			addPath := best[idx+1]

			canAdd := false
			if addPath.valid {
				addNewPos := addPath.oldPos - diagonal
				canAdd = addNewPos >= 0 && addNewPos < newLen
			}
			canRemove := removePath.valid && removePath.oldPos+1 < oldLen
			if !canAdd && !canRemove {
				if removePath.valid {
					arena.release(removePath.last)
				}
				if best[idx].valid {
					arena.release(best[idx].last)
					best[idx] = pathState{}
				}
				continue
			}

			var basePath pathState
			if !canRemove || (canAdd && removePath.valid && removePath.oldPos < addPath.oldPos) {
				if removePath.valid {
					arena.release(removePath.last)
				}
				cloned := pathState{valid: true, oldPos: addPath.oldPos, last: arena.cloneRef(addPath.last)}
				basePath = addToPath(&arena, cloned, true, false, 0)
			} else {
				basePath = addToPath(&arena, removePath, false, true, 1)
			}
			newPos := extractCommon(&arena, &basePath, newIDs, oldIDs, diagonal)
			if basePath.oldPos+1 >= oldLen && newPos+1 >= newLen {
				return buildRuns(&arena, basePath.last), true
			}
			if basePath.oldPos+1 >= oldLen && diagonal-1 < maxDiag {
				maxDiag = diagonal - 1
			}
			if newPos+1 >= newLen && diagonal+1 > minDiag {
				minDiag = diagonal + 1
			}
			if best[idx].valid {
				arena.release(best[idx].last)
			}
			best[idx] = basePath
		}
	}
	return nil, false
}

func diffTokenRuns(a, b []string) []diffRun {
	oldIDs, newIDs := internStrings(a, b)
	runs, _ := myersDiffWithin(oldIDs, newIDs, len(oldIDs)+len(newIDs))
	return runs
}

// ChangedRegions returns changed regions between two line lists, in order (equal runs omitted).
// upstream: attribution.ts:164
func ChangedRegions(a, b []string) []Region {
	if regions, ok := emptySideRegions(a, b); ok {
		return regions
	}
	return runRegions(diffTokenRuns(a, b))
}

// ChangedRegionsWithin returns ChangedRegions when the lists differ by at most maxEdits
// added or removed lines; (nil, false) for a larger rewrite.
// upstream: attribution.ts:172
func ChangedRegionsWithin(a, b []string, maxEdits int) ([]Region, bool) {
	if regions, ok := emptySideRegions(a, b); ok {
		return regions, true
	}
	oldIDs, newIDs := internStrings(a, b)
	runs, ok := myersDiffWithin(oldIDs, newIDs, maxEdits)
	if !ok {
		return nil, false
	}
	return runRegions(runs), true
}

func emptySideRegions(a, b []string) ([]Region, bool) {
	if len(a) > 0 && len(b) > 0 {
		return nil, false
	}
	if len(a) == 0 && len(b) == 0 {
		return nil, true
	}
	return []Region{{I1: 0, I2: len(a), J1: 0, J2: len(b)}}, true
}

func runRegions(runs []diffRun) []Region {
	var regions []Region
	i, j := 0, 0
	var open *Region
	for _, run := range runs {
		if !run.added && !run.removed {
			if open != nil {
				regions = append(regions, *open)
				open = nil
			}
			i += int(run.count)
			j += int(run.count)
			continue
		}
		if open == nil {
			open = &Region{I1: i, I2: i, J1: j, J2: j}
		}
		if run.removed {
			i += int(run.count)
			open.I2 = i
		} else {
			j += int(run.count)
			open.J2 = j
		}
	}
	if open != nil {
		regions = append(regions, *open)
	}
	return regions
}

type replayRegion struct {
	Region
	leftAt  int
	rightAt int
}

// replayRegions replays a boundary insertion around any HEAD lines already committed there.
// upstream: attribution.ts:216
func replayRegions(items []*lineItem, aliveAt []int, headLength int, step Step) []replayRegion {
	var out []replayRegion
	for _, region := range ChangedRegions(step.Before, step.After) {
		leftAt := -1
		if region.I1 > 0 {
			leftAt = aliveAt[region.I1-1]
		}
		rightAt := len(items)
		if region.I2 < len(aliveAt) {
			rightAt = aliveAt[region.I2]
		}
		next := region.J1
		var left, right *lineItem
		if leftAt >= 0 && leftAt < len(items) {
			left = items[leftAt]
		}
		if rightAt >= 0 && rightAt < len(items) {
			right = items[rightAt]
		}
		if region.I1 == region.I2 && ((left != nil && left.span != nil) || (right != nil && right.span != nil)) {
			start, hasStart := headEnd(left)
			end, hasEnd := headStart(right, headLength)
			var deleted []int
			if hasStart && hasEnd && start < end {
				for idx := leftAt + 1; idx < rightAt; idx++ {
					it := items[idx]
					if it.hasHead && it.head >= start && it.head < end && (it.deletedBy == PRE || it.deletedBy == GAP) {
						deleted = append(deleted, idx)
					}
				}
			}
			if len(deleted) > 0 && len(deleted)*(region.J2-next) <= pairingBudget {
				oldTexts := make([]string, len(deleted))
				for k, idx := range deleted {
					oldTexts[k] = items[idx].text
				}
				inserted := step.After[next:region.J2]
				i := 0
				j := next
				for _, run := range diffTokenRuns(oldTexts, inserted) {
					cnt := int(run.count)
					if run.removed {
						i += cnt
					} else if run.added {
						j += cnt
					} else {
						if next < j {
							r := region
							r.J1 = next
							r.J2 = j
							out = append(out, replayRegion{Region: r, leftAt: leftAt, rightAt: deleted[i]})
						}
						stop := i + cnt
						for i < stop {
							items[deleted[i]].alive = true
							i++
						}
						leftAt = deleted[i-1]
						j += cnt
						next = j
					}
				}
			}
		}
		if next < region.J2 || region.I1 < region.I2 {
			r := region
			r.J1 = next
			out = append(out, replayRegion{Region: r, leftAt: leftAt, rightAt: rightAt})
		}
	}
	return out
}

func headEnd(left *lineItem) (int, bool) {
	if left == nil {
		return 0, true
	}
	if left.hasHead {
		return left.head + 1, true
	}
	if left.span != nil && left.span.hasEnd {
		return left.span.end, true
	}
	return 0, false
}

func headStart(right *lineItem, headLength int) (int, bool) {
	if right == nil {
		return headLength, true
	}
	if right.hasHead {
		return right.head, true
	}
	if right.span != nil && right.span.hasStart {
		return right.span.start, true
	}
	return 0, false
}

// Attribute replays steps over head and derives the HEAD -> work change ops with their actors.
// upstream: attribution.ts:280
func Attribute(head []string, steps []Step, work []string, headMoved bool) (*Attribution, error) {
	items := make([]*lineItem, len(head))
	for idx, text := range head {
		items[idx] = &lineItem{
			text:    text,
			alive:   true,
			hasHead: true,
			head:    idx,
			by:      actorHead,
		}
	}
	for stepIndex, step := range steps {
		aliveAt := make([]int, 0, len(step.Before))
		for idx, it := range items {
			if it.alive {
				aliveAt = append(aliveAt, idx)
			}
		}
		mismatch := len(aliveAt) != len(step.Before)
		if !mismatch {
			for n, idx := range aliveAt {
				if items[idx].text != step.Before[n] {
					mismatch = true
					break
				}
			}
		}
		if mismatch {
			return nil, &attributionError{msg: "attribution chain broken at step " + itoa(stepIndex) + " (" + step.Actor + ")"}
		}

		type insertion struct {
			at    int
			items []*lineItem
		}
		var insertions []insertion

		for _, region := range replayRegions(items, aliveAt, len(head), step) {
			overSet := make(map[string]struct{})
			var overOrder []string
			var depSet map[string]struct{}
			var depOrder []string
			orderedInsertion := region.I1 < region.I2
			for n := region.I1; n < region.I2; n++ {
				it := items[aliveAt[n]]
				if it.span != nil && it.span.hasPlacementDeps {
					for _, actor := range it.span.placementDependencies {
						if depSet == nil {
							depSet = make(map[string]struct{})
						}
						if _, ok := depSet[actor]; !ok {
							depSet[actor] = struct{}{}
							depOrder = append(depOrder, actor)
						}
					}
				} else {
					orderedInsertion = false
				}
				it.alive = false
				it.deletedBy = step.Actor
				if _, ok := overSet[it.by]; !ok {
					overSet[it.by] = struct{}{}
					overOrder = append(overOrder, it.by)
				}
			}
			if region.J2 == region.J1 {
				continue
			}
			at := region.rightAt
			var hasStart, hasEnd bool
			var start, end int
			if region.I1 < region.I2 {
				first := items[aliveAt[region.I1]]
				last := items[aliveAt[region.I2-1]]
				if first.hasHead {
					hasStart, start = true, first.head
				} else if first.span != nil && first.span.hasStart {
					hasStart, start = true, first.span.start
				}
				if last.hasHead {
					hasEnd, end = true, last.head+1
				} else if last.span != nil && last.span.hasEnd {
					hasEnd, end = true, last.span.end
				}
				at = aliveAt[region.I2-1] + 1
			} else {
				leftAt := region.leftAt
				var left, right *lineItem
				if leftAt >= 0 && leftAt < len(items) {
					left = items[leftAt]
				}
				if at >= 0 && at < len(items) {
					right = items[at]
				}
				if (left != nil && left.span != nil) || (right != nil && right.span != nil) {
					orderedInsertion = true
					leftEnd, hasLeftEnd := headEnd(left)
					rightStart, hasRightStart := headStart(right, len(head))
					if hasLeftEnd && hasRightStart && leftEnd == rightStart {
						hasStart, start = true, leftEnd
						hasEnd, end = true, leftEnd
						for idx := leftAt + 1; idx < at; idx++ {
							if items[idx].hasHead && items[idx].head >= leftEnd {
								at = idx
								break
							}
						}
					} else {
						depSet = make(map[string]struct{})
						depOrder = nil
						for _, neighbour := range []*lineItem{left, right} {
							if neighbour == nil || neighbour.span == nil {
								continue
							}
							if _, ok := depSet[neighbour.by]; !ok {
								depSet[neighbour.by] = struct{}{}
								depOrder = append(depOrder, neighbour.by)
							}
							for _, actor := range neighbour.span.placementDependencies {
								if _, ok := depSet[actor]; !ok {
									depSet[actor] = struct{}{}
									depOrder = append(depOrder, actor)
								}
							}
						}
						if hasRightStart && hasLeftEnd {
							hasStart, hasEnd = true, true
							if rightStart < leftEnd {
								start, end = rightStart, leftEnd
							} else {
								start, end = leftEnd, rightStart
							}
						}
					}
				}
			}

			var hasPlacementDeps bool
			var placementDeps []string
			if depSet != nil {
				hasPlacementDeps = true
				placementDeps = depOrder
			} else if orderedInsertion {
				hasPlacementDeps = true
				placementDeps = []string{}
			}
			var span *itemSpan
			if hasPlacementDeps || (hasStart && hasEnd) {
				span = &itemSpan{
					hasStart:              hasStart,
					start:                 start,
					hasEnd:                hasEnd,
					end:                   end,
					hasPlacementDeps:      hasPlacementDeps,
					placementDependencies: placementDeps,
				}
			}
			added := make([]*lineItem, region.J2-region.J1)
			for k := region.J1; k < region.J2; k++ {
				added[k-region.J1] = &lineItem{
					text:  step.After[k],
					alive: true,
					by:    step.Actor,
					over:  overOrder,
					span:  span,
				}
			}
			insertions = append(insertions, insertion{at: at, items: added})
		}

		// Splice back to front.
		for n := len(insertions) - 1; n >= 0; n-- {
			ins := insertions[n]
			pos := ins.at
			tail := make([]*lineItem, len(items)-pos)
			copy(tail, items[pos:])
			items = append(items[:pos], ins.items...)
			items = append(items, tail...)
		}
		items = cancelReinsertions(items)
	}

	alive := make([]string, 0, len(work))
	for _, it := range items {
		if it.alive {
			alive = append(alive, it.text)
		}
	}
	if !linesEqual(alive, work) {
		return nil, &attributionError{msg: "attribution chain does not end at the worktree content"}
	}
	replay := deriveOps(items)
	diffLen := len(work) - len(head)
	if diffLen < 0 {
		diffLen = -diffLen
	}
	if !headMoved || len(steps) == 0 || steps[0].Actor != PRE || len(replay.Ops) == diffLen {
		return replay, nil
	}
	return reconcileNetHunks(head, work, replay, ChangedRegions(head, work)), nil
}

type attributionError struct{ msg string }

func (e *attributionError) Error() string { return e.msg }

// reconcileNetHunks keeps a replayed hunk only where it spans exactly a net HEAD -> worktree hunk
// and adds no line a disputed hunk deletes (or deletes one it adds).
// upstream: attribution.ts:389
func reconcileNetHunks(head, work []string, replay *Attribution, regions []Region) *Attribution {
	type rangeKey struct{ headStart, workStart int }
	byRange := make(map[rangeKey]*Hunk, len(replay.Hunks))
	for _, h := range replay.Hunks {
		byRange[rangeKey{h.HeadStart, h.WorkStart}] = h
	}
	kept := make([]*Hunk, len(regions))
	allKept := len(regions) == len(replay.Hunks)
	for n, r := range regions {
		h := byRange[rangeKey{r.I1, r.J1}]
		if h != nil && h.HeadCount == r.I2-r.I1 && h.WorkCount == r.J2-r.J1 {
			kept[n] = h
		} else {
			allKept = false
		}
	}
	if allKept {
		return replay
	}

	disputedRemoved := make(map[string]struct{})
	disputedAdded := make(map[string]struct{})
	dispute := func(r Region) {
		for i := r.I1; i < r.I2; i++ {
			disputedRemoved[head[i]] = struct{}{}
		}
		for j := r.J1; j < r.J2; j++ {
			disputedAdded[work[j]] = struct{}{}
		}
	}
	for n, r := range regions {
		if kept[n] == nil {
			dispute(r)
		}
	}
	for grew := true; grew; {
		grew = false
		for n, r := range regions {
			h := kept[n]
			if h == nil {
				continue
			}
			for _, op := range h.Ops {
				var set map[string]struct{}
				if op.Kind == "+" {
					set = disputedRemoved
				} else {
					set = disputedAdded
				}
				if _, ok := set[op.Text]; ok {
					kept[n] = nil
					dispute(r)
					grew = true
					break
				}
			}
		}
	}

	sources := make([]*lineItem, 0, len(replay.Ops))
	for _, it := range replay.items {
		if it.hasOp {
			sources = append(sources, it)
		}
	}
	var result []*lineItem
	i := 0
	for n, r := range regions {
		for ; i < r.I1; i++ {
			result = append(result, &lineItem{by: actorHead, text: head[i], hasHead: true, head: i, alive: true})
		}
		if h := kept[n]; h != nil {
			for _, op := range h.Ops {
				// Copy item so op id is cleanly reassigned by deriveOps.
				cp := *sources[op.ID]
				cp.hasOp = false
				cp.op = 0
				result = append(result, &cp)
			}
			i = r.I2
			continue
		}
		for ; i < r.I2; i++ {
			result = append(result, &lineItem{by: actorHead, text: head[i], hasHead: true, head: i, alive: false, deletedBy: REORDERED})
		}
		for j := r.J1; j < r.J2; j++ {
			result = append(result, &lineItem{by: REORDERED, text: work[j], alive: true})
		}
	}
	for ; i < len(head); i++ {
		result = append(result, &lineItem{by: actorHead, text: head[i], hasHead: true, head: i, alive: true})
	}
	return deriveOps(result)
}

func isAnchor(it *lineItem) bool {
	return it.alive && it.hasHead
}

func isOrderedInsertion(it *lineItem) bool {
	return it.alive && it.span != nil && it.span.hasPlacementDeps
}

func regionEnd(items []*lineItem, start int) int {
	end := start
	for end < len(items) && !isAnchor(items[end]) && !isOrderedInsertion(items[end]) {
		end++
	}
	return end
}

type windowTally struct {
	head  int
	work  int
	texts map[string]struct{}
}

func tallyItems(items []*lineItem, start, end int) windowTally {
	t := windowTally{texts: make(map[string]struct{})}
	for idx := start; idx < end; idx++ {
		it := items[idx]
		if it.hasHead {
			t.head++
		}
		if it.alive {
			t.work++
		}
		if it.hasHead || it.alive {
			t.texts[it.text] = struct{}{}
		}
	}
	return t
}

// cancelReinsertions turns a deleted HEAD line and an identical re-inserted line back into an unchanged HEAD line.
// upstream: attribution.ts:462
func cancelReinsertions(items []*lineItem) []*lineItem {
	out := make([]*lineItem, 0, len(items))
	index := 0
	for index < len(items) {
		if isAnchor(items[index]) || isOrderedInsertion(items[index]) {
			out = append(out, items[index])
			index++
			continue
		}
		start := index
		end := regionEnd(items, start)
		window := tallyItems(items, start, end)
		for end < len(items) {
			next := end
			for next < len(items) && isAnchor(items[next]) {
				next++
			}
			if next == len(items) || isOrderedInsertion(items[next]) {
				break
			}
			nextEnd := regionEnd(items, next)
			reg := tallyItems(items, next, nextEnd)
			_, hasFirst := window.texts[items[end].text]
			_, hasLast := reg.texts[items[next-1].text]
			if !hasFirst || !hasLast {
				break
			}
			anchors := next - end
			headCount := window.head + anchors + reg.head
			workCount := window.work + anchors + reg.work
			if headCount*workCount > pairingBudget {
				break
			}
			window.head = headCount
			window.work = workCount
			for n := end; n < next; n++ {
				window.texts[items[n].text] = struct{}{}
			}
			for text := range reg.texts {
				window.texts[text] = struct{}{}
			}
			end = nextEnd
		}
		out = settleWindow(items, start, end, out)
		index = end
	}
	return out
}

// settleWindow appends items[start, end) to out with paired deletions/insertions restored as HEAD anchors.
// upstream: attribution.ts:530
func settleWindow(items []*lineItem, start, end int, out []*lineItem) []*lineItem {
	var headSide []*lineItem
	var workSide []*lineItem
	deleted := false
	inserted := false
	for idx := start; idx < end; idx++ {
		it := items[idx]
		if it.hasHead {
			headSide = append(headSide, it)
			if !it.alive {
				deleted = true
			}
		}
		if it.alive {
			workSide = append(workSide, it)
			if !it.hasHead {
				inserted = true
			}
		}
	}
	var pairs [][2]*lineItem
	if deleted && inserted && len(headSide)*len(workSide) <= pairingBudget {
		pairs = alignSides(headSide, workSide)
	}
	if len(pairs) == 0 {
		return append(out, items[start:end]...)
	}
	segments := make([][]*lineItem, len(pairs)+1)
	headAt := 0
	workAt := 0
	for idx := start; idx < end; idx++ {
		it := items[idx]
		if it.hasHead {
			if headAt < len(pairs) && pairs[headAt][0] == it {
				headAt++
			} else {
				segments[headAt] = append(segments[headAt], it)
			}
		}
		if it.alive {
			if workAt < len(pairs) && pairs[workAt][1] == it {
				workAt++
			} else if !it.hasHead {
				segments[workAt] = append(segments[workAt], it)
			}
		} else if !it.hasHead {
			segments[workAt] = append(segments[workAt], it)
		}
	}
	heads := make([]int, len(pairs))
	for n, p := range pairs {
		heads[n] = p[0].head
	}
	for n, p := range pairs {
		out = append(out, segments[n]...)
		add := p[1]
		add.hasHead = true
		add.head = heads[n]
		add.span = nil
		add.by = actorHead
		out = append(out, add)
	}
	return append(out, segments[len(pairs)]...)
}

// alignSides computes order-preserving pairs of equal-text HEAD-side and worktree-side items.
// upstream: attribution.ts:587
func alignSides(head, work []*lineItem) [][2]*lineItem {
	hLen := len(head)
	wLen := len(work)
	anchorWeight := int32(hLen)
	if wLen < hLen {
		anchorWeight = int32(wLen)
	}
	anchorWeight++

	weight := func(a, b *lineItem) int32 {
		w := int32(1)
		if a.alive {
			w += anchorWeight
		}
		if b.hasHead {
			w += anchorWeight
		}
		return w
	}

	stride := wLen + 1
	table := make([]int32, (hLen+1)*stride)
	for i := hLen - 1; i >= 0; i-- {
		row := i * stride
		nextRow := (i + 1) * stride
		for j := wLen - 1; j >= 0; j-- {
			skip := table[nextRow+j]
			if right := table[row+j+1]; right > skip {
				skip = right
			}
			best := skip
			if head[i].text == work[j].text {
				if cand := table[nextRow+j+1] + weight(head[i], work[j]); cand > best {
					best = cand
				}
			}
			table[row+j] = best
		}
	}

	var pairs [][2]*lineItem
	i, j := 0, 0
	for i < hLen && j < wLen {
		if head[i].text == work[j].text && table[i*stride+j] == table[(i+1)*stride+j+1]+weight(head[i], work[j]) {
			pairs = append(pairs, [2]*lineItem{head[i], work[j]})
			i++
			j++
		} else if table[(i+1)*stride+j] >= table[i*stride+j+1] {
			i++
		} else {
			j++
		}
	}
	return pairs
}

func deriveOps(items []*lineItem) *Attribution {
	var ops []*ChangeOp
	var hunks []*Hunk
	headLine := 0
	workLine := 0
	var hunk *Hunk
	for _, it := range items {
		anchor := isAnchor(it)
		deletedHead := !it.alive && it.hasHead
		inserted := it.alive && !it.hasHead
		if anchor {
			hunk = nil
			headLine++
			workLine++
			continue
		}
		if !deletedHead && !inserted {
			continue
		}
		if hunk == nil {
			hunk = &Hunk{HeadStart: headLine, WorkStart: workLine}
			hunks = append(hunks, hunk)
		}
		var op *ChangeOp
		if deletedHead {
			by := it.deletedBy
			if by == "" {
				by = UNRECORDED
			}
			op = &ChangeOp{
				ID:   len(ops),
				Kind: "-",
				Text: it.text,
				By:   by,
			}
		} else {
			var placementDeps []string
			if it.span != nil && it.span.hasPlacementDeps {
				placementDeps = it.span.placementDependencies
			}
			op = &ChangeOp{
				ID:                    len(ops),
				Kind:                  "+",
				Text:                  it.text,
				By:                    it.by,
				Over:                  it.over,
				PlacementDependencies: placementDeps,
			}
		}
		it.hasOp = true
		it.op = op.ID
		ops = append(ops, op)
		hunk.Ops = append(hunk.Ops, op)
		if deletedHead {
			headLine++
			hunk.HeadCount++
		} else {
			workLine++
			hunk.WorkCount++
		}
	}
	return &Attribution{Ops: ops, Hunks: hunks, items: items}
}

// BuildContent returns the lines of HEAD with only the selected change ops applied, in recorded line order.
// upstream: attribution.ts:664
func BuildContent(attribution *Attribution, selected map[int]bool) []string {
	var lines []string
	for _, it := range attribution.items {
		if !it.hasOp {
			if it.alive && it.hasHead {
				lines = append(lines, it.text)
			}
			continue
		}
		isSelected := selected[it.op]
		if (it.alive && isSelected) || (!it.alive && !isSelected) {
			lines = append(lines, it.text)
		}
	}
	return lines
}

// OpStatus is the ownership verdict for one change op relative to a scope.
// upstream: attribution.ts:679
type OpStatus string

const (
	OpOwn    OpStatus = "own"
	OpOther  OpStatus = "other"
	OpReview OpStatus = "review"
)

// ReviewReason explains why an op is not plainly own/other.
// upstream: attribution.ts:682
type ReviewReason struct {
	Kind string   // "reordered" | "entangled" | "placement" | "rewritten" | "hunk"
	With []string // for "entangled" and "placement"
	By   string   // for "rewritten"
}

// Classification holds per-op verdicts of Classify.
// upstream: attribution.ts:694
type Classification struct {
	Status  []OpStatus
	Reasons map[int]ReviewReason
}

// Classify decides which change ops belong to the agents in scope.
// upstream: attribution.ts:709
func Classify(attribution *Attribution, scope map[string]bool) *Classification {
	status := make([]OpStatus, len(attribution.Ops))
	for i := range status {
		status[i] = OpOther
	}
	reasons := make(map[int]ReviewReason)
	inScope := func(actor string) bool { return scope[actor] }

	for _, hunk := range attribution.Hunks {
		review := false
		for _, op := range hunk.Ops {
			if op.By == REORDERED {
				status[op.ID] = OpReview
				reasons[op.ID] = ReviewReason{Kind: "reordered"}
				review = true
				continue
			}
			if inScope(op.By) && len(op.PlacementDependencies) > 0 {
				var missing []string
				for _, actor := range op.PlacementDependencies {
					if !inScope(actor) {
						missing = append(missing, actor)
					}
				}
				if len(missing) > 0 {
					status[op.ID] = OpReview
					reasons[op.ID] = ReviewReason{Kind: "placement", With: missing}
					review = true
					continue
				}
			}
			if inScope(op.By) {
				var foreign []string
				seenForeign := make(map[string]struct{})
				for _, actor := range op.Over {
					if actor != actorHead && !inScope(actor) {
						if _, ok := seenForeign[actor]; !ok {
							seenForeign[actor] = struct{}{}
							foreign = append(foreign, actor)
						}
					}
				}
				if len(foreign) > 0 {
					status[op.ID] = OpReview
					reasons[op.ID] = ReviewReason{Kind: "entangled", With: foreign}
					review = true
				} else {
					status[op.ID] = OpOwn
				}
				continue
			}
			if op.Kind == "+" {
				overInScope := false
				for _, actor := range op.Over {
					if inScope(actor) {
						overInScope = true
						break
					}
				}
				if overInScope {
					status[op.ID] = OpReview
					reasons[op.ID] = ReviewReason{Kind: "rewritten", By: op.By}
					review = true
				}
			}
		}
		if !review {
			continue
		}
		for _, op := range hunk.Ops {
			if status[op.ID] == OpReview {
				continue
			}
			status[op.ID] = OpReview
			reasons[op.ID] = ReviewReason{Kind: "hunk"}
		}
	}
	return &Classification{Status: status, Reasons: reasons}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
