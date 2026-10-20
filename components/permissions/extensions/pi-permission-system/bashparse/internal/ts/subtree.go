package ts

// Port of lib/src/subtree.{h,c}. The C union of an inline and a heap representation is one
// struct: inlining is a storage optimisation and every accessor returns the same value for both
// forms. A *subtree is immutable once it has been shared; "make mutable" (ts_subtree_make_mut)
// is clone, which is what the C does whenever the subtree is shared and is indistinguishable from
// mutating a subtree nobody else can see.

const (
	errorCostPerRecovery    = 500
	errorCostPerMissingTree = 110
	errorCostPerSkippedTree = 100
	errorCostPerSkippedLine = 30
	errorCostPerSkippedChar = 1
)

type subtree struct {
	symbol     uint16
	parseState uint16
	padding    Length
	size       Length
	errorCost  uint32
	children   []*subtree
	// lookaheadBytes is how far past the token the lexer read (leaves only). Nothing in a one-shot
	// parse decides on it, but it is part of an inline subtree's identity (see samePtr).
	lookaheadBytes uint32
	// inline is whether the C keeps this leaf in the pointer-sized inline representation.
	inline bool

	visible, named, extra                bool
	fragileLeft, fragileRight            bool
	hasExternalTokens                    bool
	hasExternalScannerStateChange        bool
	isMissing, isKeyword                 bool
	visibleChildCount, namedChildCount   uint32
	visibleDescendantCount               uint32
	dynamicPrecedence                    int32
	repeatDepth                          uint16
	productionID                         uint16
	firstLeafSymbol, firstLeafParseState uint16
	externalScannerState                 []byte // an external leaf's serialized scanner state
	// refs is how many parents hold this subtree, counted over the finished tree by balance.
	refs uint32
}

func (s *subtree) childCount() uint32 { return uint32(len(s.children)) }

// lookaheadBytesOf is ts_subtree_lookahead_bytes.
func (s *subtree) lookaheadBytesOf() uint32 { return s.lookaheadBytes }

func (s *subtree) totalSize() Length  { return lengthAdd(s.padding, s.size) }
func (s *subtree) totalBytes() uint32 { return s.totalSize().bytes }

func (s *subtree) isError() bool { return s.symbol == builtinSymError }
func (s *subtree) isEOF() bool   { return s.symbol == builtinSymEnd }

// errorCostOf is ts_subtree_error_cost.
func (s *subtree) errorCostOf() uint32 {
	if s.isMissing {
		return errorCostPerMissingTree + errorCostPerRecovery
	}
	return s.errorCost
}

func (s *subtree) dynamicPrec() int32 {
	if len(s.children) == 0 {
		return 0
	}
	return s.dynamicPrecedence
}

func (s *subtree) leafSymbol() uint16 {
	if len(s.children) == 0 {
		return s.symbol
	}
	return s.firstLeafSymbol
}

func (s *subtree) leafParseState() uint16 {
	if len(s.children) == 0 {
		return s.parseState
	}
	return s.firstLeafParseState
}

func (s *subtree) visibleDescendants() uint32 {
	if len(s.children) == 0 {
		return 0
	}
	return s.visibleDescendantCount
}

func (s *subtree) repeatDepthOf() uint32 {
	if len(s.children) == 0 {
		return 0
	}
	return uint32(s.repeatDepth)
}

// clone is ts_subtree_clone: a mutable copy sharing the children.
func (a *arenas) clone(s *subtree) *subtree {
	c := a.subs.alloc()
	*c = *s
	if len(s.children) > 0 {
		c.children = a.arrays.alloc(len(s.children))
		copy(c.children, s.children)
	}
	return c
}

// externalState is ts_subtree_external_scanner_state: empty unless the subtree is an external leaf.
func (s *subtree) externalState() []byte {
	if s != nil && s.hasExternalTokens && len(s.children) == 0 {
		return s.externalScannerState
	}
	return nil
}

// externalStateEqual is ts_subtree_external_scanner_state_eq (either side may be nil).
func externalStateEqual(a, b *subtree) bool {
	x, y := a.externalState(), b.externalState()
	if len(x) != len(y) {
		return false
	}
	for i := range x {
		if x[i] != y[i] {
			return false
		}
	}
	return true
}

// lastExternalToken is ts_subtree_last_external_token.
func (s *subtree) lastExternalToken() *subtree {
	if !s.hasExternalTokens {
		return nil
	}
	tree := s
	for len(tree.children) > 0 {
		for i := len(tree.children) - 1; i >= 0; i-- {
			if tree.children[i].hasExternalTokens {
				tree = tree.children[i]
				break
			}
		}
	}
	return tree
}

// canInline is ts_subtree_can_inline.
func canInline(padding, size Length, lookaheadBytes uint32) bool {
	return padding.bytes < 255 && padding.row < 16 && padding.col < 255 &&
		size.bytes < 255 && size.row == 0 && size.col < 255 && lookaheadBytes < 16
}

// samePtr is `left.ptr == right.ptr` on two Subtree unions. A heap subtree is its address. An inline
// subtree has no address: the union's pointer field is its packed fields, so two inline leaves with
// the same symbol, parse state, padding, size, lookahead bytes and flags are "the same" subtree.
// This is what makes two MISSING tokens recovered at one position the same node to
// ts_subtree_has_trailing_empty_descendant, and so decides a zero-width node's previous sibling.
func samePtr(a, b *subtree) bool {
	if a == b {
		return true
	}
	if !a.inline || !b.inline {
		return false
	}
	return a.symbol == b.symbol && a.parseState == b.parseState &&
		a.padding == b.padding && a.size.bytes == b.size.bytes &&
		a.lookaheadBytes == b.lookaheadBytes &&
		a.visible == b.visible && a.named == b.named && a.extra == b.extra &&
		a.isMissing == b.isMissing && a.isKeyword == b.isKeyword
}

// newLeaf is ts_subtree_new_leaf.
func (a *arenas) newLeaf(l *Language, symbol uint16, padding, size Length, lookaheadBytes uint32, parseState uint16, hasExternalTokens, isKeyword bool) *subtree {
	md := l.metadata(symbol)
	s := a.subs.alloc()
	*s = subtree{
		lookaheadBytes:    lookaheadBytes,
		inline:            symbol <= 255 && !hasExternalTokens && canInline(padding, size, lookaheadBytes),
		symbol:            symbol,
		parseState:        parseState,
		padding:           padding,
		size:              size,
		visible:           md.visible,
		named:             md.named,
		extra:             symbol == builtinSymEnd,
		hasExternalTokens: hasExternalTokens,
		isKeyword:         isKeyword,
	}
	return s
}

// newErrorLeaf is ts_subtree_new_error.
func (a *arenas) newErrorLeaf(l *Language, padding, size Length, lookaheadBytes uint32, parseState uint16) *subtree {
	s := a.newLeaf(l, builtinSymError, padding, size, lookaheadBytes, parseState, false, false)
	s.fragileLeft = true
	s.fragileRight = true
	return s
}

// newMissingLeaf is ts_subtree_new_missing_leaf.
func (a *arenas) newMissingLeaf(l *Language, symbol uint16, padding Length, lookaheadBytes uint32) *subtree {
	s := a.newLeaf(l, symbol, padding, Length{}, lookaheadBytes, 0, false, false)
	s.isMissing = true
	return s
}

// setSymbol is ts_subtree_set_symbol on a mutable (cloned) subtree.
func (s *subtree) setSymbol(l *Language, symbol uint16) {
	md := l.metadata(symbol)
	s.symbol = symbol
	s.named = md.named
	s.visible = md.visible
}

// newNode is ts_subtree_new_node: a parent over children, which it takes ownership of.
func (a *arenas) newNode(l *Language, symbol uint16, children []*subtree, productionID uint16) *subtree {
	s := a.subs.alloc()
	initNode(s, l, symbol, children, productionID)
	return s
}

// initNode is newNode into a subtree the caller owns (a scratch one on the stack, for a comparison).
func initNode(s *subtree, l *Language, symbol uint16, children []*subtree, productionID uint16) {
	md := l.metadata(symbol)
	fragile := symbol == builtinSymError || symbol == builtinSymErrorRepeat
	*s = subtree{
		symbol:       symbol,
		children:     children,
		visible:      md.visible,
		named:        md.named,
		fragileLeft:  fragile,
		fragileRight: fragile,
		productionID: productionID,
	}
	s.summarizeChildren(l)
}

// newErrorNode is ts_subtree_new_error_node.
func (a *arenas) newErrorNode(l *Language, children []*subtree, extra bool) *subtree {
	s := a.newNode(l, builtinSymError, children, 0)
	s.extra = extra
	return s
}

// summarizeChildren is ts_subtree_summarize_children: every property that depends on the children.
func (s *subtree) summarizeChildren(l *Language) {
	s.namedChildCount = 0
	s.visibleChildCount = 0
	s.errorCost = 0
	s.repeatDepth = 0
	s.visibleDescendantCount = 0
	s.hasExternalTokens = false
	s.hasExternalScannerStateChange = false
	s.dynamicPrecedence = 0

	structuralIndex := 0
	aliasSequence := l.aliasSequence(s.productionID)
	lookaheadEndByte := uint32(0)
	for i, child := range s.children {
		if child.hasExternalScannerStateChange {
			s.hasExternalScannerStateChange = true
		}
		if i == 0 {
			s.padding = child.padding
			s.size = child.size
		} else {
			s.size = lengthAdd(s.size, child.totalSize())
		}

		if end := s.padding.bytes + s.size.bytes + child.lookaheadBytesOf(); end > lookaheadEndByte {
			lookaheadEndByte = end
		}

		if child.symbol != builtinSymErrorRepeat {
			s.errorCost += child.errorCostOf()
		}

		grandchildCount := child.childCount()
		if s.symbol == builtinSymError || s.symbol == builtinSymErrorRepeat {
			if !child.extra && !(child.isError() && grandchildCount == 0) {
				if child.visible {
					s.errorCost += errorCostPerSkippedTree
				} else if grandchildCount > 0 {
					s.errorCost += errorCostPerSkippedTree * child.visibleChildCount
				}
			}
		}

		s.dynamicPrecedence += child.dynamicPrec()
		s.visibleDescendantCount += child.visibleDescendants()

		if !child.extra && child.symbol != 0 && aliasSequence != nil && aliasSequence[structuralIndex] != 0 {
			s.visibleDescendantCount++
			s.visibleChildCount++
			if l.metadata(aliasSequence[structuralIndex]).named {
				s.namedChildCount++
			}
		} else if child.visible {
			s.visibleDescendantCount++
			s.visibleChildCount++
			if child.named {
				s.namedChildCount++
			}
		} else if grandchildCount > 0 {
			s.visibleChildCount += child.visibleChildCount
			s.namedChildCount += child.namedChildCount
		}

		if child.hasExternalTokens {
			s.hasExternalTokens = true
		}
		if child.isError() {
			s.fragileLeft = true
			s.fragileRight = true
			s.parseState = treeStateNone
		}

		if !child.extra {
			structuralIndex++
		}
	}

	s.lookaheadBytes = lookaheadEndByte - s.size.bytes - s.padding.bytes

	if s.symbol == builtinSymError || s.symbol == builtinSymErrorRepeat {
		s.errorCost += errorCostPerRecovery +
			errorCostPerSkippedChar*s.size.bytes +
			errorCostPerSkippedLine*s.size.row
	}

	if len(s.children) > 0 {
		first := s.children[0]
		last := s.children[len(s.children)-1]

		s.firstLeafSymbol = first.leafSymbol()
		s.firstLeafParseState = first.leafParseState()

		if first.fragileLeft {
			s.fragileLeft = true
		}
		if last.fragileRight {
			s.fragileRight = true
		}

		if len(s.children) >= 2 && !s.visible && !s.named && first.symbol == s.symbol {
			if first.repeatDepthOf() > last.repeatDepthOf() {
				s.repeatDepth = uint16(first.repeatDepthOf() + 1)
			} else {
				s.repeatDepth = uint16(last.repeatDepthOf() + 1)
			}
		}
	}
}

// compareSubtrees is ts_subtree_compare: -1, 0 or 1 by symbol, then child count, in pre-order.
func compareSubtrees(left, right *subtree) int {
	switch {
	case left.symbol < right.symbol:
		return -1
	case right.symbol < left.symbol:
		return 1
	case left.childCount() < right.childCount():
		return -1
	case right.childCount() < left.childCount():
		return 1
	}
	for i := range left.children {
		if r := compareSubtrees(left.children[i], right.children[i]); r != 0 {
			return r
		}
	}
	return 0
}

// removeTrailingExtras is ts_subtree_array_remove_trailing_extras: it returns the array without
// its trailing extras, and those extras in order.
func removeTrailingExtras(trees []*subtree) (rest, extras []*subtree) {
	n := len(trees)
	for n > 0 && trees[n-1].extra {
		n--
	}
	extras = append([]*subtree(nil), trees[n:]...)
	return trees[:n], extras
}
