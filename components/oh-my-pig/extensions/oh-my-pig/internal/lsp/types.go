package lsp

import "encoding/json"

// upstream: packages/coding-agent/src/lsp/types.ts, packages/tui/src/tools/lsp.ts

// LspParams is the model-facing request shape of the `lsp` tool.
type LspParams struct {
	Action  string   `json:"action"`
	File    string   `json:"file,omitempty"`
	Line    *int     `json:"line,omitempty"`
	Symbol  string   `json:"symbol,omitempty"`
	Query   string   `json:"query,omitempty"`
	NewName string   `json:"new_name,omitempty"`
	Apply   *bool    `json:"apply,omitempty"`
	Timeout *float64 `json:"timeout,omitempty"`
	Payload *string  `json:"payload,omitempty"`
}

// LspToolDetails matches packages/tui/src/tools/lsp.ts:LspToolDetails so both
// terminal and Tern-native renderers can draw from tool results directly.
type LspToolDetails struct {
	ServerName string     `json:"serverName,omitempty"`
	Action     string     `json:"action"`
	Success    bool       `json:"success"`
	Request    *LspParams `json:"request,omitempty"`
}

// FileFormatResult matches packages/tui/src/tools/lsp.ts:FileFormatResult.
type FileFormatResult string

const (
	FileFormatUnchanged   FileFormatResult = "unchanged"
	FileFormatFormatted   FileFormatResult = "formatted"
	FileFormatFailed      FileFormatResult = "failed"
	FileFormatUnsupported FileFormatResult = "unsupported"
)

// FileDiagnosticsResult matches packages/tui/src/tools/lsp.ts:FileDiagnosticsResult.
type FileDiagnosticsResult struct {
	Server     string           `json:"server,omitempty"`
	Messages   []string         `json:"messages"`
	Summary    string           `json:"summary"`
	Errored    bool             `json:"errored"`
	Unreported []string         `json:"unreported,omitempty"`
	Formatter  FileFormatResult `json:"formatter,omitempty"`
}

// =============================================================================
// Core LSP Protocol Types
// =============================================================================

type Position struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

type Range struct {
	Start Position `json:"start"`
	End   Position `json:"end"`
}

type Location struct {
	URI   string `json:"uri"`
	Range Range  `json:"range"`
}

type LocationLink struct {
	OriginSelectionRange *Range `json:"originSelectionRange,omitempty"`
	TargetURI            string `json:"targetUri"`
	TargetRange          Range  `json:"targetRange"`
	TargetSelectionRange Range  `json:"targetSelectionRange"`
}

// DiagnosticSeverity: 1=Error, 2=Warning, 3=Information, 4=Hint.
type DiagnosticSeverity int

const (
	SeverityError   DiagnosticSeverity = 1
	SeverityWarning DiagnosticSeverity = 2
	SeverityInfo    DiagnosticSeverity = 3
	SeverityHint    DiagnosticSeverity = 4
)

type DiagnosticRelatedInformation struct {
	Location Location `json:"location"`
	Message  string   `json:"message"`
}

type Diagnostic struct {
	Range              Range                          `json:"range"`
	Severity           DiagnosticSeverity             `json:"severity,omitempty"`
	Code               any                            `json:"code,omitempty"`
	CodeDescription    *struct{ Href string }         `json:"codeDescription,omitempty"`
	Source             string                         `json:"source,omitempty"`
	Message            string                         `json:"message"`
	Tags               []int                          `json:"tags,omitempty"`
	RelatedInformation []DiagnosticRelatedInformation `json:"relatedInformation,omitempty"`
	Data               any                            `json:"data,omitempty"`
}

type PublishedDiagnostics struct {
	Diagnostics []Diagnostic
	Version     *int
}

type PublishDiagnosticsParams struct {
	URI         string       `json:"uri"`
	Diagnostics []Diagnostic `json:"diagnostics"`
	Version     *int         `json:"version,omitempty"`
}

// =============================================================================
// Text Edits & Workspace Edits
// =============================================================================

type TextEdit struct {
	Range            Range  `json:"range"`
	NewText          string `json:"newText"`
	InsertTextFormat int    `json:"insertTextFormat,omitempty"`
	AnnotationID     string `json:"annotationId,omitempty"`
}

type OptionalVersionedTextDocumentIdentifier struct {
	URI     string `json:"uri"`
	Version *int   `json:"version,omitempty"`
}

type TextDocumentEdit struct {
	TextDocument OptionalVersionedTextDocumentIdentifier `json:"textDocument"`
	Edits        []TextEdit                              `json:"edits"`
}

type CreateFileOptions struct {
	Overwrite      bool `json:"overwrite,omitempty"`
	IgnoreIfExists bool `json:"ignoreIfExists,omitempty"`
}

type RenameFileOptions struct {
	Overwrite      bool `json:"overwrite,omitempty"`
	IgnoreIfExists bool `json:"ignoreIfExists,omitempty"`
}

type DeleteFileOptions struct {
	Recursive         bool `json:"recursive,omitempty"`
	IgnoreIfNotExists bool `json:"ignoreIfNotExists,omitempty"`
}

// DocumentChange represents a union entry of WorkspaceEdit.documentChanges:
// TextDocumentEdit, CreateFile, RenameFile, or DeleteFile.
type DocumentChange struct {
	// TextDocumentEdit fields
	TextDocument *OptionalVersionedTextDocumentIdentifier `json:"textDocument,omitempty"`
	Edits        []TextEdit                               `json:"edits,omitempty"`

	// Resource operation fields
	Kind          string             `json:"kind,omitempty"`
	URI           string             `json:"uri,omitempty"`
	OldURI        string             `json:"oldUri,omitempty"`
	NewURI        string             `json:"newUri,omitempty"`
	CreateOptions *CreateFileOptions `json:"-"`
	RenameOptions *RenameFileOptions `json:"-"`
	DeleteOptions *DeleteFileOptions `json:"-"`
	RawOptions    json.RawMessage    `json:"options,omitempty"`
}

func (dc *DocumentChange) UnmarshalJSON(data []byte) error {
	type alias DocumentChange
	var raw alias
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*dc = DocumentChange(raw)
	if len(dc.RawOptions) > 0 && string(dc.RawOptions) != "null" {
		switch dc.Kind {
		case "create":
			var opts CreateFileOptions
			if err := json.Unmarshal(dc.RawOptions, &opts); err == nil {
				dc.CreateOptions = &opts
			}
		case "rename":
			var opts RenameFileOptions
			if err := json.Unmarshal(dc.RawOptions, &opts); err == nil {
				dc.RenameOptions = &opts
			}
		case "delete":
			var opts DeleteFileOptions
			if err := json.Unmarshal(dc.RawOptions, &opts); err == nil {
				dc.DeleteOptions = &opts
			}
		}
	}
	return nil
}

func (dc DocumentChange) MarshalJSON() ([]byte, error) {
	if dc.Kind == "" && dc.TextDocument != nil {
		return json.Marshal(TextDocumentEdit{
			TextDocument: *dc.TextDocument,
			Edits:        dc.Edits,
		})
	}
	m := map[string]any{"kind": dc.Kind}
	switch dc.Kind {
	case "create":
		m["uri"] = dc.URI
		if dc.CreateOptions != nil {
			m["options"] = dc.CreateOptions
		}
	case "rename":
		m["oldUri"] = dc.OldURI
		m["newUri"] = dc.NewURI
		if dc.RenameOptions != nil {
			m["options"] = dc.RenameOptions
		}
	case "delete":
		m["uri"] = dc.URI
		if dc.DeleteOptions != nil {
			m["options"] = dc.DeleteOptions
		}
	}
	return json.Marshal(m)
}

type WorkspaceEdit struct {
	Changes         map[string][]TextEdit `json:"changes,omitempty"`
	ChangesOrder    []string              `json:"-"`
	DocumentChanges []DocumentChange      `json:"documentChanges,omitempty"`
}

func (w *WorkspaceEdit) UnmarshalJSON(data []byte) error {
	var raw struct {
		Changes         json.RawMessage  `json:"changes,omitempty"`
		DocumentChanges []DocumentChange `json:"documentChanges,omitempty"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	w.DocumentChanges = raw.DocumentChanges
	w.Changes = nil
	w.ChangesOrder = nil
	if len(raw.Changes) > 0 && string(raw.Changes) != "null" {
		var changes map[string][]TextEdit
		if err := json.Unmarshal(raw.Changes, &changes); err != nil {
			return err
		}
		w.Changes = changes
		w.ChangesOrder = extractTopLevelJSONKeys(raw.Changes)
	}
	return nil
}

// orderedChangesURIs returns the URIs in Changes in server wire order when
// decoded from JSON, or sorted deterministically for Go-constructed edits.
func (w *WorkspaceEdit) orderedChangesURIs() []string {
	if w == nil || len(w.Changes) == 0 {
		return nil
	}
	if len(w.ChangesOrder) > 0 {
		out := make([]string, 0, len(w.Changes))
		seen := make(map[string]struct{}, len(w.Changes))
		for _, u := range w.ChangesOrder {
			if _, ok := w.Changes[u]; ok {
				out = append(out, u)
				seen[u] = struct{}{}
			}
		}
		if len(out) < len(w.Changes) {
			var extra []string
			for u := range w.Changes {
				if _, ok := seen[u]; !ok {
					extra = append(extra, u)
				}
			}
			out = append(out, extra...)
		}
		return out
	}
	uris := make([]string, 0, len(w.Changes))
	for u := range w.Changes {
		uris = append(uris, u)
	}
	return uris
}

// =============================================================================
// Code Actions
// =============================================================================

type LSPCommand struct {
	Title     string `json:"title"`
	Command   string `json:"command"`
	Arguments []any  `json:"arguments,omitempty"`
}

type CodeActionDisabled struct {
	Reason string `json:"reason"`
}

// CodeActionOrCommand represents either an LSP CodeAction or Command returned
// from textDocument/codeAction.
type CodeActionOrCommand struct {
	Title       string              `json:"title"`
	Kind        string              `json:"kind,omitempty"`
	Diagnostics []Diagnostic        `json:"diagnostics,omitempty"`
	IsPreferred bool                `json:"isPreferred,omitempty"`
	Disabled    *CodeActionDisabled `json:"disabled,omitempty"`
	Edit        *WorkspaceEdit      `json:"edit,omitempty"`
	// Command is either a string (when this item is itself a Command) or an *LSPCommand (CodeAction.command).
	CommandRaw  json.RawMessage `json:"command,omitempty"`
	Arguments   []any           `json:"arguments,omitempty"`
	Data        any             `json:"data,omitempty"`
	Raw         json.RawMessage `json:"-"`
	IsCommand   bool            `json:"-"`
	CommandName string          `json:"-"`
	SubCommand  *LSPCommand     `json:"-"`
}

func (c *CodeActionOrCommand) UnmarshalJSON(data []byte) error {
	type alias CodeActionOrCommand
	var raw alias
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*c = CodeActionOrCommand(raw)
	c.Raw = append(json.RawMessage(nil), data...)
	if len(c.CommandRaw) > 0 && string(c.CommandRaw) != "null" {
		var name string
		if err := json.Unmarshal(c.CommandRaw, &name); err == nil {
			c.IsCommand = true
			c.CommandName = name
		} else {
			var sub LSPCommand
			if err := json.Unmarshal(c.CommandRaw, &sub); err == nil && sub.Command != "" {
				c.SubCommand = &sub
			}
		}
	}
	return nil
}

// =============================================================================
// Symbols & Hover
// =============================================================================

type SymbolKind int

type DocumentSymbol struct {
	Name           string           `json:"name"`
	Detail         string           `json:"detail,omitempty"`
	Kind           SymbolKind       `json:"kind"`
	Tags           []int            `json:"tags,omitempty"`
	Deprecated     bool             `json:"deprecated,omitempty"`
	Range          Range            `json:"range"`
	SelectionRange Range            `json:"selectionRange"`
	Children       []DocumentSymbol `json:"children,omitempty"`
}

type SymbolInformation struct {
	Name          string     `json:"name"`
	Kind          SymbolKind `json:"kind"`
	Tags          []int      `json:"tags,omitempty"`
	Deprecated    bool       `json:"deprecated,omitempty"`
	Location      Location   `json:"location"`
	ContainerName string     `json:"containerName,omitempty"`
}

type Hover struct {
	Contents json.RawMessage `json:"contents"`
	Range    *Range          `json:"range,omitempty"`
}

// =============================================================================
// Server Configuration & Linter Client Interface
// =============================================================================

type ServerCapabilitiesHints struct {
	Flycheck     bool `json:"flycheck,omitempty"`
	SSR          bool `json:"ssr,omitempty"`
	ExpandMacro  bool `json:"expandMacro,omitempty"`
	Runnables    bool `json:"runnables,omitempty"`
	RelatedTests bool `json:"relatedTests,omitempty"`
}

// ServerConfig describes one configured LSP server or custom CLI linter.
type ServerConfig struct {
	Command         string                   `json:"command"`
	Args            []string                 `json:"args,omitempty"`
	FileTypes       []string                 `json:"fileTypes"`
	LanguageID      string                   `json:"languageId,omitempty"`
	RootMarkers     []string                 `json:"rootMarkers"`
	InitOptions     map[string]any           `json:"initOptions,omitempty"`
	Settings        map[string]any           `json:"settings,omitempty"`
	Disabled        bool                     `json:"disabled,omitempty"`
	WarmupTimeoutMs int                      `json:"warmupTimeoutMs,omitempty"`
	Capabilities    *ServerCapabilitiesHints `json:"capabilities,omitempty"`
	IsLinter        bool                     `json:"isLinter,omitempty"`
	WatchFiles      *bool                    `json:"watchFiles,omitempty"`
	ResolvedCommand string                   `json:"-"`
	CreateClient    LinterClientFactory      `json:"-"`
}

// FileChangeType matches LSP workspace/didChangeWatchedFiles FileChangeType.
type FileChangeType int

const (
	FileChangeCreated FileChangeType = 1
	FileChangeChanged FileChangeType = 2
	FileChangeDeleted FileChangeType = 3
)

type FileEvent struct {
	URI  string         `json:"uri"`
	Type FileChangeType `json:"type"`
}

// LspServerStatus mirrors packages/coding-agent/src/lsp/client.ts:LspServerStatus.
type LspServerStatus struct {
	Name      string   `json:"name"`
	Status    string   `json:"status"`
	FileTypes []string `json:"fileTypes"`
	Error     string   `json:"error,omitempty"`
}

// =============================================================================
// JSON-RPC Framing Types
// =============================================================================

type jsonRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

type jsonRPCMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *jsonRPCError   `json:"error,omitempty"`
}
