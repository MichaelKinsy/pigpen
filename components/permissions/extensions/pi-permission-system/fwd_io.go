package pi_permission_system

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// Port of src/authority/forwarding-io.ts: the filesystem half of permission forwarding (directories, atomic JSON writes, tolerant
// reads of the files another process wrote, cleanup) and its log helpers.

const (
	// ownerOnlyDirectoryMode and ownerOnlyFileMode are logging/log-file-permissions.ts OWNER_ONLY_DIRECTORY_MODE and
	// OWNER_ONLY_FILE_MODE: forwarded requests carry commands and paths.
	ownerOnlyDirectoryMode os.FileMode = 0o700
	ownerOnlyFileMode      os.FileMode = 0o600
)

// The filesystem calls the transient-lock retry guards. Package variables so a test can fail them the way the original's tests mock
// node:fs; production never reassigns them.
var (
	renameFile = os.Rename
	mkdirAll   = os.MkdirAll
)

// tempFileSeq keeps two concurrent writers of the same path from sharing a temp name: the original is single-threaded, where
// pid and millisecond alone were unique.
var tempFileSeq atomic.Uint64

var uiPromptSources = []string{"tool_call", "skill_input", "skill_read"}

// asApprovalGrant is asApprovalGrant: an object with a non-empty string surface and a string pattern.
func asApprovalGrant(v any) (ApprovalGrant, bool) {
	c, ok := v.(*jsObject)
	if !ok {
		return ApprovalGrant{}, false
	}
	surface, okS := c.str("surface")
	pattern, okP := c.str("pattern")
	if !okS || surface == "" || !okP {
		return ApprovalGrant{}, false
	}
	return ApprovalGrant{Surface: surface, Pattern: pattern}, true
}

// asForwardedSessionApproval is asForwardedSessionApproval: a tolerant read of the child's session-approval suggestion, accepted
// only when well formed (a non-empty grants array whose every entry names a non-empty surface and a pattern). The pre-#810 shape is
// rejected rather than normalized.
func asForwardedSessionApproval(v any) *ForwardedSessionApproval {
	c, ok := v.(*jsObject)
	if !ok {
		return nil
	}
	raw, ok := c.vals["grants"].([]any)
	if !ok || len(raw) == 0 {
		return nil
	}
	grants := make([]ApprovalGrant, 0, len(raw))
	for _, entry := range raw {
		g, ok := asApprovalGrant(entry)
		if !ok {
			return nil
		}
		grants = append(grants, g)
	}
	return &ForwardedSessionApproval{Grants: grants}
}

// asForwardedAccessIntent is asForwardedAccessIntent: the child-fixed access intent, accepted only when fully well formed; any
// malformed shape is nil, so the serving node floors to ask rather than resolving against corrupt facts.
func asForwardedAccessIntent(v any) *ForwardedAccessIntent {
	c, ok := v.(*jsObject)
	if !ok {
		return nil
	}
	surface, okSurface := c.str("surface")
	rawValues, okValues := c.vals["matchValues"].([]any)
	boundary, okBoundary := nullableString(c, "boundaryValue")
	cwd, okCwd := c.str("requesterCwd")
	principal, okPrincipal := c.vals["principal"].(*jsObject)
	if !okSurface || !okValues || !okBoundary || !okCwd || !okPrincipal {
		return nil
	}
	values := make([]string, 0, len(rawValues))
	for _, entry := range rawValues {
		s, ok := entry.(string)
		if !ok {
			return nil
		}
		values = append(values, s)
	}
	sessionID, okSession := principal.str("sessionId")
	agentName, okAgent := principal.str("agentName")
	if !okSession || !okAgent {
		return nil
	}
	return &ForwardedAccessIntent{
		ForwardedAccessFacts: ForwardedAccessFacts{Surface: surface, MatchValues: values, BoundaryValue: boundary},
		RequesterCwd:         cwd,
		Principal:            ForwardedPrincipal{SessionID: sessionID, AgentName: agentName},
	}
}

// FormatUnknownErrorMessage is formatUnknownErrorMessage: an error's message, or String(value) for anything else or for an error
// with an empty message (which reads "Error").
func FormatUnknownErrorMessage(err any) string {
	switch e := err.(type) {
	case error:
		if msg := e.Error(); msg != "" {
			return msg
		}
		return "Error"
	case string:
		return e
	case nil:
		return "undefined"
	case float64:
		return jsNumber(e)
	case int:
		return strconv.Itoa(e)
	case bool:
		return strconv.FormatBool(e)
	}
	return fmt.Sprint(err)
}

// IsErrnoCode is isErrnoCode: whether err carries the errno code.
func IsErrnoCode(err error, code string) bool { return err != nil && FsErrnoCode(err) == code }

func warnDetails(message string, err error) *jsObject {
	d := newObject()
	d.set("message", message)
	if err != nil {
		d.set("error", FormatUnknownErrorMessage(err))
	}
	return d
}

// LogPermissionForwardingWarning logs a warning to both the review and debug logs. A nil logger silently no-ops.
func LogPermissionForwardingWarning(logger DebugReviewLogger, message string, err error) {
	if logger == nil {
		return
	}
	logger.Review("permission_forwarding.warning", warnDetails(message, err))
	logger.Debug("permission_forwarding.warning", warnDetails(message, err))
}

// LogPermissionForwardingError logs an error to both the review and debug logs. A nil logger silently no-ops.
func LogPermissionForwardingError(logger DebugReviewLogger, message string, err error) {
	if logger == nil {
		return
	}
	logger.Review("permission_forwarding.error", warnDetails(message, err))
	logger.Debug("permission_forwarding.error", warnDetails(message, err))
}

// EnsureDirectoryExists creates path (and its parents) owner-only, retrying a transient lock, and reports whether it exists.
func EnsureDirectoryExists(logger DebugReviewLogger, path, description string) bool {
	record, err := RetryOnTransientFsError(func() error { return wrapFsError(mkdirAll(path, ownerOnlyDirectoryMode), "mkdir", path) }, nil)
	if err != nil {
		LogPermissionForwardingError(logger, fmt.Sprintf("Failed to create %s directory '%s'", description, path), err)
		return false
	}
	recordFsRetry(logger, "mkdir", path, record)
	return true
}

// GetPermissionForwardingLocationForSession is getPermissionForwardingLocationForSession.
func GetPermissionForwardingLocationForSession(forwardingDir, sessionID string) (PermissionForwardingLocation, error) {
	return CreatePermissionForwardingLocation(forwardingDir, sessionID)
}

// EnsurePermissionForwardingLocation resolves a session's location and creates its three directories; false when any is missing.
func EnsurePermissionForwardingLocation(logger DebugReviewLogger, forwardingDir, sessionID string) (PermissionForwardingLocation, bool) {
	location, err := GetPermissionForwardingLocationForSession(forwardingDir, sessionID)
	if err != nil {
		LogPermissionForwardingError(logger, "Failed to resolve permission forwarding location", err)
		return PermissionForwardingLocation{}, false
	}
	// All three are attempted even after a failure, as the original does.
	rootReady := EnsureDirectoryExists(logger, location.SessionRootDir, "permission forwarding session root")
	requestsReady := EnsureDirectoryExists(logger, location.RequestsDir, "permission forwarding requests")
	responsesReady := EnsureDirectoryExists(logger, location.ResponsesDir, "permission forwarding responses")
	if rootReady && requestsReady && responsesReady {
		return location, true
	}
	return PermissionForwardingLocation{}, false
}

// GetExistingPermissionForwardingLocation resolves a session's location, or false when its requests directory does not exist.
func GetExistingPermissionForwardingLocation(forwardingDir, sessionID string) (PermissionForwardingLocation, bool) {
	location, err := GetPermissionForwardingLocationForSession(forwardingDir, sessionID)
	if err != nil || !pathExists(location.RequestsDir) {
		return PermissionForwardingLocation{}, false
	}
	return location, true
}

// pathExists is existsSync: false on any error.
func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// TryRemoveDirectoryIfEmpty removes a directory if it is empty. It reports true when the directory is absent after the call
// (removed, or never there) and false when it still exists (non-empty, or a filesystem error prevented removal).
func TryRemoveDirectoryIfEmpty(logger DebugReviewLogger, path, description string) bool {
	if !pathExists(path) {
		return true
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		LogPermissionForwardingWarning(logger, fmt.Sprintf("Failed to inspect %s directory '%s'", description, path), wrapFsError(err, "scandir", path))
		return false
	}
	if len(entries) > 0 {
		return false
	}
	if err := wrapFsError(os.Remove(path), "rmdir", path); err != nil {
		if IsErrnoCode(err, "ENOENT") {
			return true
		}
		if IsErrnoCode(err, "ENOTEMPTY") {
			return false
		}
		LogPermissionForwardingWarning(logger, fmt.Sprintf("Failed to remove empty %s directory '%s'", description, path), err)
		return false
	}
	return true
}

// CleanupPermissionForwardingLocationIfEmpty removes the location's empty directories. responses/ goes only when requests/ is
// already gone: removing it while a request is pending causes the ENOENT write loop (#398).
func CleanupPermissionForwardingLocationIfEmpty(logger DebugReviewLogger, location PermissionForwardingLocation) {
	requestsGone := TryRemoveDirectoryIfEmpty(logger, location.RequestsDir, location.Label+" permission forwarding requests")
	if requestsGone {
		TryRemoveDirectoryIfEmpty(logger, location.ResponsesDir, location.Label+" permission forwarding responses")
	}
	TryRemoveDirectoryIfEmpty(logger, location.SessionRootDir, location.Label+" permission forwarding session root")
}

// SafeDeleteFile deletes a file; a missing one is fine, any other failure is logged as a warning.
func SafeDeleteFile(logger DebugReviewLogger, filePath, description string) {
	if err := wrapFsError(os.Remove(filePath), "unlink", filePath); err != nil {
		if IsErrnoCode(err, "ENOENT") {
			return
		}
		LogPermissionForwardingWarning(logger, fmt.Sprintf("Failed to delete %s file '%s'", description, filePath), err)
	}
}

// WriteJSONFileAtomic writes value as compact JSON (JSON.stringify) to a temp file beside filePath, owner-only, and renames it into
// place, retrying a transient lock on the rename. On failure the temp file is removed and the error returned; no partial
// destination is left. value is a *jsObject, map, slice or scalar (marshalJSON).
func WriteJSONFileAtomic(logger DebugReviewLogger, filePath string, value any) error {
	tempPath := fmt.Sprintf("%s.%d.%d.%d.tmp", filePath, os.Getpid(), time.Now().UnixMilli(), tempFileSeq.Add(1))
	// rename preserves the temp file's mode, so setting it here is enough. The temp write is deliberately outside the retry: its
	// failure shape is a write-denied directory, which no number of attempts resolves.
	err := wrapFsError(os.WriteFile(tempPath, []byte(marshalJSON(value, "")), ownerOnlyFileMode), "open", tempPath)
	if err == nil {
		var record TransientFsRetryRecord
		record, err = RetryOnTransientFsError(func() error { return wrapFsError2(renameFile(tempPath, filePath), "rename", tempPath, filePath) }, nil)
		if err == nil {
			recordFsRetry(logger, "rename", filePath, record)
			return nil
		}
	}
	SafeDeleteFile(logger, tempPath, "temporary permission-forwarding")
	return err
}

// recordFsRetry records a filesystem operation that only succeeded after retrying, on the debug stream alone: a recovered write
// decided nothing, but attempts and code are the whole diagnosis when a host keeps losing writes.
func recordFsRetry(logger DebugReviewLogger, operation, path string, record TransientFsRetryRecord) {
	if record.Attempts == 1 || logger == nil {
		return
	}
	d := newObject()
	d.set("operation", operation)
	d.set("path", path)
	d.set("attempts", float64(record.Attempts))
	if record.Code == "" {
		d.set("code", nil)
	} else {
		d.set("code", record.Code)
	}
	logger.Debug("permission_forwarding.fs_retried", d)
}

// readJSONObject reads and parses a forwarding file. ok is false with a nil error when the content parsed to something that is not
// an object (the original's invalid-format branch); err is a read or parse failure.
func readJSONObject(filePath string) (obj *jsObject, err error) {
	raw, err := os.ReadFile(filePath)
	if err != nil {
		return nil, wrapFsError(err, "open", filePath)
	}
	v, err := parseJSON(raw)
	if err != nil {
		// The message V8 gives the same text: the review log records `error.message` of the original's JSON.parse.
		if msg := v8JSONError(string(raw)); msg != "" {
			err = errors.New(msg)
		}
		return nil, err
	}
	obj, _ = v.(*jsObject)
	return obj, nil
}

// ReadForwardedPermissionRequest reads a request file tolerantly: nil when it is unreadable or lacks the required core. The payload
// and display fields are optional and only reconstructed when well formed; an older child's message is deliberately not salvaged.
func ReadForwardedPermissionRequest(logger DebugReviewLogger, filePath string) *ForwardedPermissionRequest {
	c, err := readJSONObject(filePath)
	if err != nil {
		LogPermissionForwardingWarning(logger, fmt.Sprintf("Failed to read forwarded permission request '%s'", filePath), err)
		return nil
	}
	if c == nil {
		LogPermissionForwardingWarning(logger, fmt.Sprintf("Ignoring invalid forwarded permission request format in '%s'", filePath), nil)
		return nil
	}
	id, okID := c.str("id")
	createdAt, okAt := c.num("createdAt")
	requester, okRequester := c.str("requesterSessionId")
	target, okTarget := c.str("targetSessionId")
	agent, okAgent := c.str("requesterAgentName")
	if !okID || !okAt || !okRequester || !okTarget || !okAgent {
		LogPermissionForwardingWarning(logger, fmt.Sprintf("Ignoring invalid forwarded permission request format in '%s'", filePath), nil)
		return nil
	}
	request := &ForwardedPermissionRequest{
		ID: id, CreatedAt: createdAt, RequesterSessionID: requester, TargetSessionID: target, RequesterAgentName: agent,
		Payload:         AsPromptPayload(c.vals["payload"]),
		SessionApproval: asForwardedSessionApproval(c.vals["sessionApproval"]),
		AccessIntent:    asForwardedAccessIntent(c.vals["accessIntent"]),
	}
	if source, ok := c.str("source"); ok && inStrings(uiPromptSources, source) {
		request.Source = source
	}
	if v, ok := nullableString(c, "surface"); ok {
		request.Surface = DisplayString{Set: true, Value: v}
	}
	if v, ok := nullableString(c, "value"); ok {
		request.Value = DisplayString{Set: true, Value: v}
	}
	return request
}

// ReadForwardedPermissionResponse reads a response file tolerantly: nil when it is unreadable or lacks the required core. An
// unusable decidedBy or sessionGrantWidth is dropped without rejecting the response, since the decision itself has to reach the
// requester.
func ReadForwardedPermissionResponse(logger DebugReviewLogger, filePath string) *ForwardedPermissionResponse {
	c, err := readJSONObject(filePath)
	if err != nil {
		LogPermissionForwardingWarning(logger, fmt.Sprintf("Failed to read forwarded permission response '%s'", filePath), err)
		return nil
	}
	if c == nil {
		LogPermissionForwardingWarning(logger, fmt.Sprintf("Ignoring invalid forwarded permission response format in '%s'", filePath), nil)
		return nil
	}
	approved, okApproved := c.flag("approved")
	state, hasState := c.str("state")
	responder, okResponder := c.str("responderSessionId")
	if !okApproved || !hasState || !IsPermissionDecisionState(state) || !okResponder {
		LogPermissionForwardingWarning(logger, fmt.Sprintf("Ignoring invalid forwarded permission response format in '%s'", filePath), nil)
		return nil
	}
	response := &ForwardedPermissionResponse{
		Approved:           approved,
		State:              PermissionDecisionState(state),
		ResponderSessionID: responder,
		DecidedBy:          AsDecisionSource(c.vals["decidedBy"]),
	}
	if reason, ok := c.str("denialReason"); ok {
		response.DenialReason = &reason
	}
	if at, ok := c.num("respondedAt"); ok {
		response.RespondedAt = at
	} else {
		response.RespondedAt = float64(time.Now().UnixMilli())
	}
	if width, ok := c.str("sessionGrantWidth"); ok && IsSessionGrantWidth(width) {
		response.SessionGrantWidth = SessionGrantWidth(width)
	}
	return response
}

// ListRequestFiles lists the *.json files of a requests directory, sorted; a read failure is logged and reads as empty.
func ListRequestFiles(logger DebugReviewLogger, requestsDir string) []string {
	entries, err := os.ReadDir(requestsDir)
	if err != nil {
		LogPermissionForwardingWarning(logger, fmt.Sprintf("Failed to read permission forwarding requests from '%s'", requestsDir), wrapFsError(err, "scandir", requestsDir))
		return []string{}
	}
	names := []string{}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".json") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names
}
