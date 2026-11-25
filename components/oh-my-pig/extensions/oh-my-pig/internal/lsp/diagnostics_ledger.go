package lsp

import (
	"regexp"
	"sync"
)

// upstream: packages/coding-agent/src/lsp/diagnostics-ledger.ts

var diagnosticLocationPrefixRe = regexp.MustCompile(`^.*?:\d+:\d+\s+`)

// diagnosticIdentity strips path, line, and column so shifted diagnostics keep
// the same identity across edits.
func diagnosticIdentity(message string) string {
	return diagnosticLocationPrefixRe.ReplaceAllString(message, "")
}

// DiagnosticsLedger tracks diagnostics already surfaced per file in a session
// so post-write/edit output only reports new or changed diagnostics.
type DiagnosticsLedger struct {
	mu   sync.Mutex
	seen map[string]map[string]struct{}
}

func NewDiagnosticsLedger() *DiagnosticsLedger {
	return &DiagnosticsLedger{
		seen: make(map[string]map[string]struct{}),
	}
}

// Reduce filters already-shown diagnostics for absPath and updates the seen set.
// upstream: packages/coding-agent/src/lsp/diagnostics-ledger.ts:DiagnosticsLedger.reduce
func (l *DiagnosticsLedger) Reduce(absPath string, result *FileDiagnosticsResult) *FileDiagnosticsResult {
	if result == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	previous := l.seen[absPath]
	current := make(map[string]struct{}, len(result.Messages))
	fresh := make([]string, 0, len(result.Messages))

	for _, msg := range result.Messages {
		id := diagnosticIdentity(msg)
		current[id] = struct{}{}
		if previous == nil {
			fresh = append(fresh, msg)
			continue
		}
		if _, already := previous[id]; !already {
			fresh = append(fresh, msg)
		}
	}

	if len(current) == 0 {
		delete(l.seen, absPath)
	} else {
		l.seen[absPath] = current
	}

	if len(fresh) == len(result.Messages) {
		return result
	}

	summary, errored := summarizeDiagnosticMessages(fresh)
	copyRes := *result
	copyRes.Messages = fresh
	copyRes.Summary = summary
	copyRes.Errored = errored
	return &copyRes
}

func (l *DiagnosticsLedger) Reset() {
	l.mu.Lock()
	l.seen = make(map[string]map[string]struct{})
	l.mu.Unlock()
}
