package lsp

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// upstream: packages/coding-agent/src/lsp/writethrough.ts,
//           packages/coding-agent/src/lsp/diagnostics-ledger.ts,
//           packages/tui/src/tools/output-meta.ts:formatOutputNotice

const writethroughSyncTimeout = 5 * time.Second

var (
	ledgersMu sync.Mutex
	ledgers   = make(map[string]*DiagnosticsLedger)
)

// ledgerForSession returns the per-session DiagnosticsLedger for sessionID,
// or nil when sessionID is empty so unresolved sessions never share a dedupe
// ledger or suppress each other's diagnostics.
func ledgerForSession(sessionID string) *DiagnosticsLedger {
	if sessionID == "" {
		return nil
	}
	ledgersMu.Lock()
	defer ledgersMu.Unlock()
	l, ok := ledgers[sessionID]
	if !ok {
		l = NewDiagnosticsLedger()
		ledgers[sessionID] = l
	}
	return l
}

// deleteLedgerForSession releases only sessionID's dedupe ledger on session end.
func deleteLedgerForSession(sessionID string) {
	if sessionID == "" {
		return
	}
	ledgersMu.Lock()
	delete(ledgers, sessionID)
	ledgersMu.Unlock()
}

func resetLedgers() {
	ledgersMu.Lock()
	ledgers = make(map[string]*DiagnosticsLedger)
	ledgersMu.Unlock()
}

// DiagnosticsAfterWrite runs post-write LSP synchronization and diagnostics
// collection for path, returning the formatted suffix omp appends to a `write`
// result (`\n\nLSP Diagnostics (<summary>):\n<grouped messages>`), or "" when
// LSP is disabled, `lsp.diagnosticsOnWrite` is false, no server applies, the
// 5s sync budget is exhausted, or all diagnostics were deduplicated/clean.
//
// The integrator calls this from `internal/files` after a `write` commits.
// upstream: packages/coding-agent/src/tools/write.ts, packages/coding-agent/src/lsp/writethrough.ts
func DiagnosticsAfterWrite(ctx sdk.Context, path string) string {
	return diagnosticsAfterFileChange(ctx, path, false)
}

// DiagnosticsAfterEdit runs post-edit LSP synchronization and diagnostics
// collection for path, honoring `lsp.diagnosticsOnEdit` (off by default in omp)
// while still notifying active servers of the changed file.
//
// The integrator may call this from `internal/files` after an `edit` commits.
func DiagnosticsAfterEdit(ctx sdk.Context, path string) string {
	return diagnosticsAfterFileChange(ctx, path, true)
}

func diagnosticsAfterFileChange(sdkCtx sdk.Context, rawPath string, isEdit bool) string {
	settings := readSettings(sdkCtx)
	if !settings.Enabled {
		return ""
	}
	cwd := safeCwd(sdkCtx)
	if cwd == "" {
		cwd = filepath.Dir(rawPath)
	}
	absPath := resolveToCwd(rawPath, cwd)
	sessionID := resolveSessionID(sdkCtx)
	trusted := isProjectTrusted(sdkCtx)

	enableDiagnostics := settings.DiagnosticsOnWrite
	if isEdit {
		enableDiagnostics = settings.DiagnosticsOnEdit
	}
	enableFormat := settings.FormatOnWrite

	reqCtx := contextFromSDK(sdkCtx)
	if !enableDiagnostics && !enableFormat {
		_ = defaultManager.NotifyWorkspaceWatchedFiles(reqCtx, cwd, []WatchedFileChange{
			{FilePath: absPath, Type: FileChangeChanged},
		})
		return ""
	}

	res := collectAfterWrite(reqCtx, sdkCtx, defaultManager, sessionID, trusted, ledgerForSession(sessionID), absPath, cwd, settings, enableDiagnostics, enableFormat)
	if res == nil || len(res.Messages) == 0 {
		return ""
	}
	return formatDiagnosticsNotice(res)
}

// formatDiagnosticsNotice matches packages/tui/src/tools/output-meta.ts:424-427.
func formatDiagnosticsNotice(d *FileDiagnosticsResult) string {
	if d == nil || len(d.Messages) == 0 {
		return ""
	}
	return fmt.Sprintf("\n\nLSP Diagnostics (%s):\n%s", d.Summary, formatGroupedDiagnosticMessages(d.Messages))
}

// collectAfterWrite synchronizes the file with matching language servers within
// omp's 5s sync budget, starting each server at most once. When the sync budget
// expires before synchronization finishes, it skips the inline diagnostics fetch
// and returns nil (matching lsp/writethrough.ts:586-613) rather than respawning
// and blocking again.
func collectAfterWrite(
	ctx context.Context,
	sdkCtx sdk.Context,
	m *ClientManager,
	sessionID string,
	trusted bool,
	ledger *DiagnosticsLedger,
	absPath, cwd string,
	settings Settings,
	enableDiagnostics, enableFormat bool,
) *FileDiagnosticsResult {
	cfg := GetConfigWithTrust(cwd, trusted)
	idleTimeoutMs := configIdleTimeoutMs(cfg)
	servers := GetServersForFile(cfg, absPath)
	if len(servers) == 0 {
		_ = m.NotifyWorkspaceWatchedFiles(ctx, cwd, []WatchedFileChange{
			{FilePath: absPath, Type: FileChangeChanged},
		})
		return nil
	}

	content, err := readTextFromDisk(absPath)
	if err != nil {
		return nil
	}
	lspServers, customLinters := splitServers(servers)

	m.beginPendingDiskWrite(absPath)
	syncCtx, syncCancel := context.WithTimeout(ctx, writethroughSyncTimeout)

	// Start/acquire each LSP server at most once during the single 5s sync budget,
	// capturing baseline diagnostic versions before pushing content.
	ownerID, releaseOwner := acquireSessionForCall(m, sessionID)
	defer releaseOwner()
	minVersions := make(map[string]int)
	activeClients := make(map[string]*Client, len(lspServers))
	for _, srv := range lspServers {
		var client *Client
		var cErr error
		if enableDiagnostics || (enableFormat && len(customLinters) == 0) {
			client, cErr = m.GetOrCreateClientForSessionWithTrust(syncCtx, ownerID, trusted, idleTimeoutMs, srv.Config, cwd, writethroughSyncTimeout)
		} else {
			client, cErr = m.GetActiveOrPendingClient(syncCtx, srv.Config, cwd)
			if client != nil {
				m.AcquireForSession(ownerID, client.key)
			}
		}
		if cErr != nil || client == nil {
			continue
		}
		activeClients[srv.Name] = client
		if enableDiagnostics {
			minVersions[srv.Name] = client.DiagnosticsVersion()
		}
	}

	finalContent := content
	writeFormatted := func(formatted string) {
		finalContent = formatted
		_ = withFileMutationQueue(sdkCtx, absPath, func() error {
			if wErr := os.WriteFile(absPath, []byte(finalContent), 0o644); wErr != nil {
				return wErr
			}
			refreshHashlineSnapshot(sdkCtx, absPath, finalContent)
			return nil
		})
	}

	if syncCtx.Err() == nil && enableFormat {
		if len(customLinters) > 0 {
			if formatted, fmtErr := formatContent(syncCtx, m, ownerID, trusted, idleTimeoutMs, absPath, content, cwd, customLinters); fmtErr == nil && !formatted.Failed && !formatted.Unsupported && formatted.Content != content {
				writeFormatted(formatted.Content)
			}
		} else {
			for _, srv := range lspServers {
				if client := activeClients[srv.Name]; client != nil {
					_ = client.SyncContent(syncCtx, m, absPath, content)
				}
			}
			if formatted, fmtErr := formatContent(syncCtx, m, ownerID, trusted, idleTimeoutMs, absPath, content, cwd, lspServers); fmtErr == nil && !formatted.Failed && !formatted.Unsupported && formatted.Content != content {
				writeFormatted(formatted.Content)
			}
		}
	}

	expectedDocVersions := make(map[string]int)
	uri := fileToUri(absPath)
	syncedCount := 0
	if syncCtx.Err() == nil {
		for _, srv := range lspServers {
			client := activeClients[srv.Name]
			if client == nil {
				continue
			}
			if _, recErr := client.ReconcileOpenFilesFromDisk(syncCtx, m, uri, false); recErr != nil {
				continue
			}
			if sErr := client.SyncContent(syncCtx, m, absPath, finalContent); sErr != nil {
				continue
			}
			if v, ok := client.OpenFileVersion(uri); ok {
				expectedDocVersions[srv.Name] = v
			}
			if nErr := client.NotifySaved(syncCtx, absPath); nErr != nil {
				continue
			}
			syncedCount++
		}
	}
	synced := syncCtx.Err() == nil && (len(lspServers) == 0 || syncedCount > 0)
	// Notify watched files BEFORE ending pendingDiskWrite (matching omp
	// writethrough.ts:574,611) so NotifyWorkspaceWatchedFiles preserves a
	// version-matched publishDiagnostics that arrived during SyncContent.
	if synced {
		_ = m.NotifyWorkspaceWatchedFiles(syncCtx, cwd, []WatchedFileChange{
			{FilePath: absPath, Type: FileChangeChanged},
		})
	} else {
		_ = m.NotifyWorkspaceWatchedFiles(ctx, cwd, []WatchedFileChange{
			{FilePath: absPath, Type: FileChangeChanged},
		})
	}
	syncCancel()
	m.endPendingDiskWrite(absPath)
	if !enableDiagnostics || !synced {
		return nil
	}

	waitBudget := singleDiagnosticsWaitTimeout
	if settings.RequestTimeoutSec > 0 {
		reqDur := time.Duration(settings.RequestTimeoutSec) * time.Second
		if reqDur < waitBudget {
			waitBudget = reqDur
		}
	}

	result := getDiagnosticsForFile(ctx, m, absPath, cwd, servers, minVersions, expectedDocVersions, waitBudget)
	if result == nil {
		return nil
	}
	if settings.DiagnosticsDeduplicate && ledger != nil {
		result = ledger.Reduce(absPath, result)
	}
	return result
}

// contextFromSDK derives a Go context.Context cancelled when sdkCtx.Done() closes.
func contextFromSDK(sdkCtx sdk.Context) context.Context {
	done := sdkCtx.Done()
	if done == nil {
		return context.Background()
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		select {
		case <-done:
			cancel()
		case <-ctx.Done():
		}
	}()
	return ctx
}
