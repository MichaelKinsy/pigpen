package ts

// Port of lib/src/stack.c: the graph-structured parse stack. Reference counts and node pooling are
// the garbage collector's.

const (
	maxLinkCount     = 8
	maxIteratorCount = 64
	stackVersionNone = -1
)

type stackLink struct {
	node      *stackNode
	subtree   *subtree
	isPending bool
}

type stackNode struct {
	state             uint16
	position          Length
	// links are the paths back through the stack (at most maxLinkCount). The first lives in the
	// node's own allocation: nearly every node has exactly one.
	links             []stackLink
	first             [1]stackLink
	errorCost         uint32
	nodeCount         uint32
	dynamicPrecedence int32
}

type stackSummaryEntry struct {
	position Length
	depth    uint32
	state    uint16
}

type stackStatus int

const (
	stackActive stackStatus = iota
	stackPaused
	stackHalted
)

type stackHead struct {
	node                 *stackNode
	summary              []stackSummaryEntry
	hasSummary           bool
	nodeCountAtLastError uint32
	lastExternalToken    *subtree
	lookaheadWhenPaused  *subtree
	status               stackStatus
}

type stackSlice struct {
	subtrees []*subtree
	version  int
}

type stackIterator struct {
	node         *stackNode
	subtrees     []*subtree
	subtreeCount uint32
	isPending    bool
}

type stack struct {
	heads    []stackHead
	slices   []stackSlice
	baseNode *stackNode
}

const (
	actionNone = 0
	actionStop = 1
	actionPop  = 2
)

// stackSubtreeNodeCount is stack__subtree_node_count.
func stackSubtreeNodeCount(s *subtree) uint32 {
	count := s.visibleDescendants()
	if s.visible {
		count++
	}
	// Count intermediate error nodes even though they are not visible, because a stack version's
	// node count is used to check whether it has made any progress since the last error.
	if s.symbol == builtinSymErrorRepeat {
		count++
	}
	return count
}

// newStackNode is stack_node_new.
func newStackNode(previous *stackNode, st *subtree, isPending bool, state uint16) *stackNode {
	n := &stackNode{state: state}
	if previous != nil {
		n.first[0] = stackLink{previous, st, isPending}
		n.links = n.first[:1]
		n.position = previous.position
		n.errorCost = previous.errorCost
		n.dynamicPrecedence = previous.dynamicPrecedence
		n.nodeCount = previous.nodeCount
		if st != nil {
			n.errorCost += st.errorCostOf()
			n.position = lengthAdd(n.position, st.totalSize())
			n.nodeCount += stackSubtreeNodeCount(st)
			n.dynamicPrecedence += st.dynamicPrec()
		}
	}
	return n
}

// subtreeIsEquivalent is stack__subtree_is_equivalent.
func subtreeIsEquivalent(left, right *subtree) bool {
	if left == right {
		return true
	}
	if left == nil || right == nil {
		return false
	}
	if left.symbol != right.symbol {
		return false
	}
	// If both have errors, don't bother keeping both.
	if left.errorCostOf() > 0 && right.errorCostOf() > 0 {
		return true
	}
	return left.padding.bytes == right.padding.bytes &&
		left.size.bytes == right.size.bytes &&
		left.childCount() == right.childCount() &&
		left.extra == right.extra &&
		externalStateEqual(left, right)
}

// addLink is stack_node_add_link.
func (n *stackNode) addLink(link stackLink) {
	if link.node == n {
		return
	}
	for i := 0; i < len(n.links); i++ {
		existing := &n.links[i]
		if subtreeIsEquivalent(existing.subtree, link.subtree) {
			// In general, ambiguities are preserved until they are removed from the stack during a
			// pop where multiple paths lead to the same node. But when two links directly connect
			// the same pair of nodes, the ambiguity can be removed ahead of time.
			if existing.node == link.node {
				if link.subtree.dynamicPrecOrZero() > existing.subtree.dynamicPrecOrZero() {
					existing.subtree = link.subtree
					n.dynamicPrecedence = link.node.dynamicPrecedence + link.subtree.dynamicPrecOrZero()
				}
				return
			}

			// If the previous nodes are mergeable, merge them recursively.
			if existing.node.state == link.node.state &&
				existing.node.position.bytes == link.node.position.bytes &&
				existing.node.errorCost == link.node.errorCost {
				for j := 0; j < len(link.node.links); j++ {
					existing.node.addLink(link.node.links[j])
				}
				dp := link.node.dynamicPrecedence
				if link.subtree != nil {
					dp += link.subtree.dynamicPrec()
				}
				if dp > n.dynamicPrecedence {
					n.dynamicPrecedence = dp
				}
				return
			}
		}
	}

	if len(n.links) == maxLinkCount {
		return
	}

	nodeCount := link.node.nodeCount
	dp := link.node.dynamicPrecedence
	n.links = append(n.links, link)

	if link.subtree != nil {
		nodeCount += stackSubtreeNodeCount(link.subtree)
		dp += link.subtree.dynamicPrec()
	}
	if nodeCount > n.nodeCount {
		n.nodeCount = nodeCount
	}
	if dp > n.dynamicPrecedence {
		n.dynamicPrecedence = dp
	}
}

// dynamicPrecOrZero is ts_subtree_dynamic_precedence of a possibly-nil subtree; the C reads a
// NULL_SUBTREE's fields only where the link has a subtree.
func (s *subtree) dynamicPrecOrZero() int32 {
	if s == nil {
		return 0
	}
	return s.dynamicPrec()
}

func newStack() *stack {
	s := &stack{}
	s.baseNode = newStackNode(nil, nil, false, 1)
	s.clear()
	return s
}

// clear is ts_stack_clear.
func (s *stack) clear() {
	s.heads = s.heads[:0]
	s.heads = append(s.heads, stackHead{node: s.baseNode, status: stackActive})
}

func (s *stack) versionCount() int { return len(s.heads) }

func (s *stack) haltedVersionCount() int {
	n := 0
	for i := range s.heads {
		if s.heads[i].status == stackHalted {
			n++
		}
	}
	return n
}

func (s *stack) state(v int) uint16                         { return s.heads[v].node.state }
func (s *stack) position(v int) Length                      { return s.heads[v].node.position }
func (s *stack) lastExternalToken(v int) *subtree           { return s.heads[v].lastExternalToken }
func (s *stack) setLastExternalToken(v int, token *subtree) { s.heads[v].lastExternalToken = token }

// errorCost is ts_stack_error_cost.
func (s *stack) errorCost(v int) uint32 {
	head := &s.heads[v]
	result := head.node.errorCost
	if head.status == stackPaused || (head.node.state == errorState && (len(head.node.links) == 0 || head.node.links[0].subtree == nil)) {
		result += errorCostPerRecovery
	}
	return result
}

// nodeCountSinceError is ts_stack_node_count_since_error.
func (s *stack) nodeCountSinceError(v int) uint32 {
	head := &s.heads[v]
	if head.node.nodeCount < head.nodeCountAtLastError {
		head.nodeCountAtLastError = head.node.nodeCount
	}
	return head.node.nodeCount - head.nodeCountAtLastError
}

// push is ts_stack_push.
func (s *stack) push(v int, st *subtree, pending bool, state uint16) {
	head := &s.heads[v]
	n := newStackNode(head.node, st, pending, state)
	if st == nil {
		head.nodeCountAtLastError = n.nodeCount
	}
	head.node = n
}

// addVersion is ts_stack__add_version.
func (s *stack) addVersion(original int, node *stackNode) int {
	head := stackHead{
		node:                 node,
		nodeCountAtLastError: s.heads[original].nodeCountAtLastError,
		lastExternalToken:    s.heads[original].lastExternalToken,
		status:               stackActive,
	}
	s.heads = append(s.heads, head)
	return len(s.heads) - 1
}

// addSlice is ts_stack__add_slice.
func (s *stack) addSlice(original int, node *stackNode, subtrees []*subtree) {
	for i := len(s.slices) - 1; i >= 0; i-- {
		version := s.slices[i].version
		if s.heads[version].node == node {
			s.slices = append(s.slices, stackSlice{})
			copy(s.slices[i+2:], s.slices[i+1:])
			s.slices[i+1] = stackSlice{subtrees, version}
			return
		}
	}
	version := s.addVersion(original, node)
	s.slices = append(s.slices, stackSlice{subtrees, version})
}

// iterate is stack__iter. callback decides, per iterator, whether to pop and/or stop.
func (s *stack) iterate(version int, callback func(it *stackIterator) int, goalSubtreeCount int) []stackSlice {
	s.slices = s.slices[:0]
	iterators := []stackIterator{{node: s.heads[version].node, isPending: true}}
	includeSubtrees := goalSubtreeCount >= 0

	for len(iterators) > 0 {
		for i, size := 0, len(iterators); i < size; i++ {
			it := &iterators[i]
			node := it.node

			action := callback(it)
			shouldPop := action&actionPop != 0
			shouldStop := action&actionStop != 0 || len(node.links) == 0

			if shouldPop {
				subtrees := it.subtrees
				if !shouldStop {
					subtrees = append([]*subtree(nil), subtrees...)
				}
				// ts_subtree_array_reverse
				for a, b := 0, len(subtrees)-1; a < b; a, b = a+1, b-1 {
					subtrees[a], subtrees[b] = subtrees[b], subtrees[a]
				}
				s.addSlice(version, node, subtrees)
			}

			if shouldStop {
				iterators = append(iterators[:i], iterators[i+1:]...)
				i--
				size--
				continue
			}

			for j := 1; j <= len(node.links); j++ {
				var next *stackIterator
				var link stackLink
				if j == len(node.links) {
					link = node.links[0]
					next = &iterators[i]
				} else {
					if len(iterators) >= maxIteratorCount {
						continue
					}
					link = node.links[j]
					current := iterators[i]
					current.subtrees = append([]*subtree(nil), current.subtrees...)
					iterators = append(iterators, current)
					next = &iterators[len(iterators)-1]
				}

				next.node = link.node
				if link.subtree != nil {
					if includeSubtrees {
						next.subtrees = append(next.subtrees, link.subtree)
					}
					if !link.subtree.extra {
						next.subtreeCount++
						if !link.isPending {
							next.isPending = false
						}
					}
				} else {
					next.subtreeCount++
					next.isPending = false
				}
			}
		}
	}
	return append([]stackSlice(nil), s.slices...)
}

// popCount is ts_stack_pop_count.
func (s *stack) popCount(version int, count uint32) []stackSlice {
	return s.iterate(version, func(it *stackIterator) int {
		if it.subtreeCount == count {
			return actionPop | actionStop
		}
		return actionNone
	}, int(count))
}

// popPending is ts_stack_pop_pending.
func (s *stack) popPending(version int) []stackSlice {
	pop := s.iterate(version, func(it *stackIterator) int {
		if it.subtreeCount >= 1 {
			if it.isPending {
				return actionPop | actionStop
			}
			return actionStop
		}
		return actionNone
	}, 0)
	if len(pop) > 0 {
		s.renumberVersion(pop[0].version, version)
		pop[0].version = version
	}
	return pop
}

// popError is ts_stack_pop_error: the subtrees of the ERROR on top, or nil.
func (s *stack) popError(version int) []*subtree {
	node := s.heads[version].node
	for i := 0; i < len(node.links); i++ {
		if node.links[i].subtree != nil && node.links[i].subtree.isError() {
			foundError := false
			pop := s.iterate(version, func(it *stackIterator) int {
				if len(it.subtrees) > 0 {
					if !foundError && it.subtrees[0].isError() {
						foundError = true
						return actionPop | actionStop
					}
					return actionStop
				}
				return actionNone
			}, 1)
			if len(pop) > 0 {
				s.renumberVersion(pop[0].version, version)
				return pop[0].subtrees
			}
			break
		}
	}
	return nil
}

// popAll is ts_stack_pop_all.
func (s *stack) popAll(version int) []stackSlice {
	return s.iterate(version, func(it *stackIterator) int {
		if len(it.node.links) == 0 {
			return actionPop
		}
		return actionNone
	}, 0)
}

const maxSummaryDepth = 16

// recordSummary is ts_stack_record_summary.
func (s *stack) recordSummary(version int, maxDepth uint32) {
	var summary []stackSummaryEntry
	s.iterate(version, func(it *stackIterator) int {
		state := it.node.state
		depth := it.subtreeCount
		if depth > maxDepth {
			return actionStop
		}
		for i := len(summary) - 1; i >= 0; i-- {
			entry := summary[i]
			if entry.depth < depth {
				break
			}
			if entry.depth == depth && entry.state == state {
				return actionNone
			}
		}
		summary = append(summary, stackSummaryEntry{it.node.position, depth, state})
		return actionNone
	}, -1)
	head := &s.heads[version]
	head.summary = summary
	head.hasSummary = true
}

func (s *stack) getSummary(v int) ([]stackSummaryEntry, bool) {
	return s.heads[v].summary, s.heads[v].hasSummary
}

func (s *stack) dynamicPrecedence(v int) int32 { return s.heads[v].node.dynamicPrecedence }

// hasAdvancedSinceError is ts_stack_has_advanced_since_error.
func (s *stack) hasAdvancedSinceError(v int) bool {
	head := &s.heads[v]
	node := head.node
	if node.errorCost == 0 {
		return true
	}
	for node != nil {
		if len(node.links) > 0 {
			st := node.links[0].subtree
			if st != nil {
				if st.totalBytes() > 0 {
					return true
				} else if node.nodeCount > head.nodeCountAtLastError && st.errorCostOf() == 0 {
					node = node.links[0].node
					continue
				}
			}
		}
		break
	}
	return false
}

func (s *stack) removeVersion(v int) {
	s.heads = append(s.heads[:v], s.heads[v+1:]...)
}

// renumberVersion is ts_stack_renumber_version.
func (s *stack) renumberVersion(v1, v2 int) {
	if v1 == v2 {
		return
	}
	source := &s.heads[v1]
	target := &s.heads[v2]
	if target.hasSummary && !source.hasSummary {
		source.summary = target.summary
		source.hasSummary = true
		target.summary = nil
		target.hasSummary = false
	}
	*target = *source
	s.heads = append(s.heads[:v1], s.heads[v1+1:]...)
}

func (s *stack) swapVersions(v1, v2 int) { s.heads[v1], s.heads[v2] = s.heads[v2], s.heads[v1] }

// copyVersion is ts_stack_copy_version.
func (s *stack) copyVersion(v int) int {
	head := s.heads[v]
	head.summary = nil
	head.hasSummary = false
	s.heads = append(s.heads, head)
	return len(s.heads) - 1
}

// canMerge is ts_stack_can_merge.
func (s *stack) canMerge(v1, v2 int) bool {
	h1, h2 := &s.heads[v1], &s.heads[v2]
	return h1.status == stackActive && h2.status == stackActive &&
		h1.node.state == h2.node.state &&
		h1.node.position.bytes == h2.node.position.bytes &&
		h1.node.errorCost == h2.node.errorCost &&
		externalStateEqual(h1.lastExternalToken, h2.lastExternalToken)
}

// merge is ts_stack_merge.
func (s *stack) merge(v1, v2 int) bool {
	if !s.canMerge(v1, v2) {
		return false
	}
	h1, h2 := &s.heads[v1], &s.heads[v2]
	for i := 0; i < len(h2.node.links); i++ {
		h1.node.addLink(h2.node.links[i])
	}
	if h1.node.state == errorState {
		h1.nodeCountAtLastError = h1.node.nodeCount
	}
	s.removeVersion(v2)
	return true
}

func (s *stack) halt(v int) { s.heads[v].status = stackHalted }

func (s *stack) pause(v int, lookahead *subtree) {
	head := &s.heads[v]
	head.status = stackPaused
	head.lookaheadWhenPaused = lookahead
	head.nodeCountAtLastError = head.node.nodeCount
}

func (s *stack) isActive(v int) bool { return s.heads[v].status == stackActive }
func (s *stack) isHalted(v int) bool { return s.heads[v].status == stackHalted }
func (s *stack) isPaused(v int) bool { return s.heads[v].status == stackPaused }

// resume is ts_stack_resume.
func (s *stack) resume(v int) *subtree {
	head := &s.heads[v]
	result := head.lookaheadWhenPaused
	head.status = stackActive
	head.lookaheadWhenPaused = nil
	return result
}
