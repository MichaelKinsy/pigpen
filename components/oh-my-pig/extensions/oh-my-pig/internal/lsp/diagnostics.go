package lsp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// upstream: packages/coding-agent/src/lsp/servers.ts, packages/coding-agent/src/lsp/diagnostics.ts,
//           packages/coding-agent/src/lsp/completeness.ts

const (
	diagnosticMessageLimit        = 50
	singleDiagnosticsWaitTimeout  = 3 * time.Second
	batchDiagnosticsWaitTimeout   = 400 * time.Millisecond
	diagnosticsPollInterval       = 100 * time.Millisecond
	buildStatusPollInterval       = 20 * time.Millisecond
	diagnosticsSettleWindow       = 250 * time.Millisecond
	inlineDiagnosticsWaitTimeout  = 500 * time.Millisecond
	deferredDiagnosticsWaitWindow = 12 * time.Second
	diagnosticsPipelineGrace      = 10 * time.Second
	maxGlobDiagnosticTargets      = 20
	workspaceSymbolLimit          = 200
	referenceContextLimit         = 50
	locationContextLines          = 1
	referencesRetryCount          = 2
	referencesRetryDelay          = 250 * time.Millisecond

	lspContentModified = -32801
	lspServerCancelled = -32802
)

var lspReadOnlyActions = map[string]bool{
	"diagnostics":     true,
	"definition":      true,
	"type_definition": true,
	"implementation":  true,
	"references":      true,
	"hover":           true,
	"symbols":         true,
	"status":          true,
	"capabilities":    true,
}

var projectIndexedActions = map[string]bool{
	"definition":      true,
	"type_definition": true,
	"implementation":  true,
	"references":      true,
	"rename":          true,
	"hover":           true,
}

var crossFileActions = map[string]bool{
	"definition":      true,
	"type_definition": true,
	"implementation":  true,
	"references":      true,
	"rename":          true,
	"hover":           true,
	"code_actions":    true,
}

func isCustomLinter(cfg ServerConfig) bool {
	return cfg.CreateClient != nil
}

func isProjectAwareLspServer(cfg ServerConfig) bool {
	return !isCustomLinter(cfg) && !cfg.IsLinter
}

func splitServers(servers []NamedServer) (lspServers, customLinters []NamedServer) {
	for _, s := range servers {
		if isCustomLinter(s.Config) {
			customLinters = append(customLinters, s)
		} else {
			lspServers = append(lspServers, s)
		}
	}
	return lspServers, customLinters
}

func getLspServers(cfg *LspConfig) []NamedServer {
	all := snapshotAllServers(cfg)
	var out []NamedServer
	for _, s := range all {
		if !isCustomLinter(s.Config) {
			out = append(out, s)
		}
	}
	return out
}

func getLspServersForFile(cfg *LspConfig, filePath string) []NamedServer {
	all := GetServersForFile(cfg, filePath)
	var out []NamedServer
	for _, s := range all {
		if !isCustomLinter(s.Config) {
			out = append(out, s)
		}
	}
	return out
}

func getLspServerForFile(cfg *LspConfig, filePath string) (NamedServer, bool) {
	servers := getLspServersForFile(cfg, filePath)
	if len(servers) == 0 {
		return NamedServer{}, false
	}
	return servers[0], true
}

// isMethodNotFoundError matches LSP -32601 and common server phrasings.
// upstream: packages/coding-agent/src/lsp/servers.ts:isMethodNotFoundError
func isMethodNotFoundError(err error) bool {
	if err == nil {
		return false
	}
	var rpcErr *LspResponseError
	if errors.As(err, &rpcErr) && rpcErr.HasCode && rpcErr.Code == -32601 {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "method not found") ||
		strings.Contains(msg, "unhandled method") ||
		strings.Contains(msg, "not supported") ||
		strings.Contains(msg, "-32601")
}

// reloadServer reloads a live server in place, restarting rust-analyzer if a
// new Cargo.toml appeared since initialize, or restarting on write failure.
// upstream: packages/coding-agent/src/lsp/servers.ts:reloadServer
func reloadServer(ctx context.Context, m *ClientManager, c *Client, serverName string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if isRustAnalyzerConfig(c.config) || serverName == "rust-analyzer" {
		c.mu.Lock()
		initial := c.cargoManifests
		hasInitial := c.hasCargoManifests
		c.mu.Unlock()
		current := listCargoManifests(c.cwd)
		if !hasInitial || initial != current {
			return restartServer(m, c, serverName)
		}
		_, err := c.SendRequest(ctx, "rust-analyzer/reloadWorkspace", nil, 0)
		if err == nil {
			return "Reloaded " + serverName, nil
		}
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		if !isMethodNotFoundError(err) {
			return "", err
		}
	}
	settings := c.config.Settings
	if settings == nil {
		settings = map[string]any{}
	}
	if err := c.SendNotification(ctx, "workspace/didChangeConfiguration", map[string]any{"settings": settings}); err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return restartServer(m, c, serverName)
	}
	if err := c.RefreshOpenFiles(ctx, m); err != nil {
		return "", err
	}
	return "Reloaded " + serverName, nil
}

func restartServer(m *ClientManager, c *Client, serverName string) (string, error) {
	if !m.ShutdownClient(c) {
		return "", fmt.Errorf("Failed to restart %s: server process did not exit after kill", serverName)
	}
	return "Restarted " + serverName, nil
}

// =============================================================================
// Diagnostics Waiting (Push + Pull + Dual-Mode Merge + Clangd Rebuild)
// upstream: packages/coding-agent/src/lsp/diagnostics.ts
// =============================================================================

func unreportedSummary(servers []string, reported string) string {
	unverified := "no diagnostics report yet from " + strings.Join(servers, ", ")
	if reported != "" {
		return reported + "; " + unverified
	}
	return unverified
}

func limitDiagnosticMessages(messages []string) []string {
	if len(messages) <= diagnosticMessageLimit {
		return messages
	}
	return messages[:diagnosticMessageLimit]
}

var orphanTSProjectCodes = map[int]bool{
	1375: true, 1378: true, 2307: true, 2580: true, 2591: true, 2792: true, 2867: true,
}

func filterOrphanProjectDiagnostics(absPath, serverName string, cfg ServerConfig, diags []Diagnostic) []Diagnostic {
	if len(cfg.RootMarkers) == 0 || hasRootMarkerAncestor(absPath, cfg.RootMarkers) {
		return diags
	}
	isTSServer := strings.Contains(strings.ToLower(serverName), "typescript")
	out := make([]Diagnostic, 0, len(diags))
	for _, d := range diags {
		if (d.Source == "typescript" || isTSServer) && isOrphanTSCode(d.Code) {
			continue
		}
		out = append(out, d)
	}
	return out
}

func isOrphanTSCode(code any) bool {
	switch v := code.(type) {
	case int:
		return orphanTSProjectCodes[v]
	case float64:
		return orphanTSProjectCodes[int(v)]
	case string:
		var n int
		if _, err := fmt.Sscanf(v, "%d", &n); err == nil && fmt.Sprintf("%d", n) == v {
			return orphanTSProjectCodes[n]
		}
	}
	return false
}

func hasRustWorkspaceAncestor(filePath string) bool {
	return hasRootMarkerAncestor(filePath, []string{"Cargo.toml", "rust-analyzer.toml"})
}

func isOnlyQueriedDeclaration(locs []Location, uri string, pos Position) bool {
	if len(locs) != 1 {
		return false
	}
	if canonicalURIKey(locs[0].URI) != canonicalURIKey(uri) {
		return false
	}
	r := locs[0].Range
	return comparePosition(r.Start, pos) <= 0 && comparePosition(pos, r.End) <= 0
}

// normalizeLocationResult decodes Location | Location[] | LocationLink | LocationLink[] | null.
func normalizeLocationResult(raw json.RawMessage) []Location {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return nil
	}
	var items []json.RawMessage
	if strings.HasPrefix(trimmed, "[") {
		if err := json.Unmarshal(raw, &items); err != nil {
			return nil
		}
	} else {
		items = []json.RawMessage{raw}
	}
	out := make([]Location, 0, len(items))
	for _, item := range items {
		var loc Location
		if err := json.Unmarshal(item, &loc); err == nil && loc.URI != "" {
			out = append(out, loc)
			continue
		}
		var link LocationLink
		if err := json.Unmarshal(item, &link); err == nil && link.TargetURI != "" {
			r := link.TargetSelectionRange
			if isEmptyRange(r) && !isEmptyRange(link.TargetRange) {
				r = link.TargetRange
			}
			out = append(out, Location{URI: link.TargetURI, Range: r})
		}
	}
	return out
}

func formatLocationWithContext(loc Location, cwd string) string {
	header := "  " + formatLocation(loc, cwd)
	lines, err := readLocationContext(uriToFile(loc.URI), loc.Range.Start.Line+1, locationContextLines)
	if err != nil || len(lines) == 0 {
		return header
	}
	var b strings.Builder
	b.WriteString(header)
	for _, line := range lines {
		b.WriteString("\n    ")
		b.WriteString(line)
	}
	return b.String()
}

type waitDiagnosticsOptions struct {
	timeout                 time.Duration
	minVersion              *int
	expectedDocumentVersion *int
	settle                  time.Duration
	afterProjectLoad        bool
}

type pullDiagnosticsOutcome struct {
	diagnostics []Diagnostic
	hasDiags    bool
	failed      error
	declined    bool
}

func requestDocumentDiagnostics(ctx context.Context, c *Client, uri string, timeout time.Duration) pullDiagnosticsOutcome {
	raw, err := c.SendRequest(ctx, "textDocument/diagnostic", map[string]any{
		"textDocument": map[string]string{"uri": uri},
	}, timeout)
	if err != nil {
		return pullDiagnosticsOutcome{failed: err}
	}
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return pullDiagnosticsOutcome{declined: true}
	}
	var report struct {
		Kind  string       `json:"kind"`
		Items []Diagnostic `json:"items"`
	}
	if err := json.Unmarshal(raw, &report); err != nil {
		return pullDiagnosticsOutcome{}
	}
	if report.Kind != "full" {
		return pullDiagnosticsOutcome{}
	}
	if report.Items == nil {
		report.Items = []Diagnostic{}
	}
	return pullDiagnosticsOutcome{diagnostics: report.Items, hasDiags: true}
}

func shouldRetriggerPull(err error) bool {
	var rpcErr *LspResponseError
	if !errors.As(err, &rpcErr) || !rpcErr.HasCode {
		return false
	}
	if rpcErr.Code == lspContentModified {
		return true
	}
	if rpcErr.Code != lspServerCancelled {
		return false
	}
	if dataMap, ok := rpcErr.Data.(map[string]any); ok {
		if v, exists := dataMap["retriggerRequest"].(bool); exists && !v {
			return false
		}
	}
	return true
}

func isPullFailure(err error) bool {
	var timeoutErr *LspRequestTimeoutError
	if errors.As(err, &timeoutErr) {
		return false
	}
	return !shouldRetriggerPull(err)
}

func isProvisionalColdPublish(c *Client, key string, pub PublishedDiagnostics, expectedDocVersion *int, now time.Time) bool {
	if expectedDocVersion == nil {
		return false
	}
	c.mu.Lock()
	openInfo := c.openFiles[key]
	startedAt := c.startedAt
	c.mu.Unlock()

	if openInfo != nil && openInfo.reopenedAt != nil && now.Sub(*openInfo.reopenedAt) < deferredDiagnosticsWaitWindow {
		if len(pub.Diagnostics) == 0 {
			return true
		}
		if pub.Version != nil && *pub.Version < *expectedDocVersion {
			return true
		}
	}
	if len(pub.Diagnostics) > 0 {
		return false
	}
	return pub.Version == nil && !startedAt.IsZero() && now.Sub(startedAt) < deferredDiagnosticsWaitWindow
}

// waitForDiagnostics waits for a fresh diagnostics report (pull or push).
// Returns (diags, true, nil) when a fresh report arrived (empty slice means clean),
// (nil, false, nil) when no report arrived in time (unverified, never "OK"),
// or (nil, false, err) on cancellation or pull RPC error.
// upstream: packages/coding-agent/src/lsp/diagnostics.ts:waitForDiagnostics
func waitForDiagnostics(ctx context.Context, c *Client, uri string, opts waitDiagnosticsOptions) ([]Diagnostic, bool, error) {
	timeout := opts.timeout
	if timeout <= 0 {
		timeout = singleDiagnosticsWaitTimeout
	}
	settle := opts.settle
	if settle == 0 {
		settle = diagnosticsSettleWindow
	}
	if settle < 0 {
		settle = 0
	}

	key := canonicalURIKey(uri)
	deadline := time.Now().Add(timeout)
	awaitingProjectLoad := opts.afterProjectLoad

	nextPullAt := time.Time{} // zero means ready to send immediately
	pullScheduled := true
	var pullCh chan pullDiagnosticsOutcome
	var pulled []Diagnostic
	hasPulled := false
	var pullFailure error

	hasSettled := false
	var settledDiagVersion int
	var settledAt time.Time

	for time.Now().Before(deadline) {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}

		supportsPull := c.SupportsDocumentDiagnostics()
		if awaitingProjectLoad && supportsPull {
			awaitingProjectLoad = false
			if err := c.WaitForProjectLoaded(ctx); err != nil {
				return nil, false, err
			}
			if nextDeadline := time.Now().Add(timeout); nextDeadline.After(deadline) {
				deadline = nextDeadline
			}
		}

		if pullScheduled && pullCh == nil && supportsPull && !time.Now().Before(nextPullAt) {
			pullScheduled = false
			rem := time.Until(deadline)
			if rem < time.Millisecond {
				rem = time.Millisecond
			}
			ch := make(chan pullDiagnosticsOutcome, 1)
			pullCh = ch
			go func(budget time.Duration) {
				ch <- requestDocumentDiagnostics(ctx, c, uri, budget)
			}(rem)
		}

		c.mu.Lock()
		diagVer := c.diagnosticsVersion
		pub, hasPub := c.diagnostics[key]
		notifyCh := c.diagNotify
		c.mu.Unlock()

		versionOk := opts.minVersion == nil || diagVer > *opts.minVersion
		if hasPub && versionOk && !supportsPull {
			if opts.expectedDocumentVersion != nil && pub.Version != nil && *pub.Version == *opts.expectedDocumentVersion {
				return pub.Diagnostics, true, nil
			}
			now := time.Now()
			if !hasSettled || diagVer != settledDiagVersion {
				hasSettled = true
				settledDiagVersion = diagVer
				settledAt = now
			}
			if now.Sub(settledAt) >= settle && !isProvisionalColdPublish(c, key, pub, opts.expectedDocumentVersion, now) {
				return pub.Diagnostics, true, nil
			}
		}

		waitDur := diagnosticsPollInterval
		if rem := time.Until(deadline); rem < waitDur {
			waitDur = rem
		}
		if waitDur <= 0 {
			break
		}

		timer := time.NewTimer(waitDur)
		if pullCh != nil {
			select {
			case outcome := <-pullCh:
				timer.Stop()
				pullCh = nil
				if outcome.hasDiags {
					pulled = outcome.diagnostics
					hasPulled = true
					goto finishPull
				}
				if outcome.declined {
					if capDeadline := time.Now().Add(settle); capDeadline.Before(deadline) {
						deadline = capDeadline
					}
				}
				if outcome.failed != nil {
					if shouldRetriggerPull(outcome.failed) {
						pullScheduled = true
						nextPullAt = time.Now().Add(diagnosticsPollInterval)
					} else if isPullFailure(outcome.failed) {
						pullFailure = outcome.failed
					}
				}
			case <-notifyCh:
				timer.Stop()
			case <-timer.C:
			case <-ctx.Done():
				timer.Stop()
				return nil, false, ctx.Err()
			}
		} else {
			select {
			case <-notifyCh:
				timer.Stop()
			case <-timer.C:
			case <-ctx.Done():
				timer.Stop()
				return nil, false, ctx.Err()
			}
		}
	}

finishPull:
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}

	c.mu.Lock()
	diagVer := c.diagnosticsVersion
	pub, hasPub := c.diagnostics[key]
	supportsPull := c.supportsDocumentDiagnosticsLocked()
	c.mu.Unlock()

	versionOk := opts.minVersion == nil || diagVer > *opts.minVersion
	if !hasPulled && hasPub && versionOk && !supportsPull {
		if opts.expectedDocumentVersion != nil && pub.Version != nil && *pub.Version == *opts.expectedDocumentVersion {
			return pub.Diagnostics, true, nil
		}
		now := time.Now()
		if hasSettled && diagVer == settledDiagVersion && now.Sub(settledAt) >= settle &&
			!isProvisionalColdPublish(c, key, pub, opts.expectedDocumentVersion, now) {
			return pub.Diagnostics, true, nil
		}
	}

	if pullCh != nil {
		select {
		case outcome := <-pullCh:
			if outcome.hasDiags {
				pulled = outcome.diagnostics
				hasPulled = true
			} else if outcome.failed != nil && isPullFailure(outcome.failed) {
				pullFailure = outcome.failed
			}
		case <-ctx.Done():
			return nil, false, ctx.Err()
		}
	}

	if !hasPulled {
		if versionOk && supportsPull {
			c.mu.Lock()
			pubRec, hasPubRec := c.publishedDiagnostics[key]
			pulledRec, hasPulledRec := c.pulledDiagnostics[key]
			openInfo := c.openFiles[key]
			c.mu.Unlock()
			if hasPubRec {
				isFresh := false
				if pubRec.Version == nil {
					isFresh = !isProvisionalColdPublish(c, key, pubRec, opts.expectedDocumentVersion, time.Now())
				} else {
					isFresh = openInfo == nil || *pubRec.Version >= openInfo.version
				}
				if isFresh {
					merged := pubRec.Diagnostics
					if hasPulledRec {
						merged = mergeDiagnosticLists(pulledRec.Diagnostics, pubRec.Diagnostics)
					}
					ver := opts.expectedDocumentVersion
					if ver == nil && openInfo != nil {
						v := openInfo.version
						ver = &v
					}
					c.mu.Lock()
					c.diagnostics[key] = PublishedDiagnostics{Diagnostics: merged, Version: ver}
					c.diagnosticsVersion++
					c.mu.Unlock()
					return merged, true, nil
				}
			}
		}
		if pullFailure != nil {
			return nil, false, pullFailure
		}
		return nil, false, nil
	}

	c.mu.Lock()
	openInfo := c.openFiles[key]
	ver := opts.expectedDocumentVersion
	if ver == nil && openInfo != nil {
		v := openInfo.version
		ver = &v
	}
	c.pulledDiagnostics[key] = PublishedDiagnostics{Diagnostics: pulled, Version: ver}
	pubRec, hasPubRec := c.publishedDiagnostics[key]
	canMergePush := hasPubRec && (pubRec.Version == nil || openInfo == nil || *pubRec.Version >= openInfo.version)
	combined := pulled
	if canMergePush {
		combined = mergeDiagnosticLists(pulled, pubRec.Diagnostics)
	}
	c.diagnostics[key] = PublishedDiagnostics{Diagnostics: combined, Version: ver}
	c.diagnosticsVersion++
	c.mu.Unlock()

	return combined, true, nil
}

func waitForRebuiltDiagnostics(ctx context.Context, c *Client, uri string, version, sinceSeq int, timeout time.Duration) ([]Diagnostic, bool, error) {
	key := canonicalURIKey(uri)
	deadline := time.Now().Add(timeout)
	for {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		c.mu.Lock()
		var st *documentBuildStatus
		if c.buildStatus != nil {
			st = c.buildStatus[key]
		}
		settled := st != nil && st.busySeq > sinceSeq && st.idleSeq > st.busySeq
		c.mu.Unlock()
		if settled {
			break
		}
		rem := time.Until(deadline)
		if rem <= 0 {
			return nil, false, nil
		}
		sleep := buildStatusPollInterval
		if rem < sleep {
			sleep = rem
		}
		select {
		case <-time.After(sleep):
		case <-ctx.Done():
			return nil, false, ctx.Err()
		}
	}

	rem := time.Until(deadline)
	if rem < time.Millisecond {
		rem = time.Millisecond
	}
	_, err := c.SendRequest(ctx, "textDocument/documentSymbol", map[string]any{
		"textDocument": map[string]string{"uri": uri},
	}, rem)
	if err != nil {
		if ctx.Err() != nil {
			return nil, false, ctx.Err()
		}
		return nil, false, nil
	}
	c.mu.Lock()
	pub, ok := c.diagnostics[key]
	c.mu.Unlock()
	if ok && pub.Version != nil && *pub.Version == version {
		return pub.Diagnostics, true, nil
	}
	return nil, false, nil
}

// =============================================================================
// Formatting (textDocument/formatting + custom linters)
// upstream: packages/coding-agent/src/lsp/diagnostics.ts:formatContent
// =============================================================================

type FormatContentResult struct {
	Content     string
	Failed      bool
	Unsupported bool
}

func formatContent(ctx context.Context, m *ClientManager, sessionID string, trusted bool, idleTimeoutMs int, absPath, content, cwd string, servers []NamedServer) (FormatContentResult, error) {
	if len(servers) == 0 {
		return FormatContentResult{Content: content, Unsupported: true}, nil
	}
	ownerID, releaseOwner := acquireSessionForCall(m, sessionID)
	defer releaseOwner()
	uri := fileToUri(absPath)
	ordered := append([]NamedServer(nil), servers...)
	sort.SliceStable(ordered, func(i, j int) bool {
		return ordered[i].Config.IsLinter && !ordered[j].Config.IsLinter
	})

	hadFailure := false
	for _, s := range ordered {
		if err := ctx.Err(); err != nil {
			return FormatContentResult{Content: content}, err
		}
		if s.Config.CreateClient != nil {
			linter := getLinterClient(s.Name, s.Config, cwd)
			formatted, err := linter.Format(ctx, absPath, content)
			if err != nil {
				if ctx.Err() != nil {
					return FormatContentResult{Content: content}, err
				}
				hadFailure = true
				continue
			}
			return FormatContentResult{Content: formatted}, nil
		}

		client, err := m.GetOrCreateClientForSessionWithTrust(ctx, ownerID, trusted, idleTimeoutMs, s.Config, cwd, 0)
		if err != nil {
			if ctx.Err() != nil {
				return FormatContentResult{Content: content}, err
			}
			hadFailure = true
			continue
		}
		if !client.SupportsMethod("documentFormattingProvider", "textDocument/formatting") {
			continue
		}
		raw, err := client.SendRequest(ctx, "textDocument/formatting", map[string]any{
			"textDocument": map[string]string{"uri": uri},
			"options":      resolveFormatOptions(absPath, content),
		}, 0)
		if err != nil {
			if ctx.Err() != nil {
				return FormatContentResult{Content: content}, err
			}
			hadFailure = true
			continue
		}
		var edits []TextEdit
		if len(raw) == 0 || string(raw) == "null" {
			return FormatContentResult{Content: content}, nil
		}
		if err := json.Unmarshal(raw, &edits); err != nil {
			hadFailure = true
			continue
		}
		if len(edits) == 0 {
			return FormatContentResult{Content: content}, nil
		}
		updated, err := applyTextEditsToString(content, edits)
		if err != nil {
			hadFailure = true
			continue
		}
		return FormatContentResult{Content: updated}, nil
	}
	return FormatContentResult{Content: content, Failed: hadFailure, Unsupported: !hadFailure}, nil
}

// =============================================================================
// Multi-server Diagnostics Collection for Post-Write Hooks
// upstream: packages/coding-agent/src/lsp/diagnostics.ts:getDiagnosticsForFile
// =============================================================================

type perServerDiagResult struct {
	serverName   string
	serverConfig ServerConfig
	diags        []Diagnostic
	reported     bool
	err          error
}

func getDiagnosticsForFile(
	ctx context.Context,
	m *ClientManager,
	absPath, cwd string,
	servers []NamedServer,
	minVersions map[string]int,
	expectedDocVersions map[string]int,
	waitBudget time.Duration,
) *FileDiagnosticsResult {
	if len(servers) == 0 {
		return nil
	}
	if waitBudget <= 0 {
		waitBudget = singleDiagnosticsWaitTimeout
	}
	pipelineBudget := waitBudget + diagnosticsPipelineGrace
	uri := fileToUri(absPath)
	relPath := formatPathRelativeToCwd(absPath, cwd)

	results := make([]perServerDiagResult, len(servers))
	var wg sync.WaitGroup
	for i, s := range servers {
		wg.Add(1)
		go func(idx int, srv NamedServer) {
			defer wg.Done()
			srvCtx, cancel := context.WithTimeout(ctx, pipelineBudget)
			defer cancel()

			if srv.Config.CreateClient != nil {
				linter := getLinterClient(srv.Name, srv.Config, cwd)
				diags, err := linter.Lint(srvCtx, absPath)
				results[idx] = perServerDiagResult{
					serverName:   srv.Name,
					serverConfig: srv.Config,
					diags:        diags,
					reported:     err == nil,
					err:          err,
				}
				return
			}
			client, err := m.GetActiveOrPendingClient(srvCtx, srv.Config, cwd)
			if err != nil || client == nil {
				results[idx] = perServerDiagResult{serverName: srv.Name, serverConfig: srv.Config, err: err}
				return
			}
			var minVer *int
			if v, ok := minVersions[srv.Name]; ok {
				mv := v
				minVer = &mv
			}
			var expVer *int
			if v, ok := expectedDocVersions[srv.Name]; ok {
				ev := v
				expVer = &ev
			}
			diags, reported, err := waitForDiagnostics(srvCtx, client, uri, waitDiagnosticsOptions{
				timeout:                 waitBudget,
				minVersion:              minVer,
				expectedDocumentVersion: expVer,
				afterProjectLoad:        isProjectAwareLspServer(srv.Config),
			})
			results[idx] = perServerDiagResult{
				serverName:   srv.Name,
				serverConfig: srv.Config,
				diags:        diags,
				reported:     reported && err == nil,
				err:          err,
			}
		}(i, s)
	}
	wg.Wait()

	var allDiags []Diagnostic
	var serverNames []string
	var unreported []string

	for _, r := range results {
		if r.err != nil {
			continue
		}
		if !r.reported {
			unreported = append(unreported, r.serverName)
			continue
		}
		serverNames = append(serverNames, r.serverName)
		filtered := filterOrphanProjectDiagnostics(absPath, r.serverName, r.serverConfig, r.diags)
		allDiags = append(allDiags, filtered...)
	}

	if len(serverNames) == 0 && len(unreported) == 0 {
		return nil
	}
	combinedServers := append(append([]string(nil), serverNames...), unreported...)
	serverLabel := strings.Join(combinedServers, ", ")

	if len(allDiags) == 0 {
		summary := "OK"
		if len(unreported) > 0 {
			summary = unreportedSummary(unreported, "")
		}
		return &FileDiagnosticsResult{
			Server:     serverLabel,
			Messages:   []string{},
			Summary:    summary,
			Errored:    false,
			Unreported: unreported,
		}
	}

	unique := deduplicateDiagnostics(allDiags)
	sortDiagnostics(unique)
	formatted := make([]string, len(unique))
	hasErrors := false
	for i, d := range unique {
		formatted[i] = formatDiagnostic(d, relPath)
		if d.Severity == 0 || d.Severity == SeverityError {
			hasErrors = true
		}
	}
	limited := limitDiagnosticMessages(formatted)
	summary := formatDiagnosticsSummary(unique)
	if len(unreported) > 0 {
		summary = unreportedSummary(unreported, summary)
	}
	return &FileDiagnosticsResult{
		Server:     serverLabel,
		Messages:   limited,
		Summary:    summary,
		Errored:    hasErrors,
		Unreported: unreported,
	}
}

// =============================================================================
// Location Sanity Checks (completeness.ts)
// =============================================================================

func locationsNotMentioning(locs []Location, symbolName string) []Location {
	if symbolName == "" {
		return nil
	}
	var misses []Location
	for _, loc := range locs {
		lines, err := readLocationContext(uriToFile(loc.URI), loc.Range.Start.Line+1, 0)
		if err != nil || len(lines) == 0 {
			continue
		}
		// Strip "N: " prefix from readLocationContext line.
		text := lines[0]
		if idx := strings.Index(text, ": "); idx != -1 {
			text = text[idx+2:]
		}
		if len(findSymbolMatchIndexes(text, symbolName, false)) == 0 {
			misses = append(misses, loc)
		}
	}
	return misses
}

func offTargetNote(locs []Location, symbolName, serverName, cwd string) string {
	if symbolName == "" {
		return ""
	}
	misses := locationsNotMentioning(locs, symbolName)
	if len(misses) == 0 {
		return ""
	}
	limit := min(3, len(misses))
	examples := make([]string, limit)
	for i := range limit {
		examples[i] = formatLocation(misses[i], cwd)
	}
	clangdHint := ""
	if serverName == "clangd" {
		_, err1 := os.Stat(filepath.Join(cwd, "compile_commands.json"))
		_, err2 := os.Stat(filepath.Join(cwd, "build", "compile_commands.json"))
		if os.IsNotExist(err1) && os.IsNotExist(err2) {
			clangdHint = "; clangd has no compile_commands.json and is parsing with fallback flags"
		}
	}
	return fmt.Sprintf("\n⚠ %d location(s) do not mention %s (%s)%s",
		len(misses), symbolName, strings.Join(examples, ", "), clangdHint)
}
