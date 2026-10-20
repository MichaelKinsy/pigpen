package powerline_footer

import (
	"strings"
	"sync"
)

// The bash transcript store. upstream: bash-mode/transcript.ts. It is read by the widget renderer and written by the shell
// session's reader goroutines, so it is guarded by a mutex (upstream is single-threaded).

// bashCompactLines is bashCompactLines(): carriage returns removed, an empty chunk kept as one empty line, otherwise split at "\n".
func bashCompactLines(lines []string) []string {
	var normalized []string
	for _, line := range lines {
		sanitized := strings.ReplaceAll(line, "\r", "")
		if len(sanitized) == 0 {
			normalized = append(normalized, "")
			continue
		}
		normalized = append(normalized, strings.Split(sanitized, "\n")...)
	}
	return normalized
}

type bashTranscriptStore struct {
	mu                sync.Mutex
	settings          bashTranscriptLimits
	commands          []*bashCommandRecord
	commandIndex      map[string]*bashCommandRecord
	totalLines        int
	totalBytes        int
	truncatedCommands int
}

func newBashTranscriptStore(settings bashTranscriptLimits) *bashTranscriptStore {
	return &bashTranscriptStore{settings: settings, commandIndex: map[string]*bashCommandRecord{}}
}

// startCommand upstream: transcript.ts:32. The returned record is the live one, as upstream returns it.
func (s *bashTranscriptStore) startCommand(id, command, cwdAtStart string) *bashCommandRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry := &bashCommandRecord{ID: id, Command: command, CwdAtStart: cwdAtStart, StartedAt: clock(), Output: []string{}}
	s.commands = append(s.commands, entry)
	s.commandIndex[id] = entry
	s.enforceLimits()
	return entry
}

// appendOutput upstream: transcript.ts:51. A byte of output is counted per UTF-8 byte plus one for the line break.
func (s *bashTranscriptStore) appendOutput(id, chunk string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry := s.commandIndex[id]
	if entry == nil || chunk == "" {
		return
	}
	lines := bashCompactLines([]string{chunk})
	if len(lines) == 0 {
		return
	}
	entry.Output = append(entry.Output, lines...)
	added := 0
	for _, line := range lines {
		added += len(line) + 1
	}
	entry.OutputBytes += added
	s.totalLines += len(lines)
	s.totalBytes += added
	s.enforceLimits()
}

// finishCommand upstream: transcript.ts:66.
func (s *bashTranscriptStore) finishCommand(id string, exitCode int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry := s.commandIndex[id]
	if entry == nil {
		return
	}
	entry.ExitCode = &exitCode
	finished := clock()
	entry.FinishedAt = &finished
}

// clear upstream: transcript.ts:74.
func (s *bashTranscriptStore) clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.commands = nil
	s.commandIndex = map[string]*bashCommandRecord{}
	s.totalLines, s.totalBytes, s.truncatedCommands = 0, 0, 0
}

// getSnapshot upstream: transcript.ts:82. Records are copied, with their own output slice.
func (s *bashTranscriptStore) getSnapshot() bashTranscriptSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	commands := make([]bashCommandRecord, len(s.commands))
	for i, c := range s.commands {
		copied := *c
		copied.Output = append([]string{}, c.Output...)
		if c.ExitCode != nil {
			code := *c.ExitCode
			copied.ExitCode = &code
		}
		if c.FinishedAt != nil {
			at := *c.FinishedAt
			copied.FinishedAt = &at
		}
		commands[i] = copied
	}
	return bashTranscriptSnapshot{Commands: commands, TotalLines: s.totalLines, TotalBytes: s.totalBytes, TruncatedCommands: s.truncatedCommands}
}

// enforceLimits drops the oldest commands, whole, until the limits hold; the newest command is always kept, even when it alone
// exceeds them. upstream: transcript.ts:94.
func (s *bashTranscriptStore) enforceLimits() {
	for len(s.commands) > 1 && (s.totalLines > s.settings.MaxLines || s.totalBytes > s.settings.MaxBytes) {
		removed := s.commands[0]
		s.commands = s.commands[1:]
		delete(s.commandIndex, removed.ID)
		s.totalLines = max(0, s.totalLines-len(removed.Output))
		s.totalBytes = max(0, s.totalBytes-removed.OutputBytes)
		s.truncatedCommands++
		removed.Truncated = true
	}
}
