package pi_permission_system

// Lane-local stand-ins for what other lanes of the port own (the lead dedups at integration; the names mirror the TypeScript):
//   - config: PermissionSystemExtensionConfig (here the three fields the writer reads), the log file names,
//     ensurePermissionSystemLogsDirectory (config/extension-config.ts);
//   - authority/prompt: the PromptPayload family (presentation/prompt-payload.ts).

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"syscall"
)

// LogConfig is what the log writer reads of the extension config on every write (the original's getConfig thunk result).
type LogConfig struct {
	DebugLog            bool
	PermissionReviewLog bool
	// ReviewLogFieldMaxWidth is nil when the config names none.
	ReviewLogFieldMaxWidth *float64
}

const (
	debugLogFilename  = extensionID + "-debug.jsonl"
	reviewLogFilename = extensionID + "-permission-review.jsonl"
)

// nodeFsMessage is the message Node gives an fs error: "<CODE>: <description>, <syscall> '<path>'" (a failed write names no path).
func nodeFsMessage(err error, call, path string) string {
	codes := []struct {
		errno      syscall.Errno
		code, text string
	}{
		{syscall.ENOENT, "ENOENT", "no such file or directory"},
		{syscall.EACCES, "EACCES", "permission denied"},
		{syscall.ENOTDIR, "ENOTDIR", "not a directory"},
		{syscall.EEXIST, "EEXIST", "file already exists"},
		{syscall.EISDIR, "EISDIR", "illegal operation on a directory"},
		{syscall.EPERM, "EPERM", "operation not permitted"},
		{syscall.EROFS, "EROFS", "read-only file system"},
		{syscall.ENOSPC, "ENOSPC", "no space left on device"},
	}
	for _, c := range codes {
		if errors.Is(err, c.errno) {
			if call == "write" {
				return fmt.Sprintf("%s: %s, %s", c.code, c.text, call) // fs.writeSync names no path
			}
			return fmt.Sprintf("%s: %s, %s '%s'", c.code, c.text, call, path)
		}
	}
	var pe *fs.PathError
	if errors.As(err, &pe) {
		return pe.Err.Error()
	}
	return err.Error()
}

// ensurePermissionSystemLogsDirectory creates the logs directory owner-only and tightens one inherited from an earlier version.
// It returns "" on success, else the message the original reports.
func ensurePermissionSystemLogsDirectory(logsDir string) string {
	if err := os.MkdirAll(logsDir, OwnerOnlyDirectoryMode); err != nil {
		return "Failed to create permission-system log directory '" + logsDir + "': " + nodeFsMessage(err, "mkdir", logsDir)
	}
	RestrictExistingPathToOwner(logsDir, OwnerOnlyDirectoryMode)
	return ""
}

// PromptPayload is the complete, structured description of a permission ask (ADR 0011 section 2).
type PromptPayload struct {
	// Kind is one of bash, mcp, tool, path, external_directory, bash_external_directory, skill, skill_read, forwarded.
	Kind        string
	Request     PromptRequestFacts
	Evidence    []PromptEvidence
	Annotations []PromptAnnotation
}

// PromptRequestFacts is the invariant core of an ask. A nil pointer is the original's null.
type PromptRequestFacts struct {
	Requester       PromptRequester
	Surface         string
	ToolName        *string
	InvokedToolName *string
	Value           string
	MatchedPattern  *string
	// CommandContext is command_substitution, process_substitution or subshell; nil for a current-shell command.
	CommandContext *string
	ExecutedUnit   *string
}

// PromptRequester is who is asking, one hop below when the ask was forwarded.
type PromptRequester struct {
	AgentName *string
	Forwarded bool
	SessionID *string
}

// PromptEvidence is one piece of decision evidence.
type PromptEvidence struct {
	Label, Text string
	Detail      *string
}

// PromptAnnotation is a model-generated advisory.
type PromptAnnotation struct{ Source, Text string }
