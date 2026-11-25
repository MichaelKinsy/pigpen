package lsp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// upstream: packages/coding-agent/src/lsp/tool.ts

const (
	maxRenamePairs     = 1000
	rustupProxyProbeMs = 5 * time.Second
	untrustedConfigMsg = "note: project LSP configuration and project-local binaries were skipped because this project is not trusted (using only user configuration and PATH)"
)

type actionProvider struct {
	capability string
	method     string
}

var actionProviders = map[string]actionProvider{
	"definition":      {capability: "definitionProvider", method: "textDocument/definition"},
	"type_definition": {capability: "typeDefinitionProvider", method: "textDocument/typeDefinition"},
	"implementation":  {capability: "implementationProvider", method: "textDocument/implementation"},
	"references":      {capability: "referencesProvider", method: "textDocument/references"},
	"hover":           {capability: "hoverProvider", method: "textDocument/hover"},
	"code_actions":    {capability: "codeActionProvider", method: "textDocument/codeAction"},
	"symbols":         {capability: "documentSymbolProvider", method: "textDocument/documentSymbol"},
	"rename":          {capability: "renameProvider", method: "textDocument/rename"},
}

// untrustedNoted tracks which (sessionID, cwd) pairs have already emitted the
// one-time untrusted-project note so it prints once per session/workspace.
var (
	untrustedNotedMu sync.Mutex
	untrustedNoted   = make(map[string]map[string]bool) // sessionKey -> cwd -> noted
)

func consumeUntrustedNote(sessionID, cwd string, cfg *LspConfig) string {
	if !configProjectSkipped(cfg) {
		return ""
	}
	// If no sessionID is available, emit the note directly without mutating shared state.
	if sessionID == "" {
		return untrustedConfigMsg
	}
	cleanCwd := filepath.Clean(cwd)
	untrustedNotedMu.Lock()
	defer untrustedNotedMu.Unlock()
	byCwd, ok := untrustedNoted[sessionID]
	if !ok {
		byCwd = make(map[string]bool)
		untrustedNoted[sessionID] = byCwd
	}
	if byCwd[cleanCwd] {
		return ""
	}
	byCwd[cleanCwd] = true
	return untrustedConfigMsg
}

func deleteUntrustedNotedForSession(sessionID string) {
	if sessionID == "" {
		return
	}
	untrustedNotedMu.Lock()
	delete(untrustedNoted, sessionID)
	untrustedNotedMu.Unlock()
}

func clientServes(c *Client, prov *actionProvider) bool {
	if prov == nil {
		return true
	}
	c.mu.Lock()
	noCaps := c.serverCapabilities == nil
	c.mu.Unlock()
	if noCaps {
		return true
	}
	return c.SupportsMethod(prov.capability, prov.method)
}

type selectedClient struct {
	name    string
	config  ServerConfig
	client  *Client
	skipped []string
}

func selectClientForAction(
	ctx context.Context,
	m *ClientManager,
	sessionID string,
	trusted bool,
	idleTimeoutMs int,
	candidates []NamedServer,
	action, cwd string,
) (*selectedClient, []string, error) {
	var prov *actionProvider
	if p, ok := actionProviders[action]; ok {
		prov = &p
	}
	var notes []string
	var fallback *selectedClient
	for _, cand := range candidates {
		client, err := m.GetOrCreateClientForSessionWithTrust(ctx, sessionID, trusted, idleTimeoutMs, cand.Config, cwd, 0)
		if err != nil {
			if len(candidates) == 1 || ctx.Err() != nil {
				return nil, nil, err
			}
			notes = append(notes, fmt.Sprintf("%s: failed to start (%v)", cand.Name, err))
			continue
		}
		if clientServes(client, prov) {
			return &selectedClient{
				name:    cand.Name,
				config:  cand.Config,
				client:  client,
				skipped: notes,
			}, nil, nil
		}
		if fallback == nil {
			fallback = &selectedClient{
				name:   cand.Name,
				config: cand.Config,
				client: client,
			}
		}
		notes = append(notes, fmt.Sprintf("%s: does not implement %s", cand.Name, prov.method))
	}
	if fallback != nil {
		fallback.skipped = notes
		return fallback, nil, nil
	}
	return nil, notes, nil
}

func probeRustupProxy(ctx context.Context, command string) string {
	cargoHome := os.Getenv("CARGO_HOME")
	if cargoHome == "" {
		if home, err := os.UserHomeDir(); err == nil {
			cargoHome = filepath.Join(home, ".cargo")
		}
	}
	if cargoHome == "" || filepath.Dir(command) != filepath.Join(cargoHome, "bin") {
		return ""
	}
	probeCtx, cancel := context.WithTimeout(ctx, rustupProxyProbeMs)
	defer cancel()
	cmd := exec.CommandContext(probeCtx, command, "--version")
	configureCmdCancel(cmd)
	out, err := cmd.CombinedOutput()
	if err == nil {
		return ""
	}
	if ctx.Err() != nil {
		return ""
	}
	trimmed := strings.TrimSpace(string(out))
	if trimmed != "" {
		lines := strings.Split(trimmed, "\n")
		if len(lines) > 2 {
			lines = lines[:2]
		}
		return strings.Join(lines, " ")
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return fmt.Sprintf("exit code %d", exitErr.ExitCode())
	}
	return ""
}

// formatRenameStatPath mirrors packages/coding-agent/src/lsp/tool.ts:formatRenameStatPath.
func formatRenameStatPath(filePath, cwd string) string {
	rel := formatPathRelativeToCwd(filePath, cwd)
	if filepath.IsAbs(rel) || strings.HasPrefix(rel, "/") {
		rel = shortenPath(filePath)
	}
	return strings.ReplaceAll(rel, "\t", " ")
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := range len(s) {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func executeLspTool(ctx context.Context, sdkCtx sdk.Context, m *ClientManager, cwd string, params LspParams) (sdk.ToolResult, error) {
	settings := readSettings(sdkCtx)
	if !settings.Enabled {
		return sdk.ToolResult{}, &ToolError{Message: "LSP is disabled"}
	}

	budget := resolveLspTimeouts(params.Timeout, settings.RequestTimeoutSec, settings.ToolsMaxTimeoutSec)
	timeoutSec := budget.call
	waitBudget := time.Duration(budget.wait) * time.Second

	callCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSec)*time.Second)
	defer cancel()

	sessionID := resolveSessionID(sdkCtx)
	ownerID, releaseOwner := acquireSessionForCall(m, sessionID)
	defer releaseOwner()
	trusted := isProjectTrusted(sdkCtx)
	cfg := GetConfigWithTrust(cwd, trusted)
	idleTimeoutMs := configIdleTimeoutMs(cfg)
	checkTimeoutErr := func(err error, serverName string) error {
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if callCtx.Err() != nil {
			server := ""
			if serverName != "" {
				server = " on " + serverName
			}
			return &ToolError{
				Message: fmt.Sprintf("LSP %s timed out after %ds%s. The server may still be indexing; try again or pass timeout=<larger>.",
					params.Action, timeoutSec, server),
			}
		}
		return nil
	}

	makeResult := func(text string, serverName string, success bool) sdk.ToolResult {
		if note := consumeUntrustedNote(sessionID, cwd, cfg); note != "" {
			if text == "" {
				text = note
			} else {
				text = text + "\n" + note
			}
		}
		reqCopy := params
		return sdk.ToolResult{
			Content: text,
			Details: LspToolDetails{
				ServerName: serverName,
				Action:     params.Action,
				Success:    success,
				Request:    &reqCopy,
			},
		}
	}

	switch params.Action {
	case "status":
		return executeStatus(callCtx, m, cfg, params, makeResult), nil
	case "diagnostics":
		return executeDiagnostics(callCtx, m, ownerID, trusted, idleTimeoutMs, cfg, cwd, params, timeoutSec, waitBudget, checkTimeoutErr, makeResult)
	case "rename_file":
		return executeRenameFile(callCtx, sdkCtx, m, ownerID, trusted, idleTimeoutMs, cfg, cwd, params, checkTimeoutErr, makeResult)
	case "capabilities":
		return executeCapabilities(callCtx, m, ownerID, trusted, idleTimeoutMs, cfg, cwd, params, checkTimeoutErr, makeResult)
	case "request":
		return executeRawRequest(callCtx, sdkCtx, m, ownerID, trusted, idleTimeoutMs, cfg, cwd, params, checkTimeoutErr, makeResult)
	}

	isWorkspace := params.File == "*"
	requiresFile := params.File == "" && params.Action != "reload"
	if requiresFile {
		return makeResult("Error: file parameter required. Use `*` for workspace scope where supported.", "", false), nil
	}

	var resolvedFile string
	if params.File != "" && !isWorkspace {
		resolvedFile = resolveToCwd(params.File, cwd)
	}

	if params.Action == "symbols" && (isWorkspace || resolvedFile == "") {
		return executeWorkspaceSymbols(callCtx, m, ownerID, trusted, idleTimeoutMs, cfg, cwd, params, settings.SymbolPreset, checkTimeoutErr, makeResult)
	}
	if params.Action == "reload" && (isWorkspace || resolvedFile == "") {
		return executeWorkspaceReload(callCtx, m, ownerID, trusted, cwd, params, checkTimeoutErr, makeResult)
	}
	var candidates []NamedServer
	if resolvedFile != "" {
		candidates = getLspServersForFile(cfg, resolvedFile)
	}
	if len(candidates) == 0 {
		msg := "No language server found for this action"
		if resolvedFile != "" {
			msg = describeMissingServers(cfg, resolvedFile)
		}
		return makeResult(msg, "", false), nil
	}

	serverName := candidates[0].Name
	if params.Action == "reload" {
		m.ClearInitFailure(candidates[0].Config, cwd)
	}

	selected, unsupportedNotes, err := selectClientForAction(callCtx, m, ownerID, trusted, idleTimeoutMs, candidates, params.Action, cwd)
	if err != nil {
		if tErr := checkTimeoutErr(err, serverName); tErr != nil {
			return sdk.ToolResult{}, tErr
		}
		return makeResult("LSP error: "+err.Error(), serverName, false), nil
	}
	if selected == nil {
		ext := filepath.Ext(resolvedFile)
		if ext == "" {
			ext = filepath.Base(resolvedFile)
		}
		var b strings.Builder
		fmt.Fprintf(&b, "No configured language server for %s supports %s:", ext, params.Action)
		for _, note := range unsupportedNotes {
			b.WriteString("\n  ")
			b.WriteString(note)
		}
		names := make([]string, len(candidates))
		for i, c := range candidates {
			names[i] = c.Name
		}
		return makeResult(b.String(), strings.Join(names, ", "), false), nil
	}

	serverName = selected.name
	serverConfig := selected.config
	client := selected.client
	skippedNotes := selected.skipped

	isRustAnalyzer := isRustAnalyzerConfig(serverConfig) || serverName == "rust-analyzer"
	needsProjectIndex := resolvedFile != "" && projectIndexedActions[params.Action] && isProjectAwareLspServer(serverConfig)
	rustWorkspaceWait := needsProjectIndex && isRustAnalyzer && hasRustWorkspaceAncestor(resolvedFile)

	reconciledFromDisk := false
	if resolvedFile != "" {
		reconciledFromDisk, err = client.ReconcileFileFromDisk(callCtx, m, resolvedFile)
		if err != nil {
			if tErr := checkTimeoutErr(err, serverName); tErr != nil {
				return sdk.ToolResult{}, tErr
			}
			return makeResult("LSP error: "+err.Error(), serverName, false), nil
		}
	}
	if rustWorkspaceWait {
		if err := client.WaitForProjectLoaded(callCtx); err != nil {
			if tErr := checkTimeoutErr(err, serverName); tErr != nil {
				return sdk.ToolResult{}, tErr
			}
		}
	}

	if resolvedFile != "" && params.Line != nil && params.Symbol == "" &&
		(params.Action == "references" || params.Action == "rename" || params.Action == "definition") &&
		isProjectAwareLspServer(serverConfig) {
		return sdk.ToolResult{}, &ToolError{
			Message: fmt.Sprintf("symbol is required for project-aware %s; pass symbol=<name>, optionally symbol#N for repeated occurrences", params.Action),
		}
	}

	uri := ""
	if resolvedFile != "" {
		uri = fileToUri(resolvedFile)
	}

	usesPosition := params.Action != "symbols" && params.Action != "reload"
	var locatedLine int
	var locatedChar int
	located := false
	if usesPosition && resolvedFile != "" && params.Line == nil && params.Symbol != "" {
		locatedLine, locatedChar, err = findSymbolInFile(resolvedFile, params.Symbol)
		if err != nil {
			return makeResult("LSP error: "+err.Error(), serverName, false), nil
		}
		located = true
	}

	resolvedLine := 1
	if located {
		resolvedLine = locatedLine
	} else if params.Line != nil {
		resolvedLine = *params.Line
	}

	resolvedChar := 0
	if located {
		resolvedChar = locatedChar
	} else if resolvedFile != "" && usesPosition {
		resolvedChar, err = resolveSymbolColumn(resolvedFile, resolvedLine, params.Symbol)
		if err != nil {
			return makeResult("LSP error: "+err.Error(), serverName, false), nil
		}
	}

	pos := Position{Line: resolvedLine - 1, Character: resolvedChar}
	symbolName := ""
	if params.Symbol != "" {
		symbolName, _ = parseSymbolSpec(params.Symbol)
	}

	if needsProjectIndex && !isRustAnalyzer {
		if err := client.WaitForProjectLoaded(callCtx); err != nil {
			if tErr := checkTimeoutErr(err, serverName); tErr != nil {
				return sdk.ToolResult{}, tErr
			}
		}
	}

	if crossFileActions[params.Action] {
		writesEdits := params.Action == "rename" || (params.Action == "code_actions" && params.Apply != nil && *params.Apply)
		if _, err := client.ReconcileOpenFilesFromDisk(callCtx, m, "", writesEdits); err != nil {
			if tErr := checkTimeoutErr(err, serverName); tErr != nil {
				return sdk.ToolResult{}, tErr
			}
			return makeResult("LSP error: "+err.Error(), serverName, false), nil
		}
	}

	var output string
	var actionErr error

	switch params.Action {
	case "definition":
		output, actionErr = handleLocationAction(callCtx, client, "textDocument/definition", "definition", uri, pos, symbolName, serverName, cwd, true)
	case "type_definition":
		output, actionErr = handleLocationAction(callCtx, client, "textDocument/typeDefinition", "type definition", uri, pos, "", serverName, cwd, false)
	case "implementation":
		output, actionErr = handleLocationAction(callCtx, client, "textDocument/implementation", "implementation", uri, pos, "", serverName, cwd, false)
	case "references":
		output, actionErr = handleReferences(callCtx, client, serverConfig, uri, pos, symbolName, serverName, cwd)
	case "hover":
		output, actionErr = handleHover(callCtx, client, uri, pos)
	case "code_actions":
		output, actionErr = handleCodeActions(callCtx, sdkCtx, m, client, serverConfig, uri, pos, cwd, params, reconciledFromDisk, timeoutSec, waitBudget)
	case "symbols":
		output, actionErr = handleDocumentSymbols(callCtx, client, resolvedFile, uri, cwd, params.Query, settings.SymbolPreset)
	case "rename":
		if params.NewName == "" {
			return makeResult("Error: new_name parameter required for rename", serverName, false), nil
		}
		output, actionErr = handleRename(callCtx, sdkCtx, m, client, uri, pos, cwd, params)
	case "reload":
		output, actionErr = reloadServer(callCtx, m, client, serverName)
	default:
		output = "Unknown action: " + params.Action
	}

	if actionErr != nil {
		var toolErr *ToolError
		if errors.As(actionErr, &toolErr) {
			return sdk.ToolResult{}, toolErr
		}
		if tErr := checkTimeoutErr(actionErr, serverName); tErr != nil {
			return sdk.ToolResult{}, tErr
		}
		prov, hasProv := actionProviders[params.Action]
		if params.Action == "code_actions" && params.Apply != nil && *params.Apply {
			hasProv = false
		}
		if hasProv && isMethodNotFoundError(actionErr) {
			ext := filepath.Ext(resolvedFile)
			if ext == "" {
				ext = filepath.Base(resolvedFile)
			}
			seen := make(map[string]struct{})
			var notes []string
			for _, n := range append(skippedNotes, fmt.Sprintf("%s: does not implement %s", serverName, prov.method)) {
				if _, ok := seen[n]; !ok {
					seen[n] = struct{}{}
					notes = append(notes, "  "+n)
				}
			}
			return makeResult(fmt.Sprintf("No configured language server for %s supports %s:\n%s",
				ext, params.Action, strings.Join(notes, "\n")), serverName, false), nil
		}
		return makeResult("LSP error: "+actionErr.Error(), serverName, false), nil
	}

	if located {
		output = fmt.Sprintf("%s at line %d:\n%s", params.Symbol, locatedLine, output)
	}
	client.mu.Lock()
	indexing := len(client.activeProgressTokens) > 0
	client.mu.Unlock()
	if crossFileActions[params.Action] && indexing {
		output += fmt.Sprintf("\nnote: %s is still indexing; results may be incomplete", serverName)
	}
	return makeResult(output, serverName, true), nil
}

// =============================================================================
// Individual Action Handlers
// =============================================================================

func executeStatus(
	ctx context.Context,
	m *ClientManager,
	cfg *LspConfig,
	params LspParams,
	makeResult func(string, string, bool) sdk.ToolResult,
) sdk.ToolResult {
	configured := snapshotAllServers(cfg)
	startedClients := m.ActiveClients()
	startedByName := make(map[string]LspServerStatus)
	for _, s := range configured {
		for _, sc := range startedClients {
			if sc.Name == s.Config.Command {
				startedByName[s.Name] = sc
				break
			}
		}
	}

	if len(configured) == 0 {
		return makeResult("No language servers configured for this project", "", true)
	}
	labelled := make([]string, len(configured))
	for i, s := range configured {
		if st, ok := startedByName[s.Name]; ok {
			labelled[i] = fmt.Sprintf("%s (%s)", s.Name, st.Status)
			continue
		}
		cmd := s.Config.ResolvedCommand
		if cmd == "" {
			cmd = s.Config.Command
		}
		if proxyErr := probeRustupProxy(ctx, cmd); proxyErr != "" {
			labelled[i] = fmt.Sprintf("%s (unusable: %s)", s.Name, proxyErr)
		} else {
			labelled[i] = fmt.Sprintf("%s (configured, not started)", s.Name)
		}
	}
	lines := []string{
		"Language servers: " + strings.Join(labelled, ", "),
		"  note: 'configured, not started' means the binary resolves on PATH but no request has spawned it yet; 'ready' means a client process is live for this cwd.",
	}
	return makeResult(strings.Join(lines, "\n"), "", true)
}

func executeDiagnostics(
	ctx context.Context,
	m *ClientManager,
	sessionID string,
	trusted bool,
	idleTimeoutMs int,
	cfg *LspConfig,
	cwd string,
	params LspParams,
	timeoutSec int,
	waitBudget time.Duration,
	checkTimeoutErr func(error, string) error,
	makeResult func(string, string, bool) sdk.ToolResult,
) (sdk.ToolResult, error) {
	if params.File == "*" {
		out, desc, err := runWorkspaceDiagnostics(ctx, cwd)
		if err != nil {
			if tErr := checkTimeoutErr(err, ""); tErr != nil {
				return sdk.ToolResult{}, tErr
			}
			return sdk.ToolResult{}, err
		}
		return makeResult(fmt.Sprintf("Workspace diagnostics (%s):\n%s", desc, out), "", true), nil
	}
	if params.File == "" {
		return makeResult("Error: file parameter required. Use `*` for workspace-wide diagnostics or a path/glob for specific files.", "", false), nil
	}

	targets, truncated, err := resolveDiagnosticTargets(params.File, cwd, maxGlobDiagnosticTargets)
	if err != nil {
		return makeResult("LSP error: "+err.Error(), "", false), nil
	}
	if len(targets) == 0 {
		return makeResult("No files matched pattern: "+params.File, "", true), nil
	}

	detailed := len(targets) > 1 || truncated
	var results []string
	allServerNames := make(map[string]struct{})
	var allServerOrder []string
	totalAttempts := 0
	totalFailures := 0

	if truncated {
		results = append(results, fmt.Sprintf("⚠ Pattern matched more than %d files; showing first %d. Narrow the glob or use workspace diagnostics.",
			maxGlobDiagnosticTargets, maxGlobDiagnosticTargets))
	}

	for _, target := range targets {
		if err := ctx.Err(); err != nil {
			if tErr := checkTimeoutErr(err, ""); tErr != nil {
				return sdk.ToolResult{}, tErr
			}
			return sdk.ToolResult{}, err
		}
		resolved := resolveToCwd(target, cwd)
		servers := GetServersForFile(cfg, resolved)
		if len(servers) == 0 {
			results = append(results, fmt.Sprintf("✗ %s: %s", target, describeMissingServers(cfg, resolved)))
			continue
		}

		uri := fileToUri(resolved)
		relPath := formatPathRelativeToCwd(resolved, cwd)
		var allDiags []Diagnostic
		var failedServers []string
		var reportedServers []string
		var unreportedServers []string
		succeededServers := 0

		for _, srv := range servers {
			if _, seen := allServerNames[srv.Name]; !seen {
				allServerNames[srv.Name] = struct{}{}
				allServerOrder = append(allServerOrder, srv.Name)
			}
			totalAttempts++

			if srv.Config.CreateClient != nil {
				linter := getLinterClient(srv.Name, srv.Config, cwd)
				diags, lintErr := linter.Lint(ctx, resolved)
				if lintErr != nil {
					if tErr := checkTimeoutErr(lintErr, srv.Name); tErr != nil {
						return sdk.ToolResult{}, tErr
					}
					failedServers = append(failedServers, srv.Name)
					totalFailures++
					continue
				}
				allDiags = append(allDiags, diags...)
				succeededServers++
				reportedServers = append(reportedServers, srv.Name)
				continue
			}

			client, clientErr := m.GetOrCreateClientForSessionWithTrust(ctx, sessionID, trusted, idleTimeoutMs, srv.Config, cwd, 0)
			if clientErr != nil {
				if tErr := checkTimeoutErr(clientErr, srv.Name); tErr != nil {
					return sdk.ToolResult{}, tErr
				}
				failedServers = append(failedServers, srv.Name)
				totalFailures++
				continue
			}

			if _, recErr := client.ReconcileOpenFilesFromDisk(ctx, m, uri, true); recErr != nil {
				if tErr := checkTimeoutErr(recErr, srv.Name); tErr != nil {
					return sdk.ToolResult{}, tErr
				}
				failedServers = append(failedServers, srv.Name)
				totalFailures++
				continue
			}

			minVer := client.DiagnosticsVersion()
			var rebuildSinceSeq *int

			client.mu.Lock()
			cBuildStatus := client.buildStatus != nil
			cPublishesVer := client.publishesDocumentVersion
			cBuildSeq := client.buildStatusSeq
			client.mu.Unlock()

			var syncErr error
			switch {
			case client.SupportsDocumentDiagnostics():
				_, syncErr = client.ReconcileFileFromDisk(ctx, m, resolved)
			case cBuildStatus:
				var rebuilt bool
				rebuilt, syncErr = client.RebuildFile(ctx, m, resolved)
				if rebuilt {
					seq := cBuildSeq
					rebuildSinceSeq = &seq
				}
			case cPublishesVer:
				syncErr = client.ReopenFile(ctx, m, resolved)
			default:
				syncErr = client.RefreshFile(ctx, m, resolved)
			}
			if syncErr != nil {
				if tErr := checkTimeoutErr(syncErr, srv.Name); tErr != nil {
					return sdk.ToolResult{}, tErr
				}
				failedServers = append(failedServers, srv.Name)
				totalFailures++
				continue
			}

			var expectedDocVer *int
			if v, ok := client.OpenFileVersion(uri); ok {
				ev := v
				expectedDocVer = &ev
			}

			waitCap := singleDiagnosticsWaitTimeout
			if detailed {
				waitCap = batchDiagnosticsWaitTimeout
			} else if isProjectAwareLspServer(srv.Config) {
				waitCap = waitBudget
			}
			callCap := time.Duration(timeoutSec) * time.Second
			if callCap < waitCap {
				waitCap = callCap
			}

			var diags []Diagnostic
			var reported bool
			var waitErr error
			if rebuildSinceSeq != nil && expectedDocVer != nil {
				diags, reported, waitErr = waitForRebuiltDiagnostics(ctx, client, uri, *expectedDocVer, *rebuildSinceSeq, waitCap)
			} else {
				mv := minVer
				diags, reported, waitErr = waitForDiagnostics(ctx, client, uri, waitDiagnosticsOptions{
					timeout:                 waitCap,
					minVersion:              &mv,
					expectedDocumentVersion: expectedDocVer,
					afterProjectLoad:        isProjectAwareLspServer(srv.Config),
				})
			}
			if waitErr != nil {
				if tErr := checkTimeoutErr(waitErr, srv.Name); tErr != nil {
					return sdk.ToolResult{}, tErr
				}
				failedServers = append(failedServers, srv.Name)
				totalFailures++
				continue
			}
			if !reported {
				unreportedServers = append(unreportedServers, srv.Name)
				continue
			}
			allDiags = append(allDiags, diags...)
			succeededServers++
			reportedServers = append(reportedServers, srv.Name)
		}

		unique := deduplicateDiagnostics(allDiags)
		sortDiagnostics(unique)

		var notes []string
		if len(unreportedServers) > 0 {
			hint := "retry once it finishes analyzing"
			if detailed {
				hint = "query the file alone for a longer wait"
			}
			notes = append(notes, fmt.Sprintf("⚠ %s: no diagnostics report yet from %s; %s",
				relPath, strings.Join(unreportedServers, ", "), hint))
		}
		if len(failedServers) > 0 && (succeededServers > 0 || len(unreportedServers) > 0) {
			notes = append(notes, fmt.Sprintf("⚠ %s: some servers failed (%s)",
				relPath, strings.Join(failedServers, ", ")))
		}

		var lines []string
		switch {
		case succeededServers == 0 && len(unreportedServers) == 0:
			lines = append(lines, fmt.Sprintf("✗ %s: all language servers failed (%s)",
				relPath, strings.Join(failedServers, ", ")))
		case len(unique) > 0:
			summary := formatDiagnosticsSummary(unique)
			if detailed {
				lines = append(lines, fmt.Sprintf("✗ %s: %s", relPath, summary))
			} else {
				lines = append(lines, summary+":")
			}
			msgs := make([]string, len(unique))
			for i, d := range unique {
				msgs[i] = formatDiagnostic(d, relPath)
			}
			lines = append(lines, formatGroupedDiagnosticMessages(msgs))
		case succeededServers > 0 && len(unreportedServers) > 0:
			partial := "no issues from " + strings.Join(reportedServers, ", ")
			if detailed {
				lines = append(lines, fmt.Sprintf("⚠ %s: %s", relPath, partial))
			} else {
				lines = append(lines, partial)
			}
		case succeededServers > 0:
			if detailed {
				lines = append(lines, fmt.Sprintf("✓ %s: no issues", relPath))
			} else {
				lines = append(lines, "OK")
			}
		}
		lines = append(lines, notes...)

		if !detailed {
			res := makeResult(strings.Join(lines, "\n"), strings.Join(allServerOrder, ", "), succeededServers > 0 || len(unreportedServers) > 0)
			if d, ok := res.Details.(LspToolDetails); ok {
				d.Request = nil // upstream: lsp/tool.ts:646-650 omits request on diagnostics results
				res.Details = d
			}
			return res, nil
		}
		results = append(results, lines...)
	}

	allFailed := totalAttempts > 0 && totalFailures == totalAttempts
	res := makeResult(strings.Join(results, "\n"), strings.Join(allServerOrder, ", "), !allFailed)
	if d, ok := res.Details.(LspToolDetails); ok {
		d.Request = nil // upstream: lsp/tool.ts:659 omits request on diagnostics results
		res.Details = d
	}
	return res, nil
}

func executeRenameFile(
	ctx context.Context,
	sdkCtx sdk.Context,
	m *ClientManager,
	sessionID string,
	trusted bool,
	idleTimeoutMs int,
	cfg *LspConfig,
	cwd string,
	params LspParams,
	checkTimeoutErr func(error, string) error,
	makeResult func(string, string, bool) sdk.ToolResult,
) (sdk.ToolResult, error) {
	if params.File == "" || params.NewName == "" {
		return makeResult("Error: rename_file requires both `file` (source path) and `new_name` (destination path)", "", false), nil
	}
	source := resolveToCwd(params.File, cwd)
	var dest string
	if strings.ContainsAny(params.NewName, `/\`) {
		dest = resolveToCwd(params.NewName, cwd)
	} else {
		dest = filepath.Join(filepath.Dir(source), params.NewName)
	}
	if source == dest {
		return makeResult("Error: source and destination paths are identical", "", false), nil
	}

	sourceStat, err := os.Stat(source)
	if err != nil {
		relSrc := formatRenameStatPath(source, cwd)
		if os.IsNotExist(err) {
			return makeResult("Error: source path does not exist: "+relSrc, "", false), nil
		}
		return makeResult(fmt.Sprintf("Error: cannot read source path %s: %s", relSrc, formatRenameStatError(err)), "", false), nil
	}
	if _, err := os.Lstat(dest); err == nil {
		return makeResult("Error: destination already exists: "+formatRenameStatPath(dest, cwd), "", false), nil
	} else if !os.IsNotExist(err) {
		return makeResult(fmt.Sprintf("Error: cannot read destination path %s: %s", formatRenameStatPath(dest, cwd), formatRenameStatError(err)), "", false), nil
	}

	type renamePair struct {
		OldURI string `json:"oldUri"`
		NewURI string `json:"newUri"`
	}
	var pairs []renamePair
	if !sourceStat.IsDir() {
		pairs = []renamePair{{OldURI: fileToUri(source), NewURI: fileToUri(dest)}}
	} else {
		exceeded := false
		_ = filepath.WalkDir(source, func(p string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return nil
			}
			// Match omp tool.ts:155: pair regular files only (skip dirs, symlinks, FIFOs, etc.).
			if !d.Type().IsRegular() {
				return nil
			}
			if len(pairs) >= maxRenamePairs {
				exceeded = true
				return fs.SkipAll
			}
			rel, _ := filepath.Rel(source, p)
			pairs = append(pairs, renamePair{
				OldURI: fileToUri(p),
				NewURI: fileToUri(filepath.Join(dest, rel)),
			})
			return nil
		})
		if exceeded {
			return makeResult(fmt.Sprintf("Error: directory contains more than %d files; rename in smaller batches to keep LSP edits accurate", maxRenamePairs), "", false), nil
		}
	}
	if len(pairs) == 0 {
		return makeResult("Error: no files to rename", "", false), nil
	}

	relevantNames := make(map[string]struct{})
	collectRelevant := func(p string) {
		for _, s := range getLspServersForFile(cfg, p) {
			relevantNames[s.Name] = struct{}{}
		}
	}
	collectRelevant(source)
	collectRelevant(dest)
	for _, pair := range pairs {
		collectRelevant(uriToFile(pair.OldURI))
		collectRelevant(uriToFile(pair.NewURI))
	}

	var servers []NamedServer
	for _, s := range getLspServers(cfg) {
		if _, ok := relevantNames[s.Name]; ok {
			servers = append(servers, s)
		}
	}

	type serverEdit struct {
		serverName string
		edit       *WorkspaceEdit
	}
	var responding []string
	var perServerEdits []serverEdit
	var serverNotes []string
	var hardFailures []string
	var nonRenaming []string

	lspParams := map[string]any{"files": pairs}
	for _, srv := range servers {
		if err := ctx.Err(); err != nil {
			if tErr := checkTimeoutErr(err, srv.Name); tErr != nil {
				return sdk.ToolResult{}, tErr
			}
			return sdk.ToolResult{}, err
		}
		client, clientErr := m.GetOrCreateClientForSessionWithTrust(ctx, sessionID, trusted, idleTimeoutMs, srv.Config, cwd, 0)
		if clientErr != nil {
			if tErr := checkTimeoutErr(clientErr, srv.Name); tErr != nil {
				return sdk.ToolResult{}, tErr
			}
			serverNotes = append(serverNotes, fmt.Sprintf("  %s: %v", srv.Name, clientErr))
			continue
		}
		if isProjectAwareLspServer(srv.Config) {
			_ = client.WaitForProjectLoaded(ctx)
		}
		if _, recErr := client.ReconcileOpenFilesFromDisk(ctx, m, "", true); recErr != nil {
			serverNotes = append(serverNotes, fmt.Sprintf("  %s: %v", srv.Name, recErr))
			hardFailures = append(hardFailures, srv.Name)
			continue
		}
		raw, reqErr := client.SendRequest(ctx, "workspace/willRenameFiles", lspParams, 0)
		if reqErr != nil {
			if tErr := checkTimeoutErr(reqErr, srv.Name); tErr != nil {
				return sdk.ToolResult{}, tErr
			}
			if isMethodNotFoundError(reqErr) {
				nonRenaming = append(nonRenaming, srv.Name)
			} else {
				serverNotes = append(serverNotes, fmt.Sprintf("  %s: %v", srv.Name, reqErr))
				hardFailures = append(hardFailures, srv.Name)
			}
			continue
		}
		responding = append(responding, srv.Name)
		if len(raw) > 0 && string(raw) != "null" {
			var edit WorkspaceEdit
			if err := json.Unmarshal(raw, &edit); err == nil && (len(edit.Changes) > 0 || len(edit.DocumentChanges) > 0) {
				perServerEdits = append(perServerEdits, serverEdit{serverName: srv.Name, edit: &edit})
			}
		}
	}

	sourceLabel := formatPathRelativeToCwd(source, cwd)
	destLabel := formatPathRelativeToCwd(dest, cwd)
	fileCountLabel := sourceLabel
	if sourceStat.IsDir() {
		plural := "s"
		if len(pairs) == 1 {
			plural = ""
		}
		fileCountLabel = fmt.Sprintf("%d file%s under %s", len(pairs), plural, sourceLabel)
	}

	importsNote := ""
	if len(responding) == 0 {
		if len(nonRenaming) > 0 {
			verb := "do"
			if len(nonRenaming) == 1 {
				verb = "does"
			}
			importsNote = fmt.Sprintf("imports NOT rewritten: %s %s not implement workspace/willRenameFiles",
				strings.Join(nonRenaming, ", "), verb)
		} else {
			importsNote = "imports NOT rewritten: no language server handles these files"
		}
	}

	shouldApply := params.Apply == nil || *params.Apply
	if !shouldApply {
		suffix := ""
		if importsNote != "" {
			suffix = "; " + importsNote
		}
		lines := []string{fmt.Sprintf("Rename preview: %s → %s%s", fileCountLabel, destLabel, suffix)}
		if len(perServerEdits) == 0 {
			lines = append(lines, "  No LSP edits would be applied")
		} else {
			for _, se := range perServerEdits {
				edits := formatWorkspaceEdit(se.edit, cwd)
				if len(edits) == 0 {
					continue
				}
				lines = append(lines, "  "+se.serverName+":")
				for _, e := range edits {
					lines = append(lines, "    "+e)
				}
			}
		}
		if len(serverNotes) > 0 {
			lines = append(lines, "  Server notes:")
			lines = append(lines, serverNotes...)
		}
		return makeResult(strings.Join(lines, "\n"), strings.Join(responding, ", "), true), nil
	}

	if len(hardFailures) > 0 {
		lines := []string{
			fmt.Sprintf("Error: aborted rename; workspace/willRenameFiles failed on %s, so semantic references would not be updated. No files were moved.",
				strings.Join(hardFailures, ", ")),
			"  Server notes:",
		}
		lines = append(lines, serverNotes...)
		return makeResult(strings.Join(lines, "\n"), strings.Join(responding, ", "), false), nil
	}

	// Coalesce per-URI edits across servers, preferring project-aware primary servers on overlap.
	serverCfgByName := make(map[string]ServerConfig, len(servers))
	for _, s := range servers {
		serverCfgByName[s.Name] = s.Config
	}
	type bucket struct {
		primaryServer   string
		edits           []TextEdit
		discarded       int
		conflictServers map[string]struct{}
	}
	acceptedByURI := make(map[string]*bucket)
	var uriOrder []string

	for _, se := range perServerEdits {
		inPrimary := isProjectAwareLspServer(serverCfgByName[se.serverName])
		flat, order := flattenWorkspaceTextEdits(se.edit)
		for _, uri := range order {
			edits := flat[uri]
			existing, ok := acceptedByURI[uri]
			if !ok {
				acceptedByURI[uri] = &bucket{
					primaryServer:   se.serverName,
					edits:           append([]TextEdit(nil), edits...),
					conflictServers: make(map[string]struct{}),
				}
				uriOrder = append(uriOrder, uri)
				continue
			}
			existingPrimary := isProjectAwareLspServer(serverCfgByName[existing.primaryServer])
			if inPrimary && !existingPrimary {
				var keptOld []TextEdit
				discardedOld := 0
				for _, oe := range existing.edits {
					overlap := false
					for _, ne := range edits {
						if rangesOverlap(ne.Range, oe.Range) {
							overlap = true
							break
						}
					}
					if overlap {
						discardedOld++
					} else {
						keptOld = append(keptOld, oe)
					}
				}
				if discardedOld > 0 {
					existing.conflictServers[existing.primaryServer] = struct{}{}
				}
				existing.discarded += discardedOld
				existing.primaryServer = se.serverName
				existing.edits = append(append([]TextEdit(nil), edits...), keptOld...)
			} else {
				discardedNew := 0
				for _, ne := range edits {
					overlap := false
					for _, ae := range existing.edits {
						if rangesOverlap(ae.Range, ne.Range) {
							overlap = true
							break
						}
					}
					if overlap {
						discardedNew++
					} else {
						existing.edits = append(existing.edits, ne)
					}
				}
				if discardedNew > 0 {
					existing.conflictServers[se.serverName] = struct{}{}
					existing.discarded += discardedNew
				}
			}
		}
	}

	var refEdits []renameReferenceEdit
	var summary []string
	for _, uri := range uriOrder {
		b := acceptedByURI[uri]
		if _, err := sortAndValidateTextEdits(b.edits); err != nil {
			return sdk.ToolResult{}, err
		}
		fp := uriToFile(uri)
		refEdits = append(refEdits, renameReferenceEdit{filePath: fp, edits: b.edits})
		rel := formatPathRelativeToCwd(fp, cwd)
		summary = append(summary, fmt.Sprintf("  %s: applied %d edit(s) to %s", b.primaryServer, len(b.edits), rel))
		if b.discarded > 0 {
			var others []string
			for name := range b.conflictServers {
				others = append(others, name)
			}
			sort.Strings(others)
			summary = append(summary, fmt.Sprintf("    note: discarded %d overlapping edit(s) from %s (kept %s)",
				b.discarded, strings.Join(others, ", "), b.primaryServer))
		}
	}

	// Let applyEditsThenRename errors (including gate denials and fs errors) propagate as tool errors (tool.ts:1018).
	if err := applyEditsThenRename(sdkCtx, refEdits, source, dest); err != nil {
		return sdk.ToolResult{}, err
	}
	summary = append(summary, fmt.Sprintf("  Renamed %s → %s", sourceLabel, destLabel))

	for _, srv := range servers {
		client, err := m.GetOrCreateClientForSessionWithTrust(ctx, sessionID, trusted, idleTimeoutMs, srv.Config, cwd, 0)
		if err != nil {
			continue
		}
		for _, pair := range pairs {
			key := canonicalURIKey(pair.OldURI)
			client.mu.Lock()
			_, open := client.openFiles[key]
			client.mu.Unlock()
			if open {
				_ = client.CloseDocument(ctx, pair.OldURI)
			}
		}
		_ = client.SendNotification(ctx, "workspace/didRenameFiles", lspParams)
	}

	if len(serverNotes) > 0 {
		summary = append(summary, "  Server notes:")
		summary = append(summary, serverNotes...)
	}

	suffix := ""
	if importsNote != "" {
		suffix = "; " + importsNote
	}
	header := fmt.Sprintf("Renamed %s → %s%s", fileCountLabel, destLabel, suffix)
	return makeResult(header+"\n"+strings.Join(summary, "\n"), strings.Join(responding, ", "), true), nil
}

func executeCapabilities(
	ctx context.Context,
	m *ClientManager,
	sessionID string,
	trusted bool,
	idleTimeoutMs int,
	cfg *LspConfig,
	cwd string,
	params LspParams,
	checkTimeoutErr func(error, string) error,
	makeResult func(string, string, bool) sdk.ToolResult,
) (sdk.ToolResult, error) {
	var serverList []NamedServer
	if params.File != "" && params.File != "*" {
		resolved := resolveToCwd(params.File, cwd)
		serverList = getLspServersForFile(cfg, resolved)
		if len(serverList) == 0 {
			return makeResult("No language server found for this file", "", false), nil
		}
	} else {
		serverList = getLspServers(cfg)
	}
	if len(serverList) == 0 {
		return makeResult("No language servers configured", "", false), nil
	}
	var sections []string
	var responding []string
	for _, srv := range serverList {
		client, err := m.GetOrCreateClientForSessionWithTrust(ctx, sessionID, trusted, idleTimeoutMs, srv.Config, cwd, 0)
		if err != nil {
			if tErr := checkTimeoutErr(err, srv.Name); tErr != nil {
				return sdk.ToolResult{}, tErr
			}
			sections = append(sections, fmt.Sprintf("%s: failed to start (%v)", srv.Name, err))
			continue
		}
		responding = append(responding, srv.Name)
		sections = append(sections, srv.Name+":")
		sections = append(sections, "  capabilities: "+client.formatRawCapabilities())
	}
	return makeResult(strings.Join(sections, "\n"), strings.Join(responding, ", "), true), nil
}

func executeRawRequest(
	ctx context.Context,
	sdkCtx sdk.Context,
	m *ClientManager,
	sessionID string,
	trusted bool,
	idleTimeoutMs int,
	cfg *LspConfig,
	cwd string,
	params LspParams,
	checkTimeoutErr func(error, string) error,
	makeResult func(string, string, bool) sdk.ToolResult,
) (sdk.ToolResult, error) {
	method := strings.TrimSpace(params.Query)
	if method == "" {
		return makeResult("Error: action=request requires `query` to specify the LSP method name (e.g., 'rust-analyzer/expandMacro')", "", false), nil
	}
	var chosen NamedServer
	var resolvedTarget string
	if params.File != "" && params.File != "*" {
		resolvedTarget = resolveToCwd(params.File, cwd)
		srv, ok := getLspServerForFile(cfg, resolvedTarget)
		if !ok {
			return makeResult("No language server found for this file", "", false), nil
		}
		chosen = srv
	} else {
		all := getLspServers(cfg)
		if len(all) == 0 {
			return makeResult("No language servers configured", "", false), nil
		}
		chosen = all[0]
	}

	var reqParams any
	if params.Payload != nil {
		if err := json.Unmarshal([]byte(*params.Payload), &reqParams); err != nil {
			return makeResult("Error: invalid JSON in payload: "+err.Error(), chosen.Name, false), nil
		}
	} else if resolvedTarget != "" {
		uri := fileToUri(resolvedTarget)
		if params.Line != nil {
			col, err := resolveSymbolColumn(resolvedTarget, *params.Line, params.Symbol)
			if err != nil {
				return makeResult("LSP error: "+err.Error(), chosen.Name, false), nil
			}
			reqParams = map[string]any{
				"textDocument": map[string]string{"uri": uri},
				"position":     Position{Line: *params.Line - 1, Character: col},
			}
		} else {
			reqParams = map[string]any{"textDocument": map[string]string{"uri": uri}}
		}
	} else {
		reqParams = map[string]any{}
	}

	client, err := m.GetOrCreateClientForSessionWithTrust(ctx, sessionID, trusted, idleTimeoutMs, chosen.Config, cwd, 0)
	if err != nil {
		if tErr := checkTimeoutErr(err, chosen.Name); tErr != nil {
			return sdk.ToolResult{}, tErr
		}
		return makeResult(fmt.Sprintf("LSP error from %s on %s: %v", chosen.Name, method, err), chosen.Name, false), nil
	}
	if resolvedTarget != "" {
		if _, err := client.ReconcileFileFromDisk(ctx, m, resolvedTarget); err != nil {
			return makeResult(fmt.Sprintf("LSP error from %s on %s: %v", chosen.Name, method, err), chosen.Name, false), nil
		}
	}
	popEditCtx := client.pushEditContext(sdkCtx)
	raw, err := client.SendRequest(ctx, method, reqParams, 0)
	popEditCtx()
	if err != nil {
		if tErr := checkTimeoutErr(err, chosen.Name); tErr != nil {
			return sdk.ToolResult{}, tErr
		}
		previewBytes, _ := json.Marshal(reqParams)
		preview := string(previewBytes)
		if len(preview) > 400 {
			preview = preview[:397] + "..."
		}
		return makeResult(fmt.Sprintf("LSP error from %s on %s: %v\n  params: %s", chosen.Name, method, err, preview), chosen.Name, false), nil
	}

	formatted := "null"
	trimmed := strings.TrimSpace(string(raw))
	if trimmed != "" && trimmed != "null" {
		var s string
		if err := json.Unmarshal(raw, &s); err == nil {
			formatted = s
		} else {
			var v any
			if err := json.Unmarshal(raw, &v); err == nil {
				if pretty, err := json.MarshalIndent(v, "", "  "); err == nil {
					formatted = string(pretty)
				} else {
					formatted = trimmed
				}
			} else {
				formatted = trimmed
			}
		}
	}
	return makeResult(fmt.Sprintf("%s ← %s:\n%s", chosen.Name, method, formatted), chosen.Name, true), nil
}

func executeWorkspaceSymbols(
	ctx context.Context,
	m *ClientManager,
	sessionID string,
	trusted bool,
	idleTimeoutMs int,
	cfg *LspConfig,
	cwd string,
	params LspParams,
	symbolPreset string,
	checkTimeoutErr func(error, string) error,
	makeResult func(string, string, bool) sdk.ToolResult,
) (sdk.ToolResult, error) {
	needle := strings.TrimSpace(params.Query)
	if needle == "" {
		return makeResult("Error: query parameter required for workspace symbol search", "", false), nil
	}
	servers := getLspServers(cfg)
	if len(servers) == 0 {
		return makeResult("No language server found for this action", "", false), nil
	}

	prov := &actionProvider{capability: "workspaceSymbolProvider", method: "workspace/symbol"}
	var aggregated []SymbolInformation
	var responding []string
	var failures []string
	var nonProviders []string

	for _, srv := range servers {
		if err := ctx.Err(); err != nil {
			if tErr := checkTimeoutErr(err, srv.Name); tErr != nil {
				return sdk.ToolResult{}, tErr
			}
			return sdk.ToolResult{}, err
		}
		client, err := m.GetOrCreateClientForSessionWithTrust(ctx, sessionID, trusted, idleTimeoutMs, srv.Config, cwd, 0)
		if err != nil {
			if tErr := checkTimeoutErr(err, srv.Name); tErr != nil {
				return sdk.ToolResult{}, tErr
			}
			failures = append(failures, fmt.Sprintf("  %s: %s", srv.Name, strings.ReplaceAll(err.Error(), "\t", " ")))
			continue
		}
		if !clientServes(client, prov) {
			nonProviders = append(nonProviders, srv.Name)
			continue
		}
		_, _ = client.ReconcileOpenFilesFromDisk(ctx, m, "", false)
		raw, err := client.SendRequest(ctx, "workspace/symbol", map[string]string{"query": needle}, 0)
		if err != nil {
			if tErr := checkTimeoutErr(err, srv.Name); tErr != nil {
				return sdk.ToolResult{}, tErr
			}
			failures = append(failures, fmt.Sprintf("  %s: %s", srv.Name, strings.ReplaceAll(err.Error(), "\t", " ")))
			continue
		}
		responding = append(responding, srv.Name)
		if len(raw) > 0 && string(raw) != "null" {
			var syms []SymbolInformation
			if err := json.Unmarshal(raw, &syms); err == nil && len(syms) > 0 {
				aggregated = append(aggregated, filterWorkspaceSymbols(syms, needle)...)
			}
		}
	}

	failSection := ""
	if len(failures) > 0 {
		failSection = "\nServer failures:\n" + strings.Join(failures, "\n")
	}
	if len(responding) == 0 {
		reason := "all language servers failed"
		if len(failures) == 0 {
			reason = fmt.Sprintf("no configured language server implements workspace/symbol (%s)", strings.Join(nonProviders, ", "))
		}
		return makeResult(fmt.Sprintf("Workspace symbol search failed: %s%s", reason, failSection), "", false), nil
	}

	deduped := dedupeWorkspaceSymbols(aggregated)
	if len(deduped) == 0 {
		return makeResult(fmt.Sprintf(`No symbols matching "%s"%s`, needle, failSection), strings.Join(responding, ", "), true), nil
	}

	limited := deduped
	truncLine := ""
	if len(deduped) > workspaceSymbolLimit {
		limited = deduped[:workspaceSymbolLimit]
		truncLine = fmt.Sprintf("\n[…%d symbols elided…]", len(deduped)-workspaceSymbolLimit)
	}
	lines := make([]string, len(limited))
	for i, s := range limited {
		lines[i] = "  " + formatSymbolInformation(s, cwd, symbolPreset)
	}
	out := fmt.Sprintf(`Found %d symbol(s) matching "%s":`+"\n%s%s%s",
		len(deduped), needle, strings.Join(lines, "\n"), truncLine, failSection)
	return makeResult(out, strings.Join(responding, ", "), true), nil
}

func executeWorkspaceReload(
	ctx context.Context,
	m *ClientManager,
	sessionID string,
	trusted bool,
	cwd string,
	params LspParams,
	checkTimeoutErr func(error, string) error,
	makeResult func(string, string, bool) sdk.ToolResult,
) (sdk.ToolResult, error) {
	InvalidateConfig(cwd)
	refreshed := GetConfigWithTrust(cwd, trusted)
	idleTimeoutMs := configIdleTimeoutMs(refreshed)
	servers := getLspServers(refreshed)
	configs := make([]ServerConfig, len(servers))
	for i, s := range servers {
		configs[i] = s.Config
	}
	stopped, err := m.ShutdownStaleClients(cwd, configs)
	if err != nil {
		return makeResult("LSP error: "+err.Error(), "", false), nil
	}
	if len(servers) == 0 {
		return makeResult("No language server found for this action", "", false), nil
	}

	var outputs []string
	if len(stopped) > 0 {
		outputs = append(outputs, fmt.Sprintf("Stopped %d server(s) with superseded configuration: %s", len(stopped), strings.Join(stopped, ", ")))
	}
	names := make([]string, len(servers))
	for i, srv := range servers {
		names[i] = srv.Name
		m.ClearInitFailure(srv.Config, cwd)
		client, err := m.GetOrCreateClientForSessionWithTrust(ctx, sessionID, trusted, idleTimeoutMs, srv.Config, cwd, 0)
		if err != nil {
			if tErr := checkTimeoutErr(err, srv.Name); tErr != nil {
				return sdk.ToolResult{}, tErr
			}
			outputs = append(outputs, fmt.Sprintf("Failed to reload %s: %v", srv.Name, err))
			continue
		}
		msg, relErr := reloadServer(ctx, m, client, srv.Name)
		if relErr != nil {
			if tErr := checkTimeoutErr(relErr, srv.Name); tErr != nil {
				return sdk.ToolResult{}, tErr
			}
			outputs = append(outputs, fmt.Sprintf("Failed to reload %s: %v", srv.Name, relErr))
			continue
		}
		outputs = append(outputs, msg)
	}
	return makeResult(strings.Join(outputs, "\n"), strings.Join(names, ", "), true), nil
}

func handleLocationAction(
	ctx context.Context,
	c *Client,
	method, noun, uri string,
	pos Position,
	symbolName, serverName, cwd string,
	checkOffTarget bool,
) (string, error) {
	raw, err := c.SendRequest(ctx, method, map[string]any{
		"textDocument": map[string]string{"uri": uri},
		"position":     pos,
	}, 0)
	if err != nil {
		return "", err
	}
	locs := normalizeLocationResult(raw)
	if len(locs) == 0 {
		return fmt.Sprintf("No %s found", noun), nil
	}
	lines := make([]string, len(locs))
	for i, loc := range locs {
		lines[i] = formatLocationWithContext(loc, cwd)
	}
	out := fmt.Sprintf("Found %d %s(s):\n%s", len(locs), noun, strings.Join(lines, "\n"))
	if checkOffTarget {
		out += offTargetNote(locs, symbolName, serverName, cwd)
	}
	return out, nil
}

func handleReferences(
	ctx context.Context,
	c *Client,
	srvCfg ServerConfig,
	uri string,
	pos Position,
	symbolName, serverName, cwd string,
) (string, error) {
	var locs []Location
	for attempt := range referencesRetryCount + 1 {
		raw, err := c.SendRequest(ctx, "textDocument/references", map[string]any{
			"textDocument": map[string]string{"uri": uri},
			"position":     pos,
			"context":      map[string]bool{"includeDeclaration": true},
		}, 0)
		if err != nil {
			return "", err
		}
		locs = nil
		if len(raw) > 0 && string(raw) != "null" {
			_ = json.Unmarshal(raw, &locs)
		}
		if !isProjectAwareLspServer(srvCfg) || attempt == referencesRetryCount {
			break
		}
		if len(locs) > 0 && !isOnlyQueriedDeclaration(locs, uri, pos) {
			break
		}
		_ = c.WaitForProjectLoaded(ctx)
		select {
		case <-time.After(referencesRetryDelay):
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}

	if len(locs) == 0 {
		return "No references found", nil
	}
	ctxLimit := min(referenceContextLimit, len(locs))
	lines := make([]string, 0, len(locs)+1)
	for _, loc := range locs[:ctxLimit] {
		lines = append(lines, formatLocationWithContext(loc, cwd))
	}
	if len(locs) > ctxLimit {
		rest := locs[ctxLimit:]
		lines = append(lines, fmt.Sprintf("  ... %d additional reference(s) shown without context", len(rest)))
		for _, loc := range rest {
			lines = append(lines, "  "+formatLocation(loc, cwd))
		}
	}
	out := fmt.Sprintf("Found %d reference(s):\n%s", len(locs), strings.Join(lines, "\n"))
	out += offTargetNote(locs, symbolName, serverName, cwd)
	return out, nil
}

func handleHover(ctx context.Context, c *Client, uri string, pos Position) (string, error) {
	raw, err := c.SendRequest(ctx, "textDocument/hover", map[string]any{
		"textDocument": map[string]string{"uri": uri},
		"position":     pos,
	}, 0)
	if err != nil {
		return "", err
	}
	if len(raw) == 0 || string(raw) == "null" {
		return "No hover information", nil
	}
	var hover Hover
	if err := json.Unmarshal(raw, &hover); err != nil || len(hover.Contents) == 0 || string(hover.Contents) == "null" {
		return "No hover information", nil
	}
	text := extractHoverText(hover.Contents)
	if text == "" {
		return "No hover information", nil
	}
	return text, nil
}

func handleCodeActions(
	ctx context.Context,
	sdkCtx sdk.Context,
	m *ClientManager,
	c *Client,
	srvCfg ServerConfig,
	uri string,
	pos Position,
	cwd string,
	params LspParams,
	reconciledFromDisk bool,
	timeoutSec int,
	waitBudget time.Duration,
) (string, error) {
	key := canonicalURIKey(uri)
	c.mu.Lock()
	diags := append([]Diagnostic(nil), c.diagnostics[key].Diagnostics...)
	c.mu.Unlock()

	if reconciledFromDisk {
		waitCap := singleDiagnosticsWaitTimeout
		if isProjectAwareLspServer(srvCfg) {
			waitCap = waitBudget
		}
		callCap := time.Duration(timeoutSec) * time.Second
		if callCap < waitCap {
			waitCap = callCap
		}
		var expVer *int
		if v, ok := c.OpenFileVersion(uri); ok {
			ev := v
			expVer = &ev
		}
		fresh, reported, err := waitForDiagnostics(ctx, c, uri, waitDiagnosticsOptions{
			timeout:                 waitCap,
			expectedDocumentVersion: expVer,
		})
		if err != nil {
			if ctx.Err() != nil {
				return "", err
			}
		} else if reported {
			diags = fresh
		} else {
			diags = nil
		}
	}
	if diags == nil {
		diags = []Diagnostic{}
	}

	requestActions := func(only []string) ([]CodeActionOrCommand, error) {
		ctxObj := map[string]any{
			"diagnostics": diags,
			"triggerKind": 1,
		}
		if len(only) > 0 {
			ctxObj["only"] = only
		}
		raw, err := c.SendRequest(ctx, "textDocument/codeAction", map[string]any{
			"textDocument": map[string]string{"uri": uri},
			"range":        Range{Start: pos, End: pos},
			"context":      ctxObj,
		}, 0)
		if err != nil {
			return nil, err
		}
		if len(raw) == 0 || string(raw) == "null" {
			return nil, nil
		}
		var items []CodeActionOrCommand
		if err := json.Unmarshal(raw, &items); err != nil {
			return nil, err
		}
		return items, nil
	}

	apply := params.Apply != nil && *params.Apply
	var actions []CodeActionOrCommand
	if !apply && params.Query != "" {
		var err error
		actions, err = requestActions([]string{params.Query})
		if err != nil {
			return "", err
		}
	} else {
		base, err := requestActions(nil)
		if err != nil {
			return "", err
		}
		extra, extraErr := requestActions([]string{"source"})
		if extraErr != nil && ctx.Err() != nil {
			return "", extraErr
		}
		actions = mergeCodeActions(base, extra)
	}

	if len(actions) == 0 {
		return "No code actions available", nil
	}

	if apply {
		normQuery := strings.TrimSpace(params.Query)
		var selected *CodeActionOrCommand
		if normQuery == "" {
			if len(actions) == 1 {
				selected = &actions[0]
			} else {
				var pref []*CodeActionOrCommand
				for i := range actions {
					if actions[i].IsPreferred {
						pref = append(pref, &actions[i])
					}
				}
				if len(pref) == 1 {
					selected = pref[0]
				}
			}
		} else if isAllDigits(normQuery) {
			// Match omp tool.ts:1720,1730-1734: an all-digit query is strictly an index
			// and must never fall through to a substring title match.
			if idx, err := strconv.Atoi(normQuery); err == nil && idx >= 0 && idx < len(actions) {
				selected = &actions[idx]
			}
		} else {
			lower := strings.ToLower(normQuery)
			for i := range actions {
				if strings.Contains(strings.ToLower(actions[i].Title), lower) {
					selected = &actions[i]
					break
				}
			}
		}

		if selected == nil {
			var lines []string
			if normQuery == "" {
				lines = append(lines, "apply=true needs query (an action's title or index) to choose among")
			} else {
				lines = append(lines, fmt.Sprintf(`No code action matches "%s". Available actions:`, normQuery))
			}
			for i, a := range actions {
				lines = append(lines, "  "+formatCodeAction(a, i))
			}
			return strings.Join(lines, "\n"), nil
		}

		if selected.IsCommand {
			args := selected.Arguments
			if args == nil {
				args = []any{}
			}
			popEditCtx := c.pushEditContext(sdkCtx)
			_, err := c.SendRequest(ctx, "workspace/executeCommand", map[string]any{
				"command":   selected.CommandName,
				"arguments": args,
			}, 0)
			popEditCtx()
			if err != nil {
				return "", err
			}
			return fmt.Sprintf(`Applied "%s":`+"\n  Executed command(s):\n    %s", selected.Title, selected.CommandName), nil
		}

		resolved := *selected
		var resolveErr error
		if resolved.Edit == nil {
			var rawItem any
			_ = json.Unmarshal(resolved.Raw, &rawItem)
			rawRes, err := c.SendRequest(ctx, "codeAction/resolve", rawItem, 0)
			if err != nil {
				resolveErr = err
			} else if len(rawRes) > 0 && string(rawRes) != "null" {
				var next CodeActionOrCommand
				if uErr := json.Unmarshal(rawRes, &next); uErr == nil {
					resolved = next
				}
			}
		}

		var appliedEdits []string
		if resolved.Edit != nil {
			edits, err := m.ApplyWorkspaceEditWithLsp(ctx, sdkCtx, resolved.Edit, cwd)
			if err != nil {
				return "", err
			}
			appliedEdits = edits
		}
		var executedCommands []string
		if resolved.SubCommand != nil {
			args := resolved.SubCommand.Arguments
			if args == nil {
				args = []any{}
			}
			popEditCtx := c.pushEditContext(sdkCtx)
			_, err := c.SendRequest(ctx, "workspace/executeCommand", map[string]any{
				"command":   resolved.SubCommand.Command,
				"arguments": args,
			}, 0)
			popEditCtx()
			if err != nil {
				return "", err
			}
			executedCommands = append(executedCommands, resolved.SubCommand.Command)
		}

		if len(appliedEdits) == 0 && len(executedCommands) == 0 {
			if resolveErr != nil {
				return "", fmt.Errorf(`codeAction/resolve failed for "%s": %w`, selected.Title, resolveErr)
			}
			return fmt.Sprintf(`Action "%s" has no workspace edit or command to apply`, selected.Title), nil
		}

		var summaryLines []string
		if len(appliedEdits) > 0 {
			summaryLines = append(summaryLines, "  Workspace edit:")
			for _, e := range appliedEdits {
				summaryLines = append(summaryLines, "    "+e)
			}
		}
		if len(executedCommands) > 0 {
			summaryLines = append(summaryLines, "  Executed command(s):")
			for _, cmdName := range executedCommands {
				summaryLines = append(summaryLines, "    "+cmdName)
			}
		}
		title := resolved.Title
		if title == "" {
			title = selected.Title
		}
		return fmt.Sprintf(`Applied "%s":`+"\n%s", title, strings.Join(summaryLines, "\n")), nil
	}

	lines := make([]string, len(actions))
	for i, a := range actions {
		lines[i] = "  " + formatCodeAction(a, i)
	}
	return fmt.Sprintf("%d code action(s):\n%s", len(actions), strings.Join(lines, "\n")), nil
}

func handleDocumentSymbols(ctx context.Context, c *Client, resolvedFile, uri, cwd, query, symbolPreset string) (string, error) {
	if resolvedFile == "" {
		return "Error: file parameter required for document symbols", nil
	}
	raw, err := c.SendRequest(ctx, "textDocument/documentSymbol", map[string]any{
		"textDocument": map[string]string{"uri": uri},
	}, 0)
	if err != nil {
		return "", err
	}
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" || trimmed == "[]" {
		return "No symbols found", nil
	}

	var rawItems []json.RawMessage
	if err := json.Unmarshal(raw, &rawItems); err != nil || len(rawItems) == 0 {
		return "No symbols found", nil
	}

	relPath := formatPathRelativeToCwd(resolvedFile, cwd)
	needle := strings.ToLower(strings.TrimSpace(query))

	var probe map[string]json.RawMessage
	_ = json.Unmarshal(rawItems[0], &probe)
	var lines []string
	if _, isDocSym := probe["selectionRange"]; isDocSym {
		var syms []DocumentSymbol
		if err := json.Unmarshal(raw, &syms); err != nil {
			return "", err
		}
		if needle != "" {
			lines = formatMatchingDocumentSymbols(syms, needle, symbolPreset)
		} else {
			for _, s := range syms {
				lines = append(lines, formatDocumentSymbol(s, 0, symbolPreset)...)
			}
		}
	} else {
		var syms []SymbolInformation
		if err := json.Unmarshal(raw, &syms); err != nil {
			return "", err
		}
		for _, s := range syms {
			if needle != "" &&
				!strings.Contains(strings.ToLower(s.Name), needle) &&
				!strings.Contains(strings.ToLower(s.ContainerName), needle) {
				continue
			}
			lines = append(lines, fmt.Sprintf("%s %s @ line %d",
				symbolKindToIcon(s.Kind, symbolPreset), s.Name, s.Location.Range.Start.Line+1))
		}
	}

	if len(lines) == 0 {
		return fmt.Sprintf(`No symbols matching "%s" in %s`, strings.TrimSpace(query), relPath), nil
	}
	matching := ""
	if needle != "" {
		matching = fmt.Sprintf(` matching "%s"`, strings.TrimSpace(query))
	}
	return fmt.Sprintf("Symbols%s in %s:\n%s", matching, relPath, strings.Join(lines, "\n")), nil
}

func handleRename(ctx context.Context, sdkCtx sdk.Context, m *ClientManager, c *Client, uri string, pos Position, cwd string, params LspParams) (string, error) {
	raw, err := c.SendRequest(ctx, "textDocument/rename", map[string]any{
		"textDocument": map[string]string{"uri": uri},
		"position":     pos,
		"newName":      params.NewName,
	}, 0)
	if err != nil {
		return "", err
	}
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return "Rename returned no edits", nil
	}
	var edit WorkspaceEdit
	if err := json.Unmarshal(raw, &edit); err != nil {
		return "", err
	}
	shouldApply := params.Apply == nil || *params.Apply
	if shouldApply {
		applied, err := m.ApplyWorkspaceEditWithLsp(ctx, sdkCtx, &edit, cwd)
		if err != nil {
			return "", err
		}
		lines := make([]string, len(applied))
		for i, a := range applied {
			lines[i] = "  " + a
		}
		return "Applied rename:\n" + strings.Join(lines, "\n"), nil
	}
	preview := formatWorkspaceEdit(&edit, cwd)
	lines := make([]string, len(preview))
	for i, p := range preview {
		lines[i] = "  " + p
	}
	return "Rename preview:\n" + strings.Join(lines, "\n"), nil
}
