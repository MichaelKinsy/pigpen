package lsp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"
)

// fakeLSPModeEnv activates the in-binary fake LSP server when tests re-exec
// os.Args[0] with `-test.run=TestFakeLSPHelperProcess`.
const fakeLSPModeEnv = "OMP_LSP_TEST_HELPER"

type fakeServerSpec struct {
	Mode              string              `json:"mode,omitempty"` // "standard" | "pull" | "dual" | "no_rename" | "hang_init" | "crash" | "junk_header" | "config_pull" | "slow_diag"
	Capabilities      map[string]any      `json:"capabilities,omitempty"`
	HoverText         string              `json:"hoverText,omitempty"`
	Definitions       []Location          `json:"definitions,omitempty"`
	TypeDefinitions   []Location          `json:"typeDefinitions,omitempty"`
	Implementations   []Location          `json:"implementations,omitempty"`
	References        []Location          `json:"references,omitempty"`
	DocSymbols        []DocumentSymbol    `json:"docSymbols,omitempty"`
	WorkspaceSymbols  []SymbolInformation `json:"workspaceSymbols,omitempty"`
	RenameEdit        *WorkspaceEdit      `json:"renameEdit,omitempty"`
	WillRenameEdit    *WorkspaceEdit      `json:"willRenameEdit,omitempty"`
	CodeActions       []json.RawMessage   `json:"codeActions,omitempty"`
	SourceCodeActions []json.RawMessage   `json:"sourceCodeActions,omitempty"`
	ResolvedAction    json.RawMessage     `json:"resolvedAction,omitempty"`
	FormatEdits       []TextEdit          `json:"formatEdits,omitempty"`
	PushDiagnostics   []Diagnostic        `json:"pushDiagnostics,omitempty"`
	PullDiagnostics   []Diagnostic        `json:"pullDiagnostics,omitempty"`
	PublishVersion    *bool               `json:"publishVersion,omitempty"` // default true
	PIDFile           string              `json:"pidFile,omitempty"`
	ErrorOnPull       bool                `json:"errorOnPull,omitempty"`
	PullNull          bool                `json:"pullNull,omitempty"`
	PullRetriggerOnce bool                `json:"pullRetriggerOnce,omitempty"`
	RawEcho           bool                `json:"rawEcho,omitempty"`
	RegisterDynamic   string              `json:"registerDynamic,omitempty"`
	EchoHoverPos      bool                `json:"echoHoverPos,omitempty"`
	CommandApplyEdit  *WorkspaceEdit      `json:"commandApplyEdit,omitempty"`
}

// runFakeLSPServer speaks JSON-RPC LSP over os.Stdin/os.Stdout for subprocess tests.
func runFakeLSPServer(specJSON string) {
	var spec fakeServerSpec
	if specJSON != "" {
		_ = json.Unmarshal([]byte(specJSON), &spec)
	}
	if spec.PIDFile != "" {
		_ = os.WriteFile(spec.PIDFile, []byte(strconv.Itoa(os.Getpid())), 0o644)
	}

	publishVersion := true
	if spec.PublishVersion != nil {
		publishVersion = *spec.PublishVersion
	}

	writeFrame := func(v any) {
		body, _ := json.Marshal(v)
		_, _ = fmt.Fprintf(os.Stdout, "Content-Length: %d\r\n\r\n%s", len(body), body)
	}
	respond := func(id json.RawMessage, result any, rpcErr *jsonRPCError) {
		msg := map[string]any{"jsonrpc": "2.0", "id": id}
		if rpcErr != nil {
			msg["error"] = rpcErr
		} else {
			msg["result"] = result
		}
		writeFrame(msg)
	}

	docs := make(map[string]string)
	docVersions := make(map[string]int)
	didChangeLog := make(map[string][]int)
	didCloseLog := []string{}
	didSaveLog := []string{}
	executedCommands := []string{}
	receivedMethods := []string{}
	configReplies := []json.RawMessage{}
	var pendingCmdID json.RawMessage
	var pendingApplyEditID string
	retriggered := false
	publish := func(uri string, version int) {
		if spec.Mode == "pull" || spec.Mode == "slow_diag" {
			return
		}
		diags := spec.PushDiagnostics
		if diags == nil {
			// Dynamic diagnostic check: report an error if file contains "SYNTAX_ERROR" or "TYPE_ERROR".
			text := docs[uri]
			diags = []Diagnostic{}
			for i, line := range strings.Split(text, "\n") {
				if idx := strings.Index(line, "TYPE_ERROR"); idx >= 0 {
					diags = append(diags, Diagnostic{
						Range: Range{
							Start: Position{Line: i, Character: idx},
							End:   Position{Line: i, Character: idx + len("TYPE_ERROR")},
						},
						Severity: SeverityError,
						Source:   "fake-lsp",
						Code:     "E001",
						Message:  "type mismatch on TYPE_ERROR",
					})
				}
				if idx := strings.Index(line, "WARN_UNUSED"); idx >= 0 {
					diags = append(diags, Diagnostic{
						Range: Range{
							Start: Position{Line: i, Character: idx},
							End:   Position{Line: i, Character: idx + len("WARN_UNUSED")},
						},
						Severity: SeverityWarning,
						Source:   "fake-lsp",
						Code:     "W001",
						Message:  "unused variable WARN_UNUSED",
					})
				}
			}
		}
		params := map[string]any{
			"uri":         uri,
			"diagnostics": diags,
		}
		if publishVersion {
			params["version"] = version
		}
		writeFrame(map[string]any{
			"jsonrpc": "2.0",
			"method":  "textDocument/publishDiagnostics",
			"params":  params,
		})
	}

	reader := bufio.NewReader(os.Stdin)
	for {
		// Read headers
		contentLength := -1
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			line = strings.TrimRight(line, "\r\n")
			if line == "" {
				break
			}
			if idx := strings.IndexByte(line, ':'); idx != -1 {
				if strings.EqualFold(strings.TrimSpace(line[:idx]), "Content-Length") {
					n, _ := strconv.Atoi(strings.TrimSpace(line[idx+1:]))
					contentLength = n
				}
			}
		}
		if contentLength < 0 {
			continue
		}
		body := make([]byte, contentLength)
		if _, err := io.ReadFull(reader, body); err != nil {
			return
		}
		var msg jsonRPCMessage
		if err := json.Unmarshal(body, &msg); err != nil {
			continue
		}

		if msg.Method == "" {
			// Response to a server->client request.
			configReplies = append(configReplies, msg.Result)
			if pendingApplyEditID != "" {
				var gotID string
				if err := json.Unmarshal(msg.ID, &gotID); err == nil && gotID == pendingApplyEditID {
					pendingApplyEditID = ""
					var reply any
					_ = json.Unmarshal(msg.Result, &reply)
					respond(pendingCmdID, reply, nil)
					pendingCmdID = nil
				}
			}
			continue
		}
		receivedMethods = append(receivedMethods, msg.Method)

		switch msg.Method {
		case "initialize":
			if spec.Mode == "hang_init" {
				// Never answer initialize; sleep so Go's runtime deadlock detector
				// does not kill the helper before the caller cancels/kills it.
				time.Sleep(time.Hour)
				return
			}
			if spec.Mode == "crash" {
				_, _ = fmt.Fprintln(os.Stderr, "simulated server crash on initialize")
				os.Exit(23)
			}
			if spec.Mode == "junk_header" {
				_, _ = os.Stdout.WriteString("Junk: noise-before-jsonrpc\r\n\r\n")
			}
			caps := spec.Capabilities
			if caps == nil {
				caps = map[string]any{
					"textDocumentSync":           map[string]any{"openClose": true, "change": 1, "save": map[string]any{"includeText": true}},
					"hoverProvider":              true,
					"definitionProvider":         true,
					"typeDefinitionProvider":     true,
					"implementationProvider":     true,
					"referencesProvider":         true,
					"documentSymbolProvider":     true,
					"workspaceSymbolProvider":    true,
					"renameProvider":             true,
					"codeActionProvider":         map[string]any{"resolveProvider": true},
					"documentFormattingProvider": true,
				}
				if spec.Mode == "pull" || spec.Mode == "dual" {
					caps["diagnosticProvider"] = map[string]any{"identifier": "fake-pull"}
				}
			}
			respond(msg.ID, map[string]any{
				"capabilities": caps,
				"serverInfo":   map[string]any{"name": "fake-lsp", "version": strconv.Itoa(os.Getpid())},
			}, nil)
			if spec.Mode != "slow_diag" {
				writeFrame(map[string]any{
					"jsonrpc": "2.0",
					"method":  "$/progress",
					"params":  map[string]any{"token": "init", "value": map[string]any{"kind": "begin"}},
				})
				writeFrame(map[string]any{
					"jsonrpc": "2.0",
					"method":  "$/progress",
					"params":  map[string]any{"token": "init", "value": map[string]any{"kind": "end"}},
				})
			}

		case "initialized":
			if spec.Mode == "wedge_stdin" {
				time.Sleep(time.Hour)
				return
			}
			if spec.Mode == "config_pull" {
				// Send a server->client workspace/configuration request with colliding id=1.
				writeFrame(map[string]any{
					"jsonrpc": "2.0",
					"id":      1,
					"method":  "workspace/configuration",
					"params": map[string]any{
						"items": []map[string]string{{"section": "myLang"}, {"section": "missingSection"}},
					},
				})
			}
			if spec.RegisterDynamic != "" {
				writeFrame(map[string]any{
					"jsonrpc": "2.0",
					"id":      "dyn-1",
					"method":  "client/registerCapability",
					"params": map[string]any{
						"registrations": []map[string]any{
							{"id": "reg-1", "method": spec.RegisterDynamic},
						},
					},
				})
			}

		case "textDocument/didOpen":
			var p struct {
				TextDocument struct {
					URI        string `json:"uri"`
					LanguageID string `json:"languageId"`
					Version    int    `json:"version"`
					Text       string `json:"text"`
				} `json:"textDocument"`
			}
			_ = json.Unmarshal(msg.Params, &p)
			docs[p.TextDocument.URI] = p.TextDocument.Text
			docVersions[p.TextDocument.URI] = p.TextDocument.Version
			publish(p.TextDocument.URI, p.TextDocument.Version)

		case "textDocument/didChange":
			var p struct {
				TextDocument struct {
					URI     string `json:"uri"`
					Version int    `json:"version"`
				} `json:"textDocument"`
				ContentChanges []struct {
					Text string `json:"text"`
				} `json:"contentChanges"`
			}
			_ = json.Unmarshal(msg.Params, &p)
			if len(p.ContentChanges) > 0 {
				docs[p.TextDocument.URI] = p.ContentChanges[len(p.ContentChanges)-1].Text
			}
			docVersions[p.TextDocument.URI] = p.TextDocument.Version
			didChangeLog[p.TextDocument.URI] = append(didChangeLog[p.TextDocument.URI], p.TextDocument.Version)
			publish(p.TextDocument.URI, p.TextDocument.Version)

		case "textDocument/didSave":
			var p struct {
				TextDocument struct {
					URI string `json:"uri"`
				} `json:"textDocument"`
			}
			_ = json.Unmarshal(msg.Params, &p)
			didSaveLog = append(didSaveLog, p.TextDocument.URI)

		case "textDocument/didClose":
			var p struct {
				TextDocument struct {
					URI string `json:"uri"`
				} `json:"textDocument"`
			}
			_ = json.Unmarshal(msg.Params, &p)
			didCloseLog = append(didCloseLog, p.TextDocument.URI)
			delete(docs, p.TextDocument.URI)

		case "textDocument/diagnostic":
			if spec.PullRetriggerOnce && !retriggered {
				retriggered = true
				respond(msg.ID, nil, &jsonRPCError{
					Code:    lspServerCancelled,
					Message: "Server cancelled, retrigger",
					Data:    map[string]any{"retriggerRequest": true},
				})
				continue
			}
			if spec.ErrorOnPull {
				respond(msg.ID, nil, &jsonRPCError{Code: -32603, Message: "Internal server error"})
				continue
			}
			if spec.PullNull {
				respond(msg.ID, nil, nil)
				continue
			}
			items := spec.PullDiagnostics
			if items == nil {
				items = []Diagnostic{}
			}
			respond(msg.ID, map[string]any{"kind": "full", "items": items}, nil)

		case "textDocument/hover":
			if spec.EchoHoverPos {
				var p struct {
					Position Position `json:"position"`
				}
				_ = json.Unmarshal(msg.Params, &p)
				respond(msg.ID, map[string]any{
					"contents": map[string]string{
						"kind":  "plaintext",
						"value": fmt.Sprintf("pos=%d:%d", p.Position.Line, p.Position.Character),
					},
				}, nil)
				continue
			}
			if spec.HoverText == "" {
				respond(msg.ID, nil, nil)
			} else {
				respond(msg.ID, map[string]any{
					"contents": map[string]string{"kind": "markdown", "value": spec.HoverText},
				}, nil)
			}

		case "textDocument/definition":
			respond(msg.ID, spec.Definitions, nil)

		case "textDocument/typeDefinition":
			respond(msg.ID, spec.TypeDefinitions, nil)

		case "textDocument/implementation":
			respond(msg.ID, spec.Implementations, nil)

		case "textDocument/references":
			respond(msg.ID, spec.References, nil)

		case "textDocument/documentSymbol":
			respond(msg.ID, spec.DocSymbols, nil)

		case "workspace/symbol":
			respond(msg.ID, spec.WorkspaceSymbols, nil)

		case "textDocument/rename":
			if spec.Mode == "no_rename" {
				respond(msg.ID, nil, &jsonRPCError{Code: -32601, Message: "Method not found: textDocument/rename"})
				continue
			}
			respond(msg.ID, spec.RenameEdit, nil)

		case "workspace/willRenameFiles":
			if spec.WillRenameEdit == nil {
				respond(msg.ID, nil, &jsonRPCError{Code: -32601, Message: "Method not found"})
			} else {
				respond(msg.ID, spec.WillRenameEdit, nil)
			}

		case "textDocument/codeAction":
			var p struct {
				Context struct {
					Only []string `json:"only"`
				} `json:"context"`
			}
			_ = json.Unmarshal(msg.Params, &p)
			if len(p.Context.Only) == 1 && p.Context.Only[0] == "source" {
				respond(msg.ID, spec.SourceCodeActions, nil)
			} else {
				respond(msg.ID, spec.CodeActions, nil)
			}

		case "codeAction/resolve":
			if len(spec.ResolvedAction) > 0 {
				respond(msg.ID, spec.ResolvedAction, nil)
			} else {
				respond(msg.ID, msg.Params, nil)
			}

		case "workspace/executeCommand":
			var p struct {
				Command string `json:"command"`
			}
			_ = json.Unmarshal(msg.Params, &p)
			executedCommands = append(executedCommands, p.Command)
			if spec.CommandApplyEdit != nil {
				pendingCmdID = append(json.RawMessage(nil), msg.ID...)
				pendingApplyEditID = "srv-apply-1"
				writeFrame(map[string]any{
					"jsonrpc": "2.0",
					"id":      pendingApplyEditID,
					"method":  "workspace/applyEdit",
					"params":  map[string]any{"edit": spec.CommandApplyEdit},
				})
				continue
			}
			respond(msg.ID, nil, nil)
		case "textDocument/formatting":
			respond(msg.ID, spec.FormatEdits, nil)

		case "test/state":
			respond(msg.ID, map[string]any{
				"pid":              os.Getpid(),
				"docs":             docs,
				"docVersions":      docVersions,
				"didChangeLog":     didChangeLog,
				"didCloseLog":      didCloseLog,
				"didSaveLog":       didSaveLog,
				"executedCommands": executedCommands,
				"receivedMethods":  receivedMethods,
				"configReplies":    configReplies,
			}, nil)

		case "test/echo":
			var params any
			_ = json.Unmarshal(msg.Params, &params)
			respond(msg.ID, params, nil)

		case "test/triggerApplyEdit":
			if spec.CommandApplyEdit != nil {
				pendingCmdID = append(json.RawMessage(nil), msg.ID...)
				pendingApplyEditID = "srv-apply-raw"
				writeFrame(map[string]any{
					"jsonrpc": "2.0",
					"id":      pendingApplyEditID,
					"method":  "workspace/applyEdit",
					"params":  map[string]any{"edit": spec.CommandApplyEdit},
				})
				continue
			}
			respond(msg.ID, nil, nil)

		case "shutdown":
			if spec.Mode == "hang_shutdown" {
				time.Sleep(time.Hour)
				return
			}
			respond(msg.ID, nil, nil)

		case "exit":
			os.Exit(0)
		default:
			if spec.RawEcho {
				var params any
				_ = json.Unmarshal(msg.Params, &params)
				respond(msg.ID, map[string]any{"method": msg.Method, "params": params}, nil)
			} else {
				respond(msg.ID, nil, &jsonRPCError{Code: -32601, Message: "Method not found: " + msg.Method})
			}
		}
	}
}
