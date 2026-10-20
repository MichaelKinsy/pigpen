package ts

// Port of lib/src/parser.c (ts_parser_parse and everything it calls on a fresh parse of a string).

const (
	maxVersionCount         = 6
	maxVersionCountOverflow = 4
	maxCostDifference       = 18 * errorCostPerSkippedTree
)

type errorStatus struct {
	cost              uint32
	nodeCount         uint32
	dynamicPrecedence int32
	isInError         bool
}

type errorComparison int

const (
	errorComparisonTakeLeft errorComparison = iota
	errorComparisonPreferLeft
	errorComparisonNone
	errorComparisonPreferRight
	errorComparisonTakeRight
)

type reduceAction struct {
	count             uint32
	symbol            uint16
	dynamicPrecedence int32
	productionID      uint16
}

type tokenCache struct {
	token             *subtree
	lastExternalToken *subtree
	byteIndex         uint32
}

// Tree is a parse result: the root subtree and the language it was parsed with.
type Tree struct {
	lang *Language
	root *subtree
}

// Parser is a ts.Parser for one language. It is not safe for concurrent use.
type Parser struct {
	lang           *Language
	lexer          *lexer
	stack          *stack
	scanner        *bashScanner
	scannerBuf     [serializationBufferSize]byte
	reduceActions  []reduceAction
	trailingExtras []*subtree
	finishedTree   *subtree
	cache          tokenCache
	acceptCount    uint32
}

// NewParser is a parser for lang.
func NewParser(lang *Language) *Parser { return &Parser{lang: lang} }

// Parse is ts_parser_parse over a whole string given as UTF-16 code units. It returns nil only if
// no tree was accepted (which the grammar's error recovery does not allow).
func (p *Parser) Parse(input []uint16) *Tree {
	p.lexer = newLexer(input)
	p.stack = newStack()
	p.scanner = &bashScanner{}
	p.finishedTree = nil
	p.cache = tokenCache{}
	p.acceptCount = 0
	p.trailingExtras = nil
	defer func() {
		p.lexer, p.stack, p.scanner, p.finishedTree, p.cache = nil, nil, nil, nil, tokenCache{}
	}()

	position, lastPosition := uint32(0), uint32(0)
	versionCount := 0
	for {
		for version := 0; ; version++ {
			versionCount = p.stack.versionCount()
			if version >= versionCount {
				break
			}
			for p.stack.isActive(version) {
				p.advance(version)
				position = p.stack.position(version).bytes
				if position > lastPosition || (version > 0 && position == lastPosition) {
					lastPosition = position
					break
				}
			}
		}

		// After advancing each version of the stack, re-sort the versions by their cost, removing
		// any versions that are no longer worth pursuing.
		minErrorCost := p.condenseStack()

		// If there's already a finished parse tree that's better than any in-progress version,
		// then terminate parsing.
		if p.finishedTree != nil && p.finishedTree.errorCostOf() < minErrorCost {
			p.stack.clear()
			break
		}
		if versionCount == 0 {
			break
		}
	}
	if p.finishedTree == nil {
		return nil
	}
	return &Tree{lang: p.lang, root: p.finishedTree}
}

// ---- version comparison

func (p *Parser) compareVersions(a, b errorStatus) errorComparison {
	if !a.isInError && b.isInError {
		if a.cost < b.cost {
			return errorComparisonTakeLeft
		}
		return errorComparisonPreferLeft
	}
	if a.isInError && !b.isInError {
		if b.cost < a.cost {
			return errorComparisonTakeRight
		}
		return errorComparisonPreferRight
	}
	if a.cost < b.cost {
		if (b.cost-a.cost)*(1+a.nodeCount) > maxCostDifference {
			return errorComparisonTakeLeft
		}
		return errorComparisonPreferLeft
	}
	if b.cost < a.cost {
		if (a.cost-b.cost)*(1+b.nodeCount) > maxCostDifference {
			return errorComparisonTakeRight
		}
		return errorComparisonPreferRight
	}
	if a.dynamicPrecedence > b.dynamicPrecedence {
		return errorComparisonPreferLeft
	}
	if b.dynamicPrecedence > a.dynamicPrecedence {
		return errorComparisonPreferRight
	}
	return errorComparisonNone
}

func (p *Parser) versionStatus(version int) errorStatus {
	cost := p.stack.errorCost(version)
	isPaused := p.stack.isPaused(version)
	if isPaused {
		cost += errorCostPerSkippedTree
	}
	return errorStatus{
		cost:              cost,
		nodeCount:         p.stack.nodeCountSinceError(version),
		dynamicPrecedence: p.stack.dynamicPrecedence(version),
		isInError:         isPaused || p.stack.state(version) == errorState,
	}
}

func (p *Parser) betterVersionExists(version int, isInError bool, cost uint32) bool {
	if p.finishedTree != nil && p.finishedTree.errorCostOf() <= cost {
		return true
	}
	position := p.stack.position(version)
	status := errorStatus{
		cost:              cost,
		isInError:         isInError,
		dynamicPrecedence: p.stack.dynamicPrecedence(version),
		nodeCount:         p.stack.nodeCountSinceError(version),
	}
	for i, n := 0, p.stack.versionCount(); i < n; i++ {
		if i == version || !p.stack.isActive(i) || p.stack.position(i).bytes < position.bytes {
			continue
		}
		statusI := p.versionStatus(i)
		switch p.compareVersions(status, statusI) {
		case errorComparisonTakeRight:
			return true
		case errorComparisonPreferRight:
			if p.stack.canMerge(i, version) {
				return true
			}
		}
	}
	return false
}

// ---- lexing

func (p *Parser) externalScannerScan(externalLexState uint16) bool {
	valid := p.lang.enabledExternalTokens(externalLexState)
	return p.scanner.scan(p.lexer, valid)
}

// canReuseFirstLeaf is ts_parser__can_reuse_first_leaf.
func (p *Parser) canReuseFirstLeaf(state uint16, tree *subtree, entry tableEntry) bool {
	leafSymbol := tree.leafSymbol()
	leafState := tree.leafParseState()
	currentLexMode := p.lang.lexModeFor(state)
	leafLexMode := p.lang.lexModeFor(leafState)

	// At the end of a non-terminal extra node, the lexer normally returns NULL, which indicates
	// that the parser should look for a reduce action at symbol 0. Avoid reusing tokens in this
	// situation.
	if currentLexMode.lexState == 0xffff {
		return false
	}

	// If the token was created in a state with the same set of lookaheads, it is reusable.
	if len(entry.actions) > 0 && leafLexMode == currentLexMode &&
		(leafSymbol != p.lang.keywordCaptureToken || (!tree.isKeyword && tree.parseState == state)) {
		return true
	}

	// Empty tokens are not reusable in states with different lookaheads.
	if tree.size.bytes == 0 && leafSymbol != builtinSymEnd {
		return false
	}

	// If the current state allows external tokens or other tokens that conflict with this token,
	// this token is not reusable.
	return currentLexMode.externalLexState == 0 && entry.isReusable
}

// lex is ts_parser__lex.
func (p *Parser) lex(version int, parseState uint16) *subtree {
	lang := p.lang
	lexMode := lang.lexModeFor(parseState)
	if lexMode.lexState == 0xffff {
		return nil
	}

	startPosition := p.stack.position(version)
	externalToken := p.stack.lastExternalToken(version)

	foundExternalToken := false
	errorMode := parseState == errorState
	skippedError := false
	var errorStartPosition, errorEndPosition Length
	externalScannerStateLen := 0
	externalScannerStateChanged := false
	p.lexer.reset(startPosition)

	for {
		foundToken := false
		currentPosition := p.lexer.currentPosition
		columnValid, columnValue := p.lexer.columnValid, p.lexer.columnValue

		if lexMode.externalLexState != 0 {
			p.lexer.start()
			p.scanner.deserialize(externalToken.externalState())
			foundToken = p.externalScannerScan(lexMode.externalLexState)
			p.lexer.finish()

			if foundToken {
				externalScannerStateLen = int(p.scanner.serialize(p.scannerBuf[:]))
				state := externalToken.externalState()
				externalScannerStateChanged = !bytesEqual(state, p.scannerBuf[:externalScannerStateLen])

				// Avoid infinite loops caused by the external scanner returning empty tokens.
				// Ignore the following classes of empty tokens: tokens produced during error
				// recovery, and tokens that are marked as 'extra' in the grammar.
				if p.lexer.tokenEndPosition.bytes <= currentPosition.bytes && !externalScannerStateChanged {
					symbol := lang.externalSymbols[p.lexer.data.resultSymbol]
					nextParseState := lang.nextState(parseState, symbol)
					tokenIsExtra := nextParseState == parseState
					if errorMode || !p.stack.hasAdvancedSinceError(version) || tokenIsExtra {
						foundToken = false
					}
				}
			}

			if foundToken {
				foundExternalToken = true
				break
			}

			p.lexer.reset(currentPosition)
			p.lexer.columnValid, p.lexer.columnValue = columnValid, columnValue
		}

		p.lexer.start()
		foundToken = lexMain(p.lexer, lexMode.lexState)
		p.lexer.finish()
		if foundToken {
			break
		}

		if !errorMode {
			errorMode = true
			lexMode = lang.lexModeFor(errorState)
			p.lexer.reset(startPosition)
			continue
		}

		if !skippedError {
			skippedError = true
			errorStartPosition = p.lexer.tokenStartPosition
			errorEndPosition = p.lexer.tokenStartPosition
		}

		if p.lexer.currentPosition.bytes == errorEndPosition.bytes {
			if p.lexer.eof() {
				p.lexer.data.resultSymbol = builtinSymError
				break
			}
			p.lexer.advance(false)
		}

		errorEndPosition = p.lexer.currentPosition
	}

	var result *subtree
	if skippedError {
		padding := lengthSub(errorStartPosition, startPosition)
		size := lengthSub(errorEndPosition, errorStartPosition)
		result = newErrorLeaf(lang, padding, size, parseState)
	} else {
		isKeyword := false
		symbol := p.lexer.data.resultSymbol
		padding := lengthSub(p.lexer.tokenStartPosition, startPosition)
		size := lengthSub(p.lexer.tokenEndPosition, p.lexer.tokenStartPosition)

		if foundExternalToken {
			symbol = lang.externalSymbols[symbol]
		} else if symbol == lang.keywordCaptureToken && symbol != 0 {
			endByte := p.lexer.tokenEndPosition.bytes
			p.lexer.reset(p.lexer.tokenStartPosition)
			p.lexer.start()

			isKeyword = lexKeywords(p.lexer, 0)

			if isKeyword && p.lexer.tokenEndPosition.bytes == endByte &&
				(lang.hasActions(parseState, p.lexer.data.resultSymbol) ||
					lang.isReservedWord(parseState, p.lexer.data.resultSymbol)) {
				symbol = p.lexer.data.resultSymbol
			}
		}

		result = newLeaf(lang, symbol, padding, size, parseState, foundExternalToken, isKeyword)

		if foundExternalToken {
			result.externalScannerState = append([]byte(nil), p.scannerBuf[:externalScannerStateLen]...)
			result.hasExternalScannerStateChange = externalScannerStateChanged
		}
	}
	return result
}

func bytesEqual(a, b []byte) bool {
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

// cachedToken is ts_parser__get_cached_token.
func (p *Parser) cachedToken(state uint16, position uint32, lastExternalToken *subtree) (*subtree, tableEntry) {
	c := &p.cache
	if c.token != nil && c.byteIndex == position && externalStateEqual(c.lastExternalToken, lastExternalToken) {
		entry := p.lang.table(state, c.token.symbol)
		if p.canReuseFirstLeaf(state, c.token, entry) {
			return c.token, entry
		}
	}
	return nil, tableEntry{}
}

func (p *Parser) setCachedToken(byteIndex uint32, lastExternalToken, token *subtree) {
	p.cache = tokenCache{token: token, lastExternalToken: lastExternalToken, byteIndex: byteIndex}
}

// ---- tree selection

// selectTree is ts_parser__select_tree: whether right should replace left.
func (p *Parser) selectTree(left, right *subtree) bool {
	if left == nil {
		return true
	}
	if right == nil {
		return false
	}
	if right.errorCostOf() < left.errorCostOf() {
		return true
	}
	if left.errorCostOf() < right.errorCostOf() {
		return false
	}
	if right.dynamicPrec() > left.dynamicPrec() {
		return true
	}
	if left.dynamicPrec() > right.dynamicPrec() {
		return false
	}
	if left.errorCostOf() > 0 {
		return true
	}
	switch compareSubtrees(left, right) {
	case -1:
		return false
	case 1:
		return true
	default:
		return false
	}
}

// selectChildren is ts_parser__select_children.
func (p *Parser) selectChildren(left *subtree, children []*subtree) bool {
	scratch := newNode(p.lang, left.symbol, children, 0)
	return p.selectTree(left, scratch)
}

// ---- shift, reduce, accept

func (p *Parser) shift(version int, state uint16, lookahead *subtree, extra bool) {
	isLeaf := lookahead.childCount() == 0
	toPush := lookahead
	if extra != lookahead.extra && isLeaf {
		toPush = lookahead.clone()
		toPush.extra = extra
	}
	p.stack.push(version, toPush, !isLeaf, state)
	if toPush.hasExternalTokens {
		p.stack.setLastExternalToken(version, toPush.lastExternalToken())
	}
}

func clip(s []*subtree) []*subtree { return s[:len(s):len(s)] }

// reduce is ts_parser__reduce.
func (p *Parser) reduce(version int, symbol uint16, count uint32, dynamicPrecedence int32, productionID uint16, isFragile, endOfNonTerminalExtra bool) int {
	initialVersionCount := p.stack.versionCount()

	// Pop the given number of nodes from the given version of the parse stack. If stack versions
	// have previously merged, then there may be more than one path back through the stack. For
	// each path, create a new parent node to contain the popped children, and push it onto the
	// stack in place of the children.
	pop := p.stack.popCount(version, count)
	removedVersionCount := 0
	haltedVersionCount := p.stack.haltedVersionCount()
	for i := 0; i < len(pop); i++ {
		slice := pop[i]
		sliceVersion := slice.version - removedVersionCount

		// Allow the maximum version count to be temporarily exceeded, but only by a limited
		// threshold.
		if sliceVersion > maxVersionCount+maxVersionCountOverflow+haltedVersionCount {
			p.stack.removeVersion(sliceVersion)
			removedVersionCount++
			for i+1 < len(pop) {
				if pop[i+1].version != slice.version {
					break
				}
				i++
			}
			continue
		}

		// Extra tokens on top of the stack should not be included in this new parent node. They
		// will be re-pushed onto the stack after the parent node is created and pushed.
		children, trailing := removeTrailingExtras(slice.subtrees)
		p.trailingExtras = trailing
		parent := newNode(p.lang, symbol, clip(children), productionID)

		// This pop operation may have caused multiple stack versions to collapse into one,
		// because they all diverged from a common state. In that case, choose one of the arrays
		// of trees to be the parent node's children.
		for i+1 < len(pop) {
			next := pop[i+1]
			if next.version != slice.version {
				break
			}
			i++

			nextChildren, nextTrailing := removeTrailingExtras(next.subtrees)
			if p.selectChildren(parent, nextChildren) {
				p.trailingExtras = nextTrailing
				parent = newNode(p.lang, symbol, clip(nextChildren), productionID)
			}
		}

		state := p.stack.state(sliceVersion)
		nextState := p.lang.nextState(state, symbol)
		if endOfNonTerminalExtra && nextState == state {
			parent.extra = true
		}
		if isFragile || len(pop) > 1 || initialVersionCount > 1 {
			parent.fragileLeft = true
			parent.fragileRight = true
			parent.parseState = treeStateNone
		} else {
			parent.parseState = state
		}
		parent.dynamicPrecedence += dynamicPrecedence

		// Push the parent node onto the stack, along with any extra tokens that were previously
		// on top of the stack.
		p.stack.push(sliceVersion, parent, false, nextState)
		for _, extra := range p.trailingExtras {
			p.stack.push(sliceVersion, extra, false, nextState)
		}

		for j := 0; j < sliceVersion; j++ {
			if j == version {
				continue
			}
			if p.stack.merge(j, sliceVersion) {
				removedVersionCount++
				break
			}
		}
	}

	// Return the first new stack version that was created.
	if p.stack.versionCount() > initialVersionCount {
		return initialVersionCount
	}
	return stackVersionNone
}

// accept is ts_parser__accept.
func (p *Parser) accept(version int, lookahead *subtree) {
	p.stack.push(version, lookahead, false, 1)

	pop := p.stack.popAll(version)
	for _, slice := range pop {
		trees := slice.subtrees

		var root *subtree
		for j := len(trees) - 1; j >= 0; j-- {
			tree := trees[j]
			if !tree.extra {
				spliced := make([]*subtree, 0, len(trees)-1+len(tree.children))
				spliced = append(spliced, trees[:j]...)
				spliced = append(spliced, tree.children...)
				spliced = append(spliced, trees[j+1:]...)
				root = newNode(p.lang, tree.symbol, spliced, tree.productionID)
				break
			}
		}

		p.acceptCount++

		if p.finishedTree != nil {
			if p.selectTree(p.finishedTree, root) {
				p.finishedTree = root
			}
		} else {
			p.finishedTree = root
		}
	}

	p.stack.removeVersion(pop[0].version)
	p.stack.halt(version)
}

// ---- error recovery

// doAllPotentialReductions is ts_parser__do_all_potential_reductions.
func (p *Parser) doAllPotentialReductions(startingVersion int, lookaheadSymbol uint16) bool {
	initialVersionCount := p.stack.versionCount()

	canShiftLookaheadSymbol := false
	version := startingVersion
	for i := 0; ; i++ {
		versionCount := p.stack.versionCount()
		if version >= versionCount {
			break
		}

		merged := false
		for j := initialVersionCount; j < version; j++ {
			if p.stack.merge(j, version) {
				merged = true
				break
			}
		}
		if merged {
			continue
		}

		state := p.stack.state(version)
		hasShiftAction := false
		p.reduceActions = p.reduceActions[:0]

		var firstSymbol, endSymbol uint16
		if lookaheadSymbol != 0 {
			firstSymbol = lookaheadSymbol
			endSymbol = lookaheadSymbol + 1
		} else {
			firstSymbol = 1
			endSymbol = uint16(p.lang.tokenCount)
		}

		for symbol := firstSymbol; symbol < endSymbol; symbol++ {
			entry := p.lang.table(state, symbol)
			for _, action := range entry.actions {
				switch action.typ {
				case actionShift, actionRecover:
					if !action.extra && !action.repetition {
						hasShiftAction = true
					}
				case actionReduce:
					if action.childCount > 0 {
						p.addReduceAction(reduceAction{
							symbol:            action.symbol,
							count:             uint32(action.childCount),
							dynamicPrecedence: int32(action.dynPrec),
							productionID:      action.prodID,
						})
					}
				}
			}
		}

		reductionVersion := stackVersionNone
		for _, action := range p.reduceActions {
			reductionVersion = p.reduce(version, action.symbol, action.count, action.dynamicPrecedence, action.productionID, true, false)
		}

		if hasShiftAction {
			canShiftLookaheadSymbol = true
		} else if reductionVersion != stackVersionNone && i < maxVersionCount {
			p.stack.renumberVersion(reductionVersion, version)
			continue
		} else if lookaheadSymbol != 0 {
			p.stack.removeVersion(version)
		}

		if version == startingVersion {
			version = versionCount
		} else {
			version++
		}
	}

	return canShiftLookaheadSymbol
}

// addReduceAction is ts_reduce_action_set_add.
func (p *Parser) addReduceAction(a reduceAction) {
	for _, existing := range p.reduceActions {
		if existing.symbol == a.symbol && existing.count == a.count {
			return
		}
	}
	p.reduceActions = append(p.reduceActions, a)
}

// recoverToState is ts_parser__recover_to_state.
func (p *Parser) recoverToState(version int, depth uint32, goalState uint16) bool {
	pop := p.stack.popCount(version, depth)
	previousVersion := stackVersionNone

	for i := 0; i < len(pop); i++ {
		slice := pop[i]

		if slice.version == previousVersion {
			pop = append(pop[:i], pop[i+1:]...)
			i--
			continue
		}

		if p.stack.state(slice.version) != goalState {
			p.stack.halt(slice.version)
			pop = append(pop[:i], pop[i+1:]...)
			i--
			continue
		}

		errorTrees := p.stack.popError(slice.version)
		if len(errorTrees) > 0 {
			errorTree := errorTrees[0]
			if len(errorTree.children) > 0 {
				spliced := append([]*subtree(nil), errorTree.children...)
				slice.subtrees = append(spliced, slice.subtrees...)
			}
		}

		rest, extras := removeTrailingExtras(slice.subtrees)
		p.trailingExtras = extras
		slice.subtrees = rest

		if len(slice.subtrees) > 0 {
			errNode := newErrorNode(p.lang, clip(slice.subtrees), true)
			p.stack.push(slice.version, errNode, false, goalState)
		}

		for _, tree := range p.trailingExtras {
			p.stack.push(slice.version, tree, false, goalState)
		}

		previousVersion = slice.version
	}

	return previousVersion != stackVersionNone
}

// recover is ts_parser__recover.
func (p *Parser) recover(version int, lookahead *subtree) {
	didRecover := false
	previousVersionCount := p.stack.versionCount()
	position := p.stack.position(version)
	summary, hasSummary := p.stack.getSummary(version)
	nodeCountSinceError := p.stack.nodeCountSinceError(version)
	currentErrorCost := p.stack.errorCost(version)

	// When the parser is in the error state, there are two strategies for recovering with a given
	// lookahead token:
	// 1. Find a previous state on the stack in which that lookahead token would be valid. Then,
	//    create a new stack version that is in that state again. This entails popping all of the
	//    subtrees that have been pushed onto the stack since that previous state, and wrapping
	//    them in an ERROR node.
	// 2. Wrap the lookahead token in an ERROR node, push that ERROR node onto the stack, and move
	//    on to the next lookahead token, remaining in the error state.
	if hasSummary && !lookahead.isError() {
		for i := range summary {
			entry := summary[i]

			if entry.state == errorState {
				continue
			}
			if entry.position.bytes == position.bytes {
				continue
			}
			depth := entry.depth
			if nodeCountSinceError > 0 {
				depth++
			}

			// Do not recover in ways that create redundant stack versions.
			wouldMerge := false
			for j := 0; j < previousVersionCount; j++ {
				if p.stack.state(j) == entry.state && p.stack.position(j).bytes == position.bytes {
					wouldMerge = true
					break
				}
			}
			if wouldMerge {
				continue
			}

			// Do not recover if the result would clearly be worse than some existing stack
			// version.
			newCost := currentErrorCost +
				entry.depth*errorCostPerSkippedTree +
				(position.bytes-entry.position.bytes)*errorCostPerSkippedChar +
				(position.row-entry.position.row)*errorCostPerSkippedLine
			if p.betterVersionExists(version, false, newCost) {
				break
			}

			// If the current lookahead token is valid in some previous state, recover to that
			// state. Then stop looking for further recoveries.
			if p.lang.hasActions(entry.state, lookahead.symbol) {
				if p.recoverToState(version, depth, entry.state) {
					didRecover = true
					break
				}
			}
		}
	}

	// In the process of attempting to recover, some stack versions may have been created and
	// subsequently halted. Remove those versions.
	for i := previousVersionCount; i < p.stack.versionCount(); i++ {
		if !p.stack.isActive(i) {
			p.stack.removeVersion(i)
			i--
		}
	}

	// If the parser is still in the error state at the end of the file, just wrap everything in an
	// ERROR node and terminate.
	if lookahead.isEOF() {
		parent := newErrorNode(p.lang, nil, false)
		p.stack.push(version, parent, false, 1)
		p.accept(version, lookahead)
		return
	}

	// If strategy 1 succeeded, a new stack version will have been created which is able to handle
	// the current lookahead token. Now, in addition, try strategy 2: skip the current lookahead
	// token by wrapping it in an ERROR node.

	// Don't pursue this additional strategy if there are already too many stack versions.
	if didRecover && p.stack.versionCount() > maxVersionCount {
		p.stack.halt(version)
		return
	}

	if didRecover && lookahead.hasExternalScannerStateChange {
		p.stack.halt(version)
		return
	}

	// Do not recover if the result would clearly be worse than some existing stack version.
	newCost := currentErrorCost + errorCostPerSkippedTree +
		lookahead.totalBytes()*errorCostPerSkippedChar +
		lookahead.totalSize().row*errorCostPerSkippedLine
	if p.betterVersionExists(version, false, newCost) {
		p.stack.halt(version)
		return
	}

	// If the current lookahead token is an extra token, mark it as extra. This means it won't be
	// counted in error cost calculations.
	entry := p.lang.table(1, lookahead.symbol)
	if n := len(entry.actions); n > 0 && entry.actions[n-1].typ == actionShift && entry.actions[n-1].extra {
		lookahead = lookahead.clone()
		lookahead.extra = true
	}

	// Wrap the lookahead token in an ERROR.
	errorRepeat := newNode(p.lang, builtinSymErrorRepeat, []*subtree{lookahead}, 0)

	// If other tokens have already been skipped, so there is already an ERROR at the top of the
	// stack, then pop that ERROR off the stack and wrap the two ERRORs together into one larger
	// ERROR.
	if nodeCountSinceError > 0 {
		pop := p.stack.popCount(version, 1)

		// If multiple stack versions have merged at this point, just pick one of the errors
		// arbitrarily and discard the rest.
		if len(pop) > 1 {
			for p.stack.versionCount() > pop[0].version+1 {
				p.stack.removeVersion(pop[0].version + 1)
			}
		}

		p.stack.renumberVersion(pop[0].version, version)
		subtrees := append(clip(pop[0].subtrees), errorRepeat)
		errorRepeat = newNode(p.lang, builtinSymErrorRepeat, subtrees, 0)
	}

	// Push the new ERROR onto the stack.
	p.stack.push(version, errorRepeat, false, errorState)
	if lookahead.hasExternalTokens {
		p.stack.setLastExternalToken(version, lookahead.lastExternalToken())
	}
}

// handleError is ts_parser__handle_error.
func (p *Parser) handleError(version int, lookahead *subtree) {
	previousVersionCount := p.stack.versionCount()

	// Perform any reductions that can happen in this state, regardless of the lookahead. After
	// skipping one or more invalid tokens, the parser might find a token that would have allowed
	// a reduction to take place.
	p.doAllPotentialReductions(version, 0)
	versionCount := p.stack.versionCount()
	position := p.stack.position(version)

	// Push a discontinuity onto the stack. Merge all of the stack versions that were created in
	// the previous step.
	didInsertMissingToken := false
	for v := version; v < versionCount; {
		if !didInsertMissingToken {
			state := p.stack.state(v)
			for missingSymbol := uint16(1); missingSymbol < uint16(p.lang.tokenCount); missingSymbol++ {
				stateAfterMissingSymbol := p.lang.nextState(state, missingSymbol)
				if stateAfterMissingSymbol == 0 || stateAfterMissingSymbol == state {
					continue
				}

				if p.lang.hasReduceAction(stateAfterMissingSymbol, lookahead.leafSymbol()) {
					p.lexer.reset(position)
					p.lexer.markEnd()
					padding := lengthSub(p.lexer.tokenEndPosition, position)

					versionWithMissingTree := p.stack.copyVersion(v)
					missingTree := newMissingLeaf(p.lang, missingSymbol, padding)
					p.stack.push(versionWithMissingTree, missingTree, false, stateAfterMissingSymbol)

					if p.doAllPotentialReductions(versionWithMissingTree, lookahead.leafSymbol()) {
						didInsertMissingToken = true
						break
					}
				}
			}
		}

		p.stack.push(v, nil, false, errorState)
		if v == version {
			v = previousVersionCount
		} else {
			v++
		}
	}

	for i := previousVersionCount; i < versionCount; i++ {
		p.stack.merge(version, previousVersionCount)
	}

	p.stack.recordSummary(version, maxSummaryDepth)

	p.recover(version, lookahead)
}

// ---- main loop

// advance is ts_parser__advance (without old-tree reuse, which a fresh parse never has).
func (p *Parser) advance(version int) {
	state := p.stack.state(version)
	position := p.stack.position(version).bytes
	lastExternalToken := p.stack.lastExternalToken(version)

	// Try the token the lexer returned last time before running it again.
	lookahead, tableEnt := p.cachedToken(state, position, lastExternalToken)

	needsLex := lookahead == nil
	for {
		// Otherwise, re-run the lexer.
		if needsLex {
			needsLex = false
			lookahead = p.lex(version, state)

			if lookahead != nil {
				p.setCachedToken(position, lastExternalToken, lookahead)
				tableEnt = p.lang.table(state, lookahead.symbol)
			} else {
				// When parsing a non-terminal extra, a null lookahead indicates the end of the
				// rule. The reduction is stored in the EOF table entry. After the reduction, the
				// lexer needs to be run again.
				tableEnt = p.lang.table(state, builtinSymEnd)
			}
		}

		// Process each parse action for the current lookahead token in the current state. If
		// there are multiple actions, then this is an ambiguous state. REDUCE actions always
		// create a new stack version, whereas SHIFT actions update the existing stack version and
		// terminate this loop.
		didReduce := false
		lastReductionVersion := stackVersionNone
		for i := 0; i < len(tableEnt.actions); i++ {
			action := tableEnt.actions[i]

			switch action.typ {
			case actionShift:
				if action.repetition {
					break
				}
				var nextState uint16
				if action.extra {
					nextState = state
				} else {
					nextState = action.state
				}
				p.shift(version, nextState, lookahead, action.extra)
				return

			case actionReduce:
				isFragile := len(tableEnt.actions) > 1
				endOfNonTerminalExtra := lookahead == nil
				reductionVersion := p.reduce(version, action.symbol, uint32(action.childCount), int32(action.dynPrec), action.prodID, isFragile, endOfNonTerminalExtra)
				didReduce = true
				if reductionVersion != stackVersionNone {
					lastReductionVersion = reductionVersion
				}

			case actionAccept:
				p.accept(version, lookahead)
				return

			case actionRecover:
				p.recover(version, lookahead)
				return
			}
		}

		// If a reduction was performed, then replace the current stack version with one of the
		// stack versions created by a reduction, and continue processing this version of the
		// stack with the same lookahead symbol.
		if lastReductionVersion != stackVersionNone {
			p.stack.renumberVersion(lastReductionVersion, version)
			state = p.stack.state(version)

			// At the end of a non-terminal extra rule, the lexer will return a null subtree,
			// because the parser needs to perform a fixed reduction regardless of the lookahead
			// node. After performing that reduction, run the lexer again based on the current
			// parse state.
			if lookahead == nil {
				needsLex = true
			} else {
				tableEnt = p.lang.table(state, lookahead.leafSymbol())
			}
			continue
		}

		// A reduction was performed, but was merged into an existing stack version. This version
		// can be discarded.
		if didReduce {
			p.stack.halt(version)
			return
		}

		// If the current lookahead token is a keyword that is not valid, but the default word
		// token *is* valid, then treat the lookahead token as the word token instead.
		if lookahead.isKeyword && lookahead.symbol != p.lang.keywordCaptureToken &&
			!p.lang.isReservedWord(state, lookahead.symbol) {
			tableEnt = p.lang.table(state, p.lang.keywordCaptureToken)
			if len(tableEnt.actions) > 0 {
				lookahead = lookahead.clone()
				lookahead.setSymbol(p.lang, p.lang.keywordCaptureToken)
				continue
			}
		}

		// If the current lookahead token is not valid and the previous subtree on the stack was
		// reused from an old tree, then it wasn't actually valid to reuse that previous subtree.
		// Remove it from the stack, and in its place, push each of its children.
		if p.breakdownTopOfStack(version) {
			state = p.stack.state(version)
			needsLex = true
			continue
		}

		// Otherwise, there is definitely an error in this version of the parse stack. Mark this
		// version as paused and continue processing any other stack versions that exist. If some
		// other version advances successfully, then this version can simply be removed. But if all
		// versions end up paused, then error recovery is needed.
		p.stack.pause(version, lookahead)
		return
	}
}

// breakdownTopOfStack is ts_parser__breakdown_top_of_stack.
func (p *Parser) breakdownTopOfStack(version int) bool {
	didBreakDown := false
	pending := false

	for {
		pop := p.stack.popPending(version)
		if len(pop) == 0 {
			break
		}

		didBreakDown = true
		pending = false
		for _, slice := range pop {
			state := p.stack.state(slice.version)
			parent := slice.subtrees[0]

			for _, child := range parent.children {
				pending = child.childCount() > 0

				if child.isError() {
					state = errorState
				} else if !child.extra {
					state = p.lang.nextState(state, child.symbol)
				}

				p.stack.push(slice.version, child, pending, state)
			}

			for j := 1; j < len(slice.subtrees); j++ {
				p.stack.push(slice.version, slice.subtrees[j], false, state)
			}
		}
		if !pending {
			break
		}
	}

	return didBreakDown
}

// condenseStack is ts_parser__condense_stack.
func (p *Parser) condenseStack() uint32 {
	madeChanges := false
	minErrorCost := ^uint32(0)
	for i := 0; i < p.stack.versionCount(); i++ {
		// Prune any versions that have been marked for removal.
		if p.stack.isHalted(i) {
			p.stack.removeVersion(i)
			i--
			continue
		}

		// Keep track of the minimum error cost of any stack version so that it can be returned.
		statusI := p.versionStatus(i)
		if !statusI.isInError && statusI.cost < minErrorCost {
			minErrorCost = statusI.cost
		}

		// Examine each pair of stack versions, removing any versions that are clearly worse than
		// another version. Ensure that the versions are ordered from most promising to least
		// promising.
		for j := 0; j < i; j++ {
			statusJ := p.versionStatus(j)

			switch p.compareVersions(statusJ, statusI) {
			case errorComparisonTakeLeft:
				madeChanges = true
				p.stack.removeVersion(i)
				i--
				j = i

			case errorComparisonPreferLeft, errorComparisonNone:
				if p.stack.merge(j, i) {
					madeChanges = true
					i--
					j = i
				}

			case errorComparisonPreferRight:
				madeChanges = true
				if p.stack.merge(j, i) {
					i--
					j = i
				} else {
					p.stack.swapVersions(i, j)
				}

			case errorComparisonTakeRight:
				madeChanges = true
				p.stack.removeVersion(j)
				i--
				j--
			}
		}
	}

	// Enforce a hard upper bound on the number of stack versions by discarding the least
	// promising versions.
	for p.stack.versionCount() > maxVersionCount {
		p.stack.removeVersion(maxVersionCount)
		madeChanges = true
	}

	// If the best-performing stack version is currently paused, or all versions are paused, then
	// resume the best paused version and begin the error recovery process. Otherwise, remove the
	// paused versions.
	if p.stack.versionCount() > 0 {
		hasUnpausedVersion := false
		for i, n := 0, p.stack.versionCount(); i < n; i++ {
			if p.stack.isPaused(i) {
				if !hasUnpausedVersion && p.acceptCount < maxVersionCount {
					minErrorCost = p.stack.errorCost(i)
					lookahead := p.stack.resume(i)
					p.handleError(i, lookahead)
					hasUnpausedVersion = true
				} else {
					p.stack.removeVersion(i)
					madeChanges = true
					i--
					n--
				}
			} else {
				hasUnpausedVersion = true
			}
		}
	}
	_ = madeChanges
	return minErrorCost
}
