package ohmyhermes

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

const (
	// MaestroHandoffSchema is the OMH coding executor handoff contract version.
	MaestroHandoffSchema = "coding_executor_handoff/v1"

	defaultLSPTimeout      = 20 * time.Second
	defaultDispatchTimeout = 120 * time.Second
	maxLSPDiagFiles        = 30
	maxLSPDiagRows         = 60
	maxLSPPreviewRows      = 200
)

// LSPToolsEnabled reports whether the opt-in OMH LSP tools are enabled via OMH_LSP_TOOLS.
func LSPToolsEnabled() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("OMH_LSP_TOOLS")))
	switch v {
	case "1", "true", "on", "yes":
		return true
	default:
		return false
	}
}

// ---------------------------------------------------------------------------
// Pure Go JSON-RPC 2.0 over stdio framing (LSP Content-Length transport)
// ---------------------------------------------------------------------------

// EncodeLSPMessage encodes a JSON-RPC 2.0 payload with an LSP Content-Length header.
func EncodeLSPMessage(payload any) ([]byte, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "Content-Length: %d\r\n\r\n", len(body))
	buf.Write(body)
	return buf.Bytes(), nil
}

// DecodeLSPMessage reads one Content-Length framed JSON-RPC 2.0 message from r.
func DecodeLSPMessage(r *bufio.Reader) ([]byte, error) {
	contentLen := -1
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		trimmed := strings.TrimRight(line, "\r\n")
		if trimmed == "" {
			break
		}
		colon := strings.IndexByte(trimmed, ':')
		if colon < 0 {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(trimmed[:colon]))
		val := strings.TrimSpace(trimmed[colon+1:])
		if key == "content-length" {
			n, err := strconv.Atoi(val)
			if err != nil || n < 0 {
				return nil, fmt.Errorf("invalid Content-Length %q", val)
			}
			contentLen = n
		}
	}
	if contentLen < 0 {
		return nil, errors.New("missing Content-Length header")
	}
	body := make([]byte, contentLen)
	if _, err := io.ReadFull(r, body); err != nil {
		return nil, err
	}
	return body, nil
}

type jsonRPCMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *jsonRPCError   `json:"error,omitempty"`
}

type jsonRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type lspClient struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	reader *bufio.Reader
	mu     sync.Mutex
	nextID atomic.Int64
	diags  map[string][]lspDiagnostic
}

type lspPosition struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

type lspRange struct {
	Start lspPosition `json:"start"`
	End   lspPosition `json:"end"`
}

type lspLocation struct {
	URI   string   `json:"uri"`
	Range lspRange `json:"range"`
}

type lspTextEdit struct {
	Range   lspRange `json:"range"`
	NewText string   `json:"newText"`
}

type lspWorkspaceEdit struct {
	Changes         map[string][]lspTextEdit `json:"changes,omitempty"`
	DocumentChanges []struct {
		TextDocument struct {
			URI string `json:"uri"`
		} `json:"textDocument"`
		Edits []lspTextEdit `json:"edits"`
	} `json:"documentChanges,omitempty"`
}

type lspDiagnostic struct {
	Range    lspRange `json:"range"`
	Severity int      `json:"severity,omitempty"`
	Code     any      `json:"code,omitempty"`
	Source   string   `json:"source,omitempty"`
	Message  string   `json:"message"`
}

func detectLSPServerCommand(filePath string) (string, []string, string, error) {
	lang := languageIDForPath(filePath)
	if override := strings.TrimSpace(os.Getenv("OMH_LSP_SERVER")); override != "" {
		parts := strings.Fields(override)
		if len(parts) > 0 {
			return parts[0], parts[1:], lang, nil
		}
	}
	var candidates [][]string
	switch strings.ToLower(filepath.Ext(filePath)) {
	case ".go":
		candidates = [][]string{{"gopls"}}
	case ".py", ".pyi":
		candidates = [][]string{
			{"pyright-langserver", "--stdio"},
			{"basedpyright-langserver", "--stdio"},
		}
	case ".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs":
		candidates = [][]string{{"typescript-language-server", "--stdio"}}
	case ".rs":
		candidates = [][]string{{"rust-analyzer"}}
	default:
		return "", nil, lang, fmt.Errorf("no language server configured for extension %q (set OMH_LSP_SERVER to override)", filepath.Ext(filePath))
	}
	for _, cand := range candidates {
		if bin, err := exec.LookPath(cand[0]); err == nil {
			return bin, cand[1:], lang, nil
		}
	}
	return "", nil, lang, fmt.Errorf("language server not found on PATH for %s (tried %s; install it or set OMH_LSP_SERVER)", filepath.Ext(filePath), candidates[0][0])
}

func languageIDForPath(filePath string) string {
	switch strings.ToLower(filepath.Ext(filePath)) {
	case ".go":
		return "go"
	case ".py", ".pyi":
		return "python"
	case ".ts":
		return "typescript"
	case ".tsx":
		return "typescriptreact"
	case ".js", ".mjs", ".cjs":
		return "javascript"
	case ".jsx":
		return "javascriptreact"
	case ".rs":
		return "rust"
	default:
		return "plaintext"
	}
}

func pathToFileURI(absPath string) string {
	slash := filepath.ToSlash(absPath)
	if !strings.HasPrefix(slash, "/") {
		slash = "/" + slash
	}
	u := url.URL{Scheme: "file", Path: slash}
	return u.String()
}

func fileURIToPath(rawURI string) string {
	u, err := url.Parse(rawURI)
	if err != nil || u.Scheme != "file" {
		return rawURI
	}
	p := u.Path
	// Handle file:///C:/... on Windows.
	if len(p) >= 3 && p[0] == '/' && ((p[1] >= 'A' && p[1] <= 'Z') || (p[1] >= 'a' && p[1] <= 'z')) && p[2] == ':' {
		p = p[1:]
	}
	return filepath.FromSlash(p)
}

func startLSPClient(ctx context.Context, root, bin string, args []string) (*lspClient, error) {
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = root
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, err
	}
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		return nil, err
	}
	c := &lspClient{
		cmd:    cmd,
		stdin:  stdin,
		reader: bufio.NewReaderSize(stdout, 64*1024),
		diags:  make(map[string][]lspDiagnostic),
	}
	return c, nil
}

func (c *lspClient) close() {
	if c == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	_, _ = c.request(ctx, "shutdown", nil)
	_ = c.notify("exit", nil)
	_ = c.stdin.Close()
	done := make(chan struct{})
	go func() {
		_ = c.cmd.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		if c.cmd.Process != nil {
			_ = c.cmd.Process.Kill()
		}
		<-done
	}
}

func (c *lspClient) writeMsg(msg any) error {
	raw, err := EncodeLSPMessage(msg)
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	_, err = c.stdin.Write(raw)
	return err
}

func (c *lspClient) notify(method string, params any) error {
	return c.writeMsg(map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
		"params":  params,
	})
}

func (c *lspClient) request(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id := c.nextID.Add(1)
	if err := c.writeMsg(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
		"params":  params,
	}); err != nil {
		return nil, err
	}

	type readResult struct {
		data []byte
		err  error
	}
	for {
		ch := make(chan readResult, 1)
		go func() {
			b, err := DecodeLSPMessage(c.reader)
			ch <- readResult{data: b, err: err}
		}()
		var raw []byte
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case res := <-ch:
			if res.err != nil {
				return nil, res.err
			}
			raw = res.data
		}

		var msg jsonRPCMessage
		if err := json.Unmarshal(raw, &msg); err != nil {
			continue
		}
		// Record published diagnostics notifications opportunistically.
		if msg.Method == "textDocument/publishDiagnostics" && len(msg.Params) > 0 {
			var p struct {
				URI         string          `json:"uri"`
				Diagnostics []lspDiagnostic `json:"diagnostics"`
			}
			if json.Unmarshal(msg.Params, &p) == nil && p.URI != "" {
				c.diags[p.URI] = p.Diagnostics
			}
			continue
		}
		// Reply to server-to-client requests so servers don't stall waiting.
		if msg.Method != "" && len(msg.ID) > 0 {
			_ = c.writeMsg(map[string]any{
				"jsonrpc": "2.0",
				"id":      json.RawMessage(msg.ID),
				"result":  nil,
			})
			continue
		}
		if len(msg.ID) == 0 {
			continue
		}
		var respID int64
		if err := json.Unmarshal(msg.ID, &respID); err != nil || respID != id {
			continue
		}
		if msg.Error != nil {
			return nil, fmt.Errorf("LSP error %d: %s", msg.Error.Code, msg.Error.Message)
		}
		return msg.Result, nil
	}
}

func (c *lspClient) initialize(ctx context.Context, root string) error {
	params := map[string]any{
		"processId": os.Getpid(),
		"rootUri":   pathToFileURI(root),
		"rootPath":  root,
		"capabilities": map[string]any{
			"textDocument": map[string]any{
				"definition": map[string]any{"linkSupport": false},
				"references": map[string]any{},
				"rename":     map[string]any{"prepareSupport": false},
				"publishDiagnostics": map[string]any{
					"relatedInformation": false,
				},
				"diagnostic": map[string]any{},
			},
			"workspace": map[string]any{
				"workspaceEdit": map[string]any{"documentChanges": true},
			},
		},
	}
	if _, err := c.request(ctx, "initialize", params); err != nil {
		return err
	}
	return c.notify("initialized", map[string]any{})
}

func (c *lspClient) didOpen(absPath, langID, text string) error {
	return c.notify("textDocument/didOpen", map[string]any{
		"textDocument": map[string]any{
			"uri":        pathToFileURI(absPath),
			"languageId": langID,
			"version":    1,
			"text":       text,
		},
	})
}

// ---------------------------------------------------------------------------
// Workspace path resolution and symbol targeting helpers
// ---------------------------------------------------------------------------

func resolveWorkspaceRoot(ctx sdk.Context) string {
	if cwd := strings.TrimSpace(ctx.Cwd()); cwd != "" {
		if abs, err := filepath.Abs(cwd); err == nil {
			return abs
		}
		return cwd
	}
	if wd, err := os.Getwd(); err == nil {
		return wd
	}
	return "."
}

func resolveConfinedPath(root, rel string) (string, error) {
	rel = strings.TrimSpace(rel)
	if rel == "" {
		return "", errors.New("path is required")
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	var candidate string
	if filepath.IsAbs(rel) {
		candidate = filepath.Clean(rel)
	} else {
		candidate = filepath.Clean(filepath.Join(absRoot, rel))
	}
	r, err := filepath.Rel(absRoot, candidate)
	if err != nil || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q escapes workspace root %q", rel, absRoot)
	}
	if realRoot, err := filepath.EvalSymlinks(absRoot); err == nil {
		if realCand, err := filepath.EvalSymlinks(candidate); err == nil {
			rr, relErr := filepath.Rel(realRoot, realCand)
			if relErr != nil || rr == ".." || strings.HasPrefix(rr, ".."+string(filepath.Separator)) {
				return "", fmt.Errorf("path %q resolves via symlink outside workspace root %q", rel, absRoot)
			}
		}
	}
	return candidate, nil
}

func locateSymbolOnLine(content string, line1 int, symbol string, occurrence int) (int, string, error) {
	if line1 < 1 {
		return 0, "", errors.New("line must be >= 1")
	}
	if strings.TrimSpace(symbol) == "" {
		return 0, "", errors.New("symbol is required")
	}
	if occurrence < 1 {
		occurrence = 1
	}
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	if line1 > len(lines) {
		return 0, "", fmt.Errorf("line %d out of range (file has %d lines)", line1, len(lines))
	}
	lineText := lines[line1-1]
	searchFrom := 0
	found := -1
	for range occurrence {
		idx := strings.Index(lineText[searchFrom:], symbol)
		if idx < 0 {
			return 0, lineText, fmt.Errorf("symbol %q (occurrence %d) not found on line %d", symbol, occurrence, line1)
		}
		found = searchFrom + idx
		searchFrom = found + len(symbol)
	}
	return found, lineText, nil
}

func workspaceRelativeSlash(absPath, root string) string {
	if rel, err := filepath.Rel(root, absPath); err == nil && !strings.HasPrefix(rel, "..") {
		return filepath.ToSlash(rel)
	}
	return filepath.ToSlash(absPath)
}

func parseLocations(raw json.RawMessage, root string) []map[string]any {
	if len(raw) == 0 || string(raw) == "null" {
		return []map[string]any{}
	}
	var locs []lspLocation
	if err := json.Unmarshal(raw, &locs); err != nil {
		var single lspLocation
		if err2 := json.Unmarshal(raw, &single); err2 == nil && single.URI != "" {
			locs = []lspLocation{single}
		}
	}
	out := make([]map[string]any, 0, len(locs))
	for _, loc := range locs {
		if loc.URI == "" {
			continue
		}
		abs := fileURIToPath(loc.URI)
		out = append(out, map[string]any{
			"path":      workspaceRelativeSlash(abs, root),
			"line":      loc.Range.Start.Line + 1,
			"character": loc.Range.Start.Character,
			"end_line":  loc.Range.End.Line + 1,
		})
	}
	return out
}

func applyWorkspaceEdits(root string, edit lspWorkspaceEdit, apply bool) ([]map[string]any, int, error) {
	byFile := make(map[string][]lspTextEdit)
	for uri, edits := range edit.Changes {
		abs := fileURIToPath(uri)
		byFile[abs] = append(byFile[abs], edits...)
	}
	for _, dc := range edit.DocumentChanges {
		if dc.TextDocument.URI == "" {
			continue
		}
		abs := fileURIToPath(dc.TextDocument.URI)
		byFile[abs] = append(byFile[abs], dc.Edits...)
	}

	files := make([]string, 0, len(byFile))
	for f := range byFile {
		if _, err := resolveConfinedPath(root, f); err != nil {
			return nil, 0, fmt.Errorf("refusing edit outside workspace: %w", err)
		}
		files = append(files, f)
	}
	sort.Strings(files)

	previews := make([]map[string]any, 0)
	totalEdits := 0
	for _, abs := range files {
		rawBytes, err := os.ReadFile(abs)
		if err != nil {
			return nil, 0, err
		}
		edits := byFile[abs]
		totalEdits += len(edits)
		// Sort descending by start position so edits apply without shifting earlier offsets.
		sort.SliceStable(edits, func(i, j int) bool {
			if edits[i].Range.Start.Line != edits[j].Range.Start.Line {
				return edits[i].Range.Start.Line > edits[j].Range.Start.Line
			}
			return edits[i].Range.Start.Character > edits[j].Range.Start.Character
		})
		text := string(rawBytes)
		rel := workspaceRelativeSlash(abs, root)
		for _, e := range edits {
			if len(previews) < maxLSPPreviewRows {
				previews = append(previews, map[string]any{
					"path":     rel,
					"line":     e.Range.Start.Line + 1,
					"new_text": e.NewText,
				})
			}
			if apply {
				text = applyOneTextEdit(text, e)
			}
		}
		if apply {
			if err := atomicWriteBytes(abs, []byte(text), 0o644); err != nil {
				return nil, 0, err
			}
		}
	}
	return previews, totalEdits, nil
}

func applyOneTextEdit(content string, e lspTextEdit) string {
	lines := strings.Split(content, "\n")
	offset := func(pos lspPosition) int {
		if pos.Line < 0 {
			return 0
		}
		if pos.Line >= len(lines) {
			return len(content)
		}
		off := 0
		for i := range pos.Line {
			off += len(lines[i]) + 1
		}
		return off + utf16ColToByteOffset(lines[pos.Line], pos.Character)
	}
	start := offset(e.Range.Start)
	end := offset(e.Range.End)
	if start > end || start < 0 || end > len(content) {
		return content
	}
	return content[:start] + e.NewText + content[end:]
}

// utf16ColToByteOffset converts an LSP UTF-16 code-unit column to a byte offset in line.
func utf16ColToByteOffset(line string, col int) int {
	if col <= 0 {
		return 0
	}
	units := 0
	for byteIdx, r := range line {
		if units >= col {
			return byteIdx
		}
		if r >= 0x10000 {
			units += 2
		} else {
			units++
		}
	}
	return len(line)
}

func expandDiagnosticFiles(root, requested string) ([]string, int, error) {
	if !strings.ContainsAny(requested, "*?[") {
		abs, err := resolveConfinedPath(root, requested)
		if err != nil {
			return nil, 0, err
		}
		if _, err := os.Stat(abs); err != nil {
			return nil, 0, err
		}
		return []string{abs}, 0, nil
	}
	pattern := filepath.ToSlash(strings.TrimPrefix(requested, "./"))
	var matches []string
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			if p != root && (strings.HasPrefix(name, ".") || name == "node_modules" || name == "venv" || name == "__pycache__" || name == "vendor") {
				return filepath.SkipDir
			}
			return nil
		}
		rel := workspaceRelativeSlash(p, root)
		if ok, _ := filepath.Match(pattern, rel); ok {
			matches = append(matches, p)
			return nil
		}
		if ok, _ := filepath.Match(filepath.Base(pattern), filepath.Base(rel)); ok && strings.Contains(pattern, "**") {
			matches = append(matches, p)
		}
		return nil
	})
	if err != nil {
		return nil, 0, err
	}
	sort.Strings(matches)
	if len(matches) > maxLSPDiagFiles {
		return matches[:maxLSPDiagFiles], len(matches) - maxLSPDiagFiles, nil
	}
	return matches, 0, nil
}

func lspSeverityName(sev int) string {
	switch sev {
	case 1:
		return "error"
	case 2:
		return "warning"
	case 3:
		return "info"
	case 4:
		return "hint"
	default:
		return "unknown"
	}
}

func lspErrorResult(code, hint string) sdk.ToolResult {
	return jsonToolResult(map[string]any{
		"ok":    false,
		"error": code,
		"hint":  hint,
	}, true)
}

func executeLSPNavigation(ctx sdk.Context, params map[string]any, method string, extraParams map[string]any) (any, error) {
	root := resolveWorkspaceRoot(ctx)
	relPath := paramString(params, "path")
	line := paramInt(params, "line", 0)
	symbol := paramString(params, "symbol")
	occurrence := paramInt(params, "occurrence", 1)

	absPath, err := resolveConfinedPath(root, relPath)
	if err != nil {
		return lspErrorResult("scope_mismatch", err.Error()), nil
	}
	rawBytes, err := os.ReadFile(absPath)
	if err != nil {
		return lspErrorResult("invalid_argument", fmt.Sprintf("read %s: %v", relPath, err)), nil
	}
	col, _, err := locateSymbolOnLine(string(rawBytes), line, symbol, occurrence)
	if err != nil {
		return lspErrorResult("invalid_argument", err.Error()), nil
	}
	bin, args, langID, err := detectLSPServerCommand(absPath)
	if err != nil {
		return lspErrorResult("server_unavailable", err.Error()), nil
	}

	reqCtx, cancel := context.WithTimeout(context.Background(), defaultLSPTimeout)
	defer cancel()
	client, err := startLSPClient(reqCtx, root, bin, args)
	if err != nil {
		return lspErrorResult("server_unavailable", err.Error()), nil
	}
	defer client.close()

	if err := client.initialize(reqCtx, root); err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return lspErrorResult("timeout", "language server timed out during initialize"), nil
		}
		return lspErrorResult("lsp_unavailable", err.Error()), nil
	}
	if err := client.didOpen(absPath, langID, string(rawBytes)); err != nil {
		return lspErrorResult("lsp_unavailable", err.Error()), nil
	}

	reqParams := map[string]any{
		"textDocument": map[string]any{"uri": pathToFileURI(absPath)},
		"position":     map[string]any{"line": line - 1, "character": col},
	}
	for k, v := range extraParams {
		reqParams[k] = v
	}

	resRaw, err := client.request(reqCtx, method, reqParams)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return lspErrorResult("timeout", fmt.Sprintf("LSP %s timed out", method)), nil
		}
		return lspErrorResult("lsp_unavailable", err.Error()), nil
	}

	locs := parseLocations(resRaw, root)
	return jsonToolResult(map[string]any{
		"ok":        true,
		"method":    method,
		"path":      workspaceRelativeSlash(absPath, root),
		"line":      line,
		"symbol":    symbol,
		"locations": locs,
		"count":     len(locs),
	}, false), nil
}

func registerLSPTools(ext *sdk.Extension) {
	if !LSPToolsEnabled() {
		return
	}

	commonProps := map[string]any{
		"path":       map[string]any{"type": "string", "description": "File path relative to workspace root"},
		"line":       map[string]any{"type": "integer", "minimum": 1, "description": "1-based line containing symbol"},
		"symbol":     map[string]any{"type": "string", "description": "Identifier as written on that line"},
		"occurrence": map[string]any{"type": "integer", "minimum": 1, "default": 1, "description": "Nth match on line"},
	}

	ext.RegisterTool(sdk.ToolDefinition{
		Name:        "lsp_definition",
		Label:       "LSP Definition",
		Description: "Go to the definition of the symbol at path:line via the language server (opt-in via OMH_LSP_TOOLS=1).",
		Parameters: sdk.Schema{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"path", "line", "symbol"},
			"properties":           commonProps,
		},
		Execute: func(ctx sdk.Context, params map[string]any) (any, error) {
			return executeLSPNavigation(ctx, params, "textDocument/definition", nil)
		},
	})

	ext.RegisterTool(sdk.ToolDefinition{
		Name:        "lsp_references",
		Label:       "LSP References",
		Description: "Find semantic references to the symbol at path:line across the workspace via the language server (opt-in via OMH_LSP_TOOLS=1).",
		Parameters: sdk.Schema{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"path", "line", "symbol"},
			"properties":           commonProps,
		},
		Execute: func(ctx sdk.Context, params map[string]any) (any, error) {
			return executeLSPNavigation(ctx, params, "textDocument/references", map[string]any{
				"context": map[string]any{"includeDeclaration": true},
			})
		},
	})

	renameProps := map[string]any{
		"path":       commonProps["path"],
		"line":       commonProps["line"],
		"symbol":     commonProps["symbol"],
		"occurrence": commonProps["occurrence"],
		"new_name":   map[string]any{"type": "string", "description": "New identifier name"},
		"apply":      map[string]any{"type": "boolean", "default": true, "description": "false = preview edits without modifying files"},
	}
	ext.RegisterTool(sdk.ToolDefinition{
		Name:        "lsp_rename",
		Label:       "LSP Rename",
		Description: "Rename the symbol at path:line across the workspace via the language server; apply=false previews edits (opt-in via OMH_LSP_TOOLS=1).",
		Parameters: sdk.Schema{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"path", "line", "symbol", "new_name"},
			"properties":           renameProps,
		},
		Execute: func(ctx sdk.Context, params map[string]any) (any, error) {
			root := resolveWorkspaceRoot(ctx)
			relPath := paramString(params, "path")
			line := paramInt(params, "line", 0)
			symbol := paramString(params, "symbol")
			occurrence := paramInt(params, "occurrence", 1)
			newName := paramString(params, "new_name")
			apply := true
			if b, ok := paramBool(params, "apply"); ok {
				apply = b
			}
			if newName == "" {
				return lspErrorResult("invalid_argument", "new_name is required"), nil
			}

			absPath, err := resolveConfinedPath(root, relPath)
			if err != nil {
				return lspErrorResult("scope_mismatch", err.Error()), nil
			}
			rawBytes, err := os.ReadFile(absPath)
			if err != nil {
				return lspErrorResult("invalid_argument", fmt.Sprintf("read %s: %v", relPath, err)), nil
			}
			col, _, err := locateSymbolOnLine(string(rawBytes), line, symbol, occurrence)
			if err != nil {
				return lspErrorResult("invalid_argument", err.Error()), nil
			}
			bin, args, langID, err := detectLSPServerCommand(absPath)
			if err != nil {
				return lspErrorResult("server_unavailable", err.Error()), nil
			}

			reqCtx, cancel := context.WithTimeout(context.Background(), defaultLSPTimeout)
			defer cancel()
			client, err := startLSPClient(reqCtx, root, bin, args)
			if err != nil {
				return lspErrorResult("server_unavailable", err.Error()), nil
			}
			defer client.close()

			if err := client.initialize(reqCtx, root); err != nil {
				return lspErrorResult("lsp_unavailable", err.Error()), nil
			}
			if err := client.didOpen(absPath, langID, string(rawBytes)); err != nil {
				return lspErrorResult("lsp_unavailable", err.Error()), nil
			}

			resRaw, err := client.request(reqCtx, "textDocument/rename", map[string]any{
				"textDocument": map[string]any{"uri": pathToFileURI(absPath)},
				"position":     map[string]any{"line": line - 1, "character": col},
				"newName":      newName,
			})
			if err != nil {
				if errors.Is(err, context.DeadlineExceeded) {
					return lspErrorResult("timeout", "LSP textDocument/rename timed out"), nil
				}
				return lspErrorResult("lsp_unavailable", err.Error()), nil
			}
			var wsEdit lspWorkspaceEdit
			if len(resRaw) > 0 && string(resRaw) != "null" {
				if err := json.Unmarshal(resRaw, &wsEdit); err != nil {
					return lspErrorResult("lsp_unavailable", fmt.Sprintf("decode workspace edit: %v", err)), nil
				}
			}
			previews, totalEdits, err := applyWorkspaceEdits(root, wsEdit, apply)
			if err != nil {
				return lspErrorResult("scope_mismatch", err.Error()), nil
			}
			return jsonToolResult(map[string]any{
				"ok":          true,
				"applied":     apply,
				"symbol":      symbol,
				"new_name":    newName,
				"total_edits": totalEdits,
				"edits":       previews,
			}, false), nil
		},
	})

	ext.RegisterTool(sdk.ToolDefinition{
		Name:        "lsp_diagnostics",
		Label:       "LSP Diagnostics",
		Description: "Collect language-server errors and warnings for a file or workspace glob (opt-in via OMH_LSP_TOOLS=1).",
		Parameters: sdk.Schema{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"path"},
			"properties": map[string]any{
				"path": map[string]any{"type": "string", "description": "File path or glob relative to workspace root"},
			},
		},
		Execute: func(ctx sdk.Context, params map[string]any) (any, error) {
			root := resolveWorkspaceRoot(ctx)
			requested := paramString(params, "path")
			files, omitted, err := expandDiagnosticFiles(root, requested)
			if err != nil {
				return lspErrorResult("scope_mismatch", err.Error()), nil
			}
			if len(files) == 0 {
				return jsonToolResult(map[string]any{
					"ok":            true,
					"diagnostics":   []map[string]any{},
					"files_checked": 0,
					"files_omitted": omitted,
				}, false), nil
			}
			bin, args, _, err := detectLSPServerCommand(files[0])
			if err != nil {
				return lspErrorResult("server_unavailable", err.Error()), nil
			}

			reqCtx, cancel := context.WithTimeout(context.Background(), defaultLSPTimeout)
			defer cancel()
			client, err := startLSPClient(reqCtx, root, bin, args)
			if err != nil {
				return lspErrorResult("server_unavailable", err.Error()), nil
			}
			defer client.close()

			if err := client.initialize(reqCtx, root); err != nil {
				return lspErrorResult("lsp_unavailable", err.Error()), nil
			}

			rows := make([]map[string]any, 0)
			for _, abs := range files {
				data, err := os.ReadFile(abs)
				if err != nil {
					continue
				}
				uri := pathToFileURI(abs)
				_ = client.didOpen(abs, languageIDForPath(abs), string(data))

				// Try pull diagnostics (LSP 3.17 textDocument/diagnostic), falling back to published diagnostics.
				var items []lspDiagnostic
				if resRaw, err := client.request(reqCtx, "textDocument/diagnostic", map[string]any{
					"textDocument": map[string]any{"uri": uri},
				}); err == nil && len(resRaw) > 0 {
					var report struct {
						Items []lspDiagnostic `json:"items"`
					}
					if json.Unmarshal(resRaw, &report) == nil {
						items = report.Items
					}
				}
				if len(items) == 0 {
					items = client.diags[uri]
				}
				rel := workspaceRelativeSlash(abs, root)
				for _, d := range items {
					if len(rows) >= maxLSPDiagRows {
						break
					}
					rows = append(rows, map[string]any{
						"path":     rel,
						"line":     d.Range.Start.Line + 1,
						"severity": lspSeverityName(d.Severity),
						"source":   d.Source,
						"message":  d.Message,
					})
				}
			}

			return jsonToolResult(map[string]any{
				"ok":            true,
				"diagnostics":   rows,
				"count":         len(rows),
				"files_checked": len(files),
				"files_omitted": omitted,
			}, false), nil
		},
	})
}

// ---------------------------------------------------------------------------
// omh_maestro: coding_executor_handoff/v1 store & tool
// ---------------------------------------------------------------------------

// MaestroHandoff persists one coding_executor_handoff/v1 record under $OMH_HOME/runtime/maestro/<id>.json.
type MaestroHandoff struct {
	Schema           string   `json:"schema"`
	HandoffID        string   `json:"handoff_id"`
	Owner            string   `json:"owner"`
	Lane             string   `json:"lane,omitempty"`
	Goal             string   `json:"goal"`
	WriteScope       []string `json:"write_scope,omitempty"`
	AcceptanceChecks []string `json:"acceptance_checks,omitempty"`
	VerifyCommand    string   `json:"verify_command,omitempty"`
	Model            string   `json:"model,omitempty"`
	Workdir          string   `json:"workdir,omitempty"`
	Prompt           string   `json:"prompt,omitempty"`
	Status           string   `json:"status"`
	CreatedAt        string   `json:"created_at"`
	UpdatedAt        string   `json:"updated_at"`
	DispatchedAt     string   `json:"dispatched_at,omitempty"`
	DispatchCommand  []string `json:"dispatch_command,omitempty"`
	DispatchExitCode *int     `json:"dispatch_exit_code,omitempty"`
	DispatchOutput   string   `json:"dispatch_output,omitempty"`
	SessionID        string   `json:"session_id,omitempty"`
}

// MaestroStore persists coding_executor_handoff/v1 records atomically under $OMH_HOME/runtime/maestro/.
type MaestroStore struct {
	mu      sync.Mutex
	pathsFn func() OMHPaths
}

// NewMaestroStore constructs a MaestroStore using pathsFn.
func NewMaestroStore(pathsFn func() OMHPaths) *MaestroStore {
	return &MaestroStore{pathsFn: pathsFn}
}

func (s *MaestroStore) dir() string {
	return filepath.Join(s.pathsFn().RuntimeDir, "maestro")
}

func (s *MaestroStore) recordPath(id string) (string, error) {
	safe, err := sanitizeID(id)
	if err != nil {
		return "", err
	}
	return filepath.Join(s.dir(), safe+".json"), nil
}

// Prepare validates and persists a new coding_executor_handoff/v1 in state prepared_not_observed.
func (s *MaestroStore) Prepare(h MaestroHandoff) (MaestroHandoff, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	h.Owner = strings.TrimSpace(h.Owner)
	h.Goal = strings.TrimSpace(h.Goal)
	if h.Owner == "" {
		return MaestroHandoff{}, errors.New("owner is required (omh_maestro never auto-selects a coding owner)")
	}
	if h.Goal == "" {
		return MaestroHandoff{}, errors.New("goal is required")
	}
	if h.HandoffID == "" {
		h.HandoffID = newShortID("maestro")
	}
	p, err := s.recordPath(h.HandoffID)
	if err != nil {
		return MaestroHandoff{}, err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	h.Schema = MaestroHandoffSchema
	h.Status = "prepared_not_observed"
	if h.CreatedAt == "" {
		h.CreatedAt = now
	}
	h.UpdatedAt = now

	if err := atomicWriteJSON(p, h); err != nil {
		return MaestroHandoff{}, err
	}
	return h, nil
}

// Get loads one MaestroHandoff by ID.
func (s *MaestroStore) Get(id string) (MaestroHandoff, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	p, err := s.recordPath(id)
	if err != nil {
		return MaestroHandoff{}, err
	}
	var h MaestroHandoff
	ok, err := readJSON(p, &h)
	if err != nil {
		return MaestroHandoff{}, err
	}
	if !ok {
		return MaestroHandoff{}, fmt.Errorf("maestro handoff %q not found", id)
	}
	return h, nil
}

// List returns all persisted MaestroHandoff records ordered newest-first.
func (s *MaestroStore) List() ([]MaestroHandoff, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	entries, err := os.ReadDir(s.dir())
	if err != nil {
		if os.IsNotExist(err) {
			return []MaestroHandoff{}, nil
		}
		return nil, err
	}
	out := make([]MaestroHandoff, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		var h MaestroHandoff
		ok, err := readJSON(filepath.Join(s.dir(), entry.Name()), &h)
		if err != nil || !ok {
			continue
		}
		out = append(out, h)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt != out[j].CreatedAt {
			return out[i].CreatedAt > out[j].CreatedAt
		}
		return out[i].HandoffID < out[j].HandoffID
	})
	return out, nil
}

// Dispatch executes a prepared handoff using an explicit local CLI command (or owner binary if found on PATH).
func (s *MaestroStore) Dispatch(id string, command []string, timeoutSec int) (MaestroHandoff, error) {
	h, err := s.Get(id)
	if err != nil {
		return MaestroHandoff{}, err
	}
	if len(command) == 0 {
		bin, args, err := defaultDispatchCommand(h)
		if err != nil {
			return h, err
		}
		command = append([]string{bin}, args...)
	} else {
		if _, err := exec.LookPath(command[0]); err != nil {
			return h, fmt.Errorf("dispatch executable %q not found on PATH: %w", command[0], err)
		}
	}

	timeout := defaultDispatchTimeout
	if timeoutSec > 0 {
		if timeoutSec > 600 {
			timeoutSec = 600
		}
		timeout = time.Duration(timeoutSec) * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, command[0], command[1:]...)
	cmd.WaitDelay = 2 * time.Second
	if strings.TrimSpace(h.Workdir) != "" {
		cmd.Dir = h.Workdir
	}
	var outBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &outBuf

	runErr := cmd.Run()
	exitCode := 0
	if runErr != nil && !errors.Is(runErr, exec.ErrWaitDelay) {
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = -1
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC().Format(time.RFC3339)
	h.DispatchedAt = now
	h.UpdatedAt = now
	h.DispatchCommand = append([]string(nil), command...)
	h.DispatchExitCode = &exitCode
	outStr := outBuf.String()
	if len(outStr) > 16384 {
		outStr = outStr[:16384] + "\n...[truncated]"
	}
	h.DispatchOutput = outStr
	if exitCode == 0 {
		h.Status = "dispatched_succeeded"
	} else {
		h.Status = "dispatched_failed"
	}
	if sid := extractSessionOrThreadID(outStr); sid != "" {
		h.SessionID = sid
	}
	p, err := s.recordPath(h.HandoffID)
	if err != nil {
		return h, err
	}
	if err := atomicWriteJSON(p, h); err != nil {
		return h, err
	}
	return h, nil
}

func defaultDispatchCommand(h MaestroHandoff) (string, []string, error) {
	owner := strings.ToLower(strings.TrimSpace(h.Owner))
	prompt := strings.TrimSpace(h.Prompt)
	if prompt == "" {
		prompt = h.Goal
	}
	switch owner {
	case "codex":
		bin, err := exec.LookPath("codex")
		if err != nil {
			return "", nil, fmt.Errorf("codex CLI not found on PATH; pass an explicit command or install codex")
		}
		args := []string{"exec", "--json"}
		if h.Model != "" {
			args = append(args, "--model", h.Model)
		}
		args = append(args, prompt)
		return bin, args, nil
	case "claude", "claude-code":
		bin, err := exec.LookPath("claude")
		if err != nil {
			return "", nil, fmt.Errorf("claude CLI not found on PATH; pass an explicit command or install claude")
		}
		args := []string{"-p", "--output-format", "json"}
		if h.Model != "" {
			args = append(args, "--model", h.Model)
		}
		args = append(args, prompt)
		return bin, args, nil
	default:
		return "", nil, fmt.Errorf("no default CLI runner for owner %q; provide an explicit command array to dispatch", h.Owner)
	}
}

func extractSessionOrThreadID(output string) string {
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "{") {
			continue
		}
		var obj map[string]any
		if json.Unmarshal([]byte(line), &obj) != nil {
			continue
		}
		if s, ok := obj["session_id"].(string); ok && strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s)
		}
		if s, ok := obj["thread_id"].(string); ok && strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s)
		}
	}
	return ""
}

func registerMaestroTool(ext *sdk.Extension, store *MaestroStore) {
	ext.RegisterTool(sdk.ToolDefinition{
		Name:        "omh_maestro",
		Label:       "OMH Maestro",
		Description: "Prepare, inspect, list, and optionally dispatch OMH coding_executor_handoff/v1 records under $OMH_HOME/runtime/maestro/.",
		Parameters: sdk.Schema{
			"type": "object",
			"properties": map[string]any{
				"action": map[string]any{
					"type":        "string",
					"enum":        []string{"prepare_handoff", "dispatch", "list", "get"},
					"description": "Operation to perform on Maestro handoff records",
				},
				"handoff_id": map[string]any{"type": "string", "description": "Handoff identifier (required for get/dispatch; optional for prepare_handoff)"},
				"owner":      map[string]any{"type": "string", "description": "Explicitly chosen coding owner (e.g. codex, claude, hermes); never auto-selected"},
				"lane":       map[string]any{"type": "string", "description": "Optional lane name owning this handoff"},
				"goal":       map[string]any{"type": "string", "description": "Concrete coding goal for the handoff"},
				"write_scope": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Disjoint file/directory paths this lane may modify",
				},
				"acceptance_checks": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Acceptance criteria and checks required before completion",
				},
				"verify_command": map[string]any{"type": "string", "description": "Exact verification command for this handoff"},
				"model":          map[string]any{"type": "string", "description": "Optional model identifier passed verbatim without silent fallback"},
				"workdir":        map[string]any{"type": "string", "description": "Working directory for dispatch"},
				"prompt":         map[string]any{"type": "string", "description": "Composed executor prompt"},
				"command": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Optional explicit local CLI command argv for dispatch",
				},
				"timeout_s": map[string]any{"type": "integer", "description": "Optional dispatch timeout in seconds"},
			},
			"required": []string{"action"},
		},
		Execute: func(ctx sdk.Context, params map[string]any) (any, error) {
			action := strings.ToLower(paramString(params, "action"))
			switch action {
			case "prepare_handoff":
				workdir := paramString(params, "workdir")
				if workdir == "" {
					workdir = resolveWorkspaceRoot(ctx)
				}
				h, err := store.Prepare(MaestroHandoff{
					HandoffID:        paramString(params, "handoff_id"),
					Owner:            paramString(params, "owner"),
					Lane:             paramString(params, "lane"),
					Goal:             paramString(params, "goal"),
					WriteScope:       paramStringSlice(params, "write_scope"),
					AcceptanceChecks: paramStringSlice(params, "acceptance_checks"),
					VerifyCommand:    paramString(params, "verify_command"),
					Model:            paramString(params, "model"),
					Workdir:          workdir,
					Prompt:           paramString(params, "prompt"),
				})
				if err != nil {
					return errorToolResult(err.Error()), nil
				}
				return jsonToolResult(map[string]any{
					"ok":      true,
					"action":  "prepare_handoff",
					"handoff": h,
				}, false), nil

			case "get":
				id := paramString(params, "handoff_id")
				if id == "" {
					return errorToolResult("handoff_id is required for get"), nil
				}
				h, err := store.Get(id)
				if err != nil {
					return errorToolResult(err.Error()), nil
				}
				return jsonToolResult(map[string]any{
					"ok":      true,
					"action":  "get",
					"handoff": h,
				}, false), nil

			case "list":
				items, err := store.List()
				if err != nil {
					return errorToolResult(err.Error()), nil
				}
				return jsonToolResult(map[string]any{
					"ok":       true,
					"action":   "list",
					"count":    len(items),
					"handoffs": items,
				}, false), nil

			case "dispatch":
				id := paramString(params, "handoff_id")
				if id == "" {
					return errorToolResult("handoff_id is required for dispatch"), nil
				}
				h, err := store.Dispatch(id, paramStringSlice(params, "command"), paramInt(params, "timeout_s", 0))
				if err != nil {
					return errorToolResult(err.Error()), nil
				}
				return jsonToolResult(map[string]any{
					"ok":      true,
					"action":  "dispatch",
					"handoff": h,
				}, false), nil

			default:
				return errorToolResult(fmt.Sprintf("unknown omh_maestro action %q (expected prepare_handoff, dispatch, list, get)", action)), nil
			}
		},
	})
}
