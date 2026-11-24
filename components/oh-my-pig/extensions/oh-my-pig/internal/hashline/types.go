package hashline

// OpKind identifies a parsed hashline operation inside a file section.
type OpKind int

const (
	OpPutRange        OpKind = iota // PUT N.=M: with body
	OpPutBlock                      // PUT N*: with body
	OpPutBefore                     // PUT <N: with body (N=1 is BOF)
	OpPutAfter                      // PUT >N: with body
	OpPutEOF                        // PUT >$: with body
	OpPutAfterBlock                 // PUT >N*: with body
	OpCutRange                      // CUT N.=M [@reg]
	OpCutBlock                      // CUT N* [@reg]
	OpPasteBefore                   // PUT <N [@reg]
	OpPasteAfter                    // PUT >N [@reg]
	OpPasteEOF                      // PUT >$ [@reg]
	OpPasteAfterBlock               // PUT >N* [@reg]
	OpPasteRange                    // PUT N.=M @reg
	OpPasteBlock                    // PUT N* @reg
)

// Op is one parsed operation inside a file section.
type Op struct {
	Kind     OpKind
	LineNum  int // 1-indexed line number in the patch input
	Start    int // 1-indexed target line (start of range or anchor)
	End      int // 1-indexed end of range (for Range ops)
	Register string
	Payload  []string
}

// FileOpKind identifies a whole-file operation.
type FileOpKind int

const (
	FileOpNone FileOpKind = iota
	FileOpDelete
	FileOpMove
)

// FileOp is a whole-file REM or MV directive.
type FileOp struct {
	Kind FileOpKind
	Dest string
}

// Section is one [PATH#TAG] file section in a hashline patch.
type Section struct {
	Path     string
	FileHash string
	HasHash  bool
	Ops      []Op
	FileOp   FileOp
}

// BlockSpan is a 1-indexed inclusive line span for a resolved syntactic block.
type BlockSpan struct {
	Start int
	End   int
}

// BlockResolution records a resolved block op for the model-facing header note.
// upstream: crates/pi-edit/src/modes/hashline/types.rs:135
type BlockResolution struct {
	Kind       OpKind
	AnchorLine int
	Start      int
	End        int
}

// ApplyResult is the outcome of applying a section's ops to LF-normalized text.
type ApplyResult struct {
	Content          string
	Changed          bool
	FirstChangedLine int
	Preview          string
	BlockResolutions []BlockResolution
	Warnings         []string
}

// MismatchDetails holds context for a stale or unrecognized snapshot tag.
// upstream: crates/pi-edit/src/modes/hashline/mismatch.rs:17
type MismatchDetails struct {
	Path             string
	ExpectedFileHash string
	ActualFileHash   string
	FileLines        []string
	AnchorLines      []int
	HashRecognized   bool
	TagOriginPaths   []string
}
