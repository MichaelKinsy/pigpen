package hashline

// Pure data types shared across the hashline tokenizer, parser, applier,
// and patcher.
// upstream: crates/edit/src/modes/hashline/types.rs

// Anchor is a 1-indexed line anchor.
// upstream: crates/edit/src/modes/hashline/types.rs:9
type Anchor struct {
	Line uint32
}

// CursorKind identifies where an Insert edit lands relative to existing content.
// upstream: crates/edit/src/modes/hashline/types.rs:15
type CursorKind int

const (
	CursorBof CursorKind = iota
	CursorEof
	CursorBeforeAnchor
	CursorAfterAnchor
)

// Cursor is where an Insert or gap Paste lands.
// upstream: crates/edit/src/modes/hashline/types.rs:15
type Cursor struct {
	Kind   CursorKind
	Anchor Anchor
}

// ParsedRange is a parsed A.=B inclusive line range.
// upstream: crates/edit/src/modes/hashline/types.rs:24
type ParsedRange struct {
	Start Anchor
	End   Anchor
}

// PasteTargetKind identifies gap vs span paste.
// upstream: crates/edit/src/modes/hashline/types.rs:31
type PasteTargetKind int

const (
	PasteTargetGap PasteTargetKind = iota
	PasteTargetSpan
)

// PasteTarget is where a paste edit lands.
// upstream: crates/edit/src/modes/hashline/types.rs:31
type PasteTarget struct {
	Kind   PasteTargetKind
	Cursor Cursor
	Range  ParsedRange
}

// BlockMode is the deferred block-op mode.
// upstream: crates/edit/src/modes/hashline/types.rs:38
type BlockMode int

const (
	BlockModeReplace     BlockMode = iota // None in Rust
	BlockModeInsertAfter                  // Some(InsertAfter)
	BlockModeCut                          // Some(Cut)
	BlockModePasteAfter                   // Some(PasteAfter)
)

// EditKind identifies the low-level Edit variant.
// upstream: crates/edit/src/modes/hashline/types.rs:48
type EditKind int

const (
	EditInsert EditKind = iota
	EditDelete
	EditCut
	EditPaste
	EditBlock
)

// Edit is one low-level edit produced by the parser and consumed by the applier.
// Multi-line replacements decompose to one Insert per replacement line plus one
// Delete per consumed line.
// upstream: crates/edit/src/modes/hashline/types.rs:48
type Edit struct {
	Kind        EditKind
	LineNum     uint32
	Index       uint32
	Cursor      Cursor      // Insert
	Text        string      // Insert
	Replacement bool        // Insert
	BlockStart  uint32      // Insert, Paste (0 = none)
	Anchor      Anchor      // Delete, Block
	OldAssert   string      // Delete (optional)
	HasOldAssrt bool        // Delete
	Range       ParsedRange // Cut
	Register    string      // Cut, Paste, Block
	HasRegister bool        // Cut, Paste, Block
	PasteAt     PasteTarget // Paste
	Payloads    []string    // Block
	BlockMode   BlockMode   // Block
}

// FileOpKind identifies a whole-file operation.
// upstream: crates/edit/src/modes/hashline/types.rs:119
type FileOpKind int

const (
	FileOpNone FileOpKind = iota
	FileOpDelete
	FileOpMove
)

// FileOp is a whole-file REM or MV directive.
// upstream: crates/edit/src/modes/hashline/types.rs:119
type FileOp struct {
	Kind FileOpKind
	Dest string
}

// BlockOpKind identifies which block op produced a BlockResolution.
// upstream: crates/edit/src/modes/hashline/types.rs:126
type BlockOpKind int

const (
	BlockOpReplace BlockOpKind = iota
	BlockOpInsertAfter
	BlockOpCut
	BlockOpPasteAfter
)

// BlockResolution records one resolved block-op anchor and its concrete line span.
// upstream: crates/edit/src/modes/hashline/types.rs:135
type BlockResolution struct {
	AnchorLine uint32
	Start      uint32
	End        uint32
	Op         BlockOpKind
}

// BlockSpan is a resolved 1-indexed inclusive line span of a block target.
// upstream: crates/edit/src/modes/hashline/types.rs:145
type BlockSpan struct {
	Start uint32
	End   uint32
}

// ApplyResult is the result of applying a parsed set of edits to a text body.
// upstream: crates/edit/src/modes/hashline/types.rs:152
type ApplyResult struct {
	Text             string
	FirstChangedLine uint32 // 0 = none
	Warnings         []string
	BlockResolutions []BlockResolution
}

// EngineFileOp identifies what a staged file does to the filesystem.
// upstream: crates/edit/src/engine.rs:51
type EngineFileOp string

const (
	EngineFileOpCreate EngineFileOp = "create"
	EngineFileOpUpdate EngineFileOp = "update"
	EngineFileOpDelete EngineFileOp = "delete"
	EngineFileOpNoop   EngineFileOp = "noop"
)

// WireOp returns the wire label matching crates/edit/src/engine.rs:62.
func (op EngineFileOp) WireOp() string {
	switch op {
	case EngineFileOpCreate:
		return "create"
	case EngineFileOpDelete:
		return "delete"
	default:
		return "update"
	}
}

// Resolved pairs an absolute filesystem path with its display path.
// upstream: crates/edit/src/engine.rs:74
type Resolved struct {
	Absolute string
	Display  string
}
