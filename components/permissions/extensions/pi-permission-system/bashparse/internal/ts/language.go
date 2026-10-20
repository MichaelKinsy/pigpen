// Package ts is a pure-Go port of the parts of the tree-sitter C runtime (lib/src of v0.26.9, the
// runtime web-tree-sitter 0.26.9 is built from) that parse a whole string with a generated
// parser, plus the node accessors web-tree-sitter exposes. It runs the parse tables of
// tree-sitter-bash 0.25.1 (bash_tables.bin.gz, dumped from the grammar's own src/parser.c by
// port/bashparse-diff/gen/dumptables.c), the lexer transpiled from the same file (lex_gen.go), and
// a port of the grammar's external scanner (scanner.c, bash_scanner.go).
//
// Why a port and not a Go tree-sitter reimplementation: the original's checks read the exact tree,
// error recovery included (version costs, pruning, token reuse), and a recovery that differs in a
// single ERROR boundary changes what a command is taken to run. This package keeps the C code's
// structure and names (cited as `file.c:function`) so it can be compared line by line, and is
// proven against the C runtime and web-tree-sitter by the differential in port/bashparse-diff.
//
// Left out because a one-shot parse of a string never reaches it: incremental reuse of an old tree
// (reusable_node, edits, included range differences), included ranges other than the whole text,
// cancellation and progress callbacks, logging and dot graphs, subtree reference counting and
// pooling (the garbage collector owns subtrees; a "mutable" subtree is a copy; balancing counts
// references itself, see balance.go), and the column-dependence bookkeeping that only incremental
// reuse reads.
//
// Positions are UTF-16: the input is the UTF-16 code units of a JavaScript string and a Length's
// bytes are UTF-16LE bytes (two per code unit, four per surrogate pair), as web-tree-sitter feeds
// the C runtime, because error costs are counted in those bytes.
package ts

import (
	"bytes"
	"compress/gzip"
	_ "embed"
	"encoding/binary"
	"fmt"
	"io"
	"sync"
)

//go:embed bash_tables.bin.gz
var tablesGz []byte

const (
	builtinSymEnd         = 0
	builtinSymError       = 0xffff
	builtinSymErrorRepeat = 0xfffe
	errorState            = 0
	treeStateNone         = 0xffff
)

const (
	actionShift   = 0
	actionReduce  = 1
	actionAccept  = 2
	actionRecover = 3
)

type symbolMetadata struct{ visible, named, supertype bool }

type mapSlice struct{ index, length uint16 }

type fieldMapEntry struct {
	fieldID    uint16
	childIndex uint8
	inherited  bool
}

type lexMode struct{ lexState, externalLexState, reservedWordSetID uint16 }

// parseAction is a TSParseAction.
type parseAction struct {
	typ        uint8
	state      uint16
	extra      bool
	repetition bool
	childCount uint8
	symbol     uint16
	dynPrec    int16
	prodID     uint16
}

// tableEntry is a TableEntry: the actions of one (state, symbol) pair.
type tableEntry struct {
	actions    []parseAction
	isReusable bool
}

// Language is the loaded grammar tables (TSLanguage).
type Language struct {
	symbolCount, tokenCount, externalTokenCount, stateCount uint32
	largeStateCount, productionIDCount, fieldCount          uint32
	maxAliasSequenceLength                                  uint32
	keywordCaptureToken                                     uint16

	symbolNames      []string
	publicSymbolMap  []uint16
	symbolMetadata   []symbolMetadata
	fieldNames       []string
	fieldMapSlices   []mapSlice
	fieldMapEntries  []fieldMapEntry
	aliasSequences   []uint16
	lexModes         []lexMode
	parseTable       []uint16
	smallParseTable  []uint16
	smallParseMap    []uint32
	actions          []parseAction // every 8-byte slot decoded as an action (entry headers decode to unused values)
	rawActions       [][8]byte
	externalSymbols  []uint16
	externalStates   []bool
	externalStateRow uint32
}

var (
	bashOnce sync.Once
	bashLang *Language
	bashErr  error
)

// BashLanguage is the tree-sitter-bash 0.25.1 grammar.
func BashLanguage() (*Language, error) {
	bashOnce.Do(func() { bashLang, bashErr = loadLanguage() })
	return bashLang, bashErr
}

type reader struct {
	b   []byte
	off int
	err error
}

func (r *reader) take(n int) []byte {
	if r.err != nil || r.off+n > len(r.b) {
		r.err = io.ErrUnexpectedEOF
		return make([]byte, n)
	}
	s := r.b[r.off : r.off+n]
	r.off += n
	return s
}
func (r *reader) u8() uint8   { return r.take(1)[0] }
func (r *reader) u16() uint16 { return binary.LittleEndian.Uint16(r.take(2)) }
func (r *reader) u32() uint32 { return binary.LittleEndian.Uint32(r.take(4)) }
func (r *reader) str() string { n := int(r.u16()); return string(r.take(n)) }

func loadLanguage() (*Language, error) {
	zr, err := gzip.NewReader(bytes.NewReader(tablesGz))
	if err != nil {
		return nil, err
	}
	data, err := io.ReadAll(zr)
	if err != nil {
		return nil, err
	}
	r := &reader{b: data}
	if r.u32() != 0x31425354 {
		return nil, fmt.Errorf("ts: bad tables magic")
	}
	l := &Language{}
	l.symbolCount = r.u32()
	l.tokenCount = r.u32()
	l.externalTokenCount = r.u32()
	l.stateCount = r.u32()
	l.largeStateCount = r.u32()
	l.productionIDCount = r.u32()
	l.fieldCount = r.u32()
	l.maxAliasSequenceLength = r.u32()
	l.keywordCaptureToken = uint16(r.u32())
	actionCount := r.u32()
	smallLen := r.u32()
	for i := uint32(0); i < l.symbolCount; i++ {
		l.symbolNames = append(l.symbolNames, r.str())
	}
	for i := uint32(0); i < l.symbolCount; i++ {
		l.publicSymbolMap = append(l.publicSymbolMap, r.u16())
	}
	for i := uint32(0); i < l.symbolCount; i++ {
		l.symbolMetadata = append(l.symbolMetadata, symbolMetadata{r.u8() != 0, r.u8() != 0, r.u8() != 0})
	}
	for i := uint32(0); i <= l.fieldCount; i++ {
		l.fieldNames = append(l.fieldNames, r.str())
	}
	for i := uint32(0); i < l.productionIDCount; i++ {
		l.fieldMapSlices = append(l.fieldMapSlices, mapSlice{r.u16(), r.u16()})
	}
	fe := r.u32()
	for i := uint32(0); i < fe; i++ {
		l.fieldMapEntries = append(l.fieldMapEntries, fieldMapEntry{r.u16(), r.u8(), r.u8() != 0})
	}
	for i := uint32(0); i < l.productionIDCount*l.maxAliasSequenceLength; i++ {
		l.aliasSequences = append(l.aliasSequences, r.u16())
	}
	for i := uint32(0); i < l.stateCount; i++ {
		r.u16() // primary_state_ids: only query analysis reads them
	}
	for i := uint32(0); i < l.stateCount; i++ {
		l.lexModes = append(l.lexModes, lexMode{r.u16(), r.u16(), r.u16()})
	}
	l.parseTable = make([]uint16, l.largeStateCount*l.symbolCount)
	for i := range l.parseTable {
		l.parseTable[i] = r.u16()
	}
	l.smallParseTable = make([]uint16, smallLen)
	for i := range l.smallParseTable {
		l.smallParseTable[i] = r.u16()
	}
	l.smallParseMap = make([]uint32, l.stateCount-l.largeStateCount)
	for i := range l.smallParseMap {
		l.smallParseMap[i] = r.u32()
	}
	l.rawActions = make([][8]byte, actionCount)
	l.actions = make([]parseAction, actionCount)
	for i := range l.rawActions {
		copy(l.rawActions[i][:], r.take(8))
		l.actions[i] = decodeAction(l.rawActions[i])
	}
	for i := uint32(0); i < l.externalTokenCount; i++ {
		l.externalSymbols = append(l.externalSymbols, r.u16())
	}
	rows := r.u32()
	l.externalStateRow = rows
	l.externalStates = make([]bool, rows*l.externalTokenCount)
	for i := range l.externalStates {
		l.externalStates[i] = r.u8() != 0
	}
	if r.err != nil {
		return nil, r.err
	}
	if r.off != len(data) {
		return nil, fmt.Errorf("ts: %d trailing table bytes", len(data)-r.off)
	}
	return l, nil
}

// decodeAction reads a TSParseAction from an 8-byte slot (offsets checked by dumptables.c).
func decodeAction(b [8]byte) parseAction {
	return parseAction{
		typ:        b[0],
		state:      binary.LittleEndian.Uint16(b[2:4]),
		extra:      b[4] != 0,
		repetition: b[5] != 0,
		childCount: b[1],
		symbol:     binary.LittleEndian.Uint16(b[2:4]),
		dynPrec:    int16(binary.LittleEndian.Uint16(b[4:6])),
		prodID:     binary.LittleEndian.Uint16(b[6:8]),
	}
}

// lookup is ts_language_lookup: the table value for a state and symbol (a successor state for a
// non-terminal, an action index for a terminal).
func (l *Language) lookup(state uint16, symbol uint16) uint16 {
	if uint32(state) >= l.largeStateCount {
		index := l.smallParseMap[uint32(state)-l.largeStateCount]
		data := l.smallParseTable[index:]
		groupCount := data[0]
		data = data[1:]
		for i := uint16(0); i < groupCount; i++ {
			sectionValue := data[0]
			symbolCount := data[1]
			data = data[2:]
			for j := uint16(0); j < symbolCount; j++ {
				if data[j] == symbol {
					return sectionValue
				}
			}
			data = data[symbolCount:]
		}
		return 0
	}
	return l.parseTable[uint32(state)*l.symbolCount+uint32(symbol)]
}

func (l *Language) hasActions(state, symbol uint16) bool { return l.lookup(state, symbol) != 0 }

// table is ts_language_table_entry.
func (l *Language) table(state, symbol uint16) tableEntry {
	if symbol == builtinSymError || symbol == builtinSymErrorRepeat {
		return tableEntry{}
	}
	idx := l.lookup(state, symbol)
	hdr := l.rawActions[idx]
	count := int(hdr[0])
	entry := tableEntry{isReusable: hdr[1] != 0}
	if count > 0 {
		entry.actions = l.actions[int(idx)+1 : int(idx)+1+count]
	}
	return entry
}

func (l *Language) hasReduceAction(state, symbol uint16) bool {
	e := l.table(state, symbol)
	return len(e.actions) > 0 && e.actions[0].typ == actionReduce
}

// nextState is ts_language_next_state.
func (l *Language) nextState(state, symbol uint16) uint16 {
	if symbol == builtinSymError || symbol == builtinSymErrorRepeat {
		return 0
	}
	if uint32(symbol) < l.tokenCount {
		e := l.table(state, symbol)
		if n := len(e.actions); n > 0 {
			a := e.actions[n-1]
			if a.typ == actionShift {
				if a.extra {
					return state
				}
				return a.state
			}
		}
		return 0
	}
	return l.lookup(state, symbol)
}

func (l *Language) metadata(symbol uint16) symbolMetadata {
	switch symbol {
	case builtinSymError:
		return symbolMetadata{visible: true, named: true}
	case builtinSymErrorRepeat:
		return symbolMetadata{}
	}
	return l.symbolMetadata[symbol]
}

func (l *Language) publicSymbol(symbol uint16) uint16 {
	if symbol == builtinSymError {
		return symbol
	}
	return l.publicSymbolMap[symbol]
}

func (l *Language) symbolName(symbol uint16) string {
	switch symbol {
	case builtinSymError:
		return "ERROR"
	case builtinSymErrorRepeat:
		return "_ERROR"
	}
	if uint32(symbol) < l.symbolCount {
		return l.symbolNames[symbol]
	}
	return ""
}

// isReservedWord is ts_language_is_reserved_word; this grammar has no reserved words.
func (l *Language) isReservedWord(state, symbol uint16) bool { return false }

func (l *Language) lexModeFor(state uint16) lexMode { return l.lexModes[state] }

func (l *Language) fieldMap(productionID uint16) []fieldMapEntry {
	if l.fieldCount == 0 {
		return nil
	}
	s := l.fieldMapSlices[productionID]
	return l.fieldMapEntries[s.index : s.index+s.length]
}

// enabledExternalTokens is ts_language_enabled_external_tokens.
func (l *Language) enabledExternalTokens(externalScannerState uint16) []bool {
	if externalScannerState == 0 {
		return nil
	}
	n := l.externalTokenCount
	return l.externalStates[uint32(externalScannerState)*n : (uint32(externalScannerState)+1)*n]
}

// aliasSequence is ts_language_alias_sequence: the alias symbol of each structural child of a
// production (0 for none), or nil for production 0.
func (l *Language) aliasSequence(productionID uint16) []uint16 {
	if productionID == 0 {
		return nil
	}
	n := l.maxAliasSequenceLength
	return l.aliasSequences[uint32(productionID)*n : (uint32(productionID)+1)*n]
}
