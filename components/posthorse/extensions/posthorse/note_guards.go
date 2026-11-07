package posthorse

import (
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// Port of src/note-guards.ts: scope ownership for the native file tools and the batch
// editing tools, plus the tool_call/tool_result/tool_execution_end bookkeeping.

// capturedCall is CapturedCall (note-guards.ts:44-49): the session and call a tool_call
// handler captured before execution.
type capturedCall struct {
	session *sessionNotes
	call    noteCall
	cwd     string
	tool    string
}

// unicodeSpaces is /[\u00A0\u2000-\u200A\u202F\u205F\u3000]/gu (note-guards.ts:30).
func normalizeToolSpaces(path string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == 0x00A0, r >= 0x2000 && r <= 0x200A, r == 0x202F, r == 0x205F, r == 0x3000:
			return ' '
		}
		return r
	}, path)
}

// toolPath: native core tools expand @/~ paths; pi-apply-edits deliberately treats them
// literally (note-guards.ts:25-42).
func toolPath(tool, path, cwd string) (string, error) {
	if tool != "read" && tool != "write" && tool != "edit" {
		if runtime.GOOS == "windows" {
			return nodeResolve(cwd, path), nil
		}
		if nodeIsAbsolute(path) {
			return path, nil
		}
		return cwd + "/" + path, nil
	}
	normalized := strings.TrimPrefix(normalizeToolSpaces(path), "@")
	if normalized == "~" || strings.HasPrefix(normalized, "~/") {
		home, err := nodeHomedir()
		if err != nil {
			return "", err
		}
		normalized = nodeJoin2(home, normalized[1:])
	}
	if strings.HasPrefix(normalized, "file://") {
		converted, err := fileURLToPathOS(normalized, runtime.GOOS == "windows")
		if err != nil {
			return "", err
		}
		normalized = converted
	}
	return nodeResolve(cwd, normalized), nil
}

// optionalAddress is `NoteAddress | undefined`.
type optionalAddress struct {
	address noteAddress
	ok      bool
}

// relativeParts is relative(root, path).split(sep) (note-guards.ts:56).
func relativeParts(root, path string) []string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		rel = path
	}
	if rel == "." {
		rel = ""
	}
	return strings.Split(rel, string(filepath.Separator))
}

// noteAddressOf is noteAddress (note-guards.ts:51-71): the address of a path in a known
// scope, or in a fork scope found by its `.forks/<id>/notes` layout.
func noteAddressOf(ctx sdk.Context, path string, session *sessionNotes) (optionalAddress, error) {
	known, ok, err := addressOf(path, session.scopes)
	if err != nil {
		return optionalAddress{}, err
	}
	if ok {
		return optionalAddress{known, true}, nil
	}
	canonical, err := canonicalPath(path)
	if err != nil {
		return optionalAddress{}, err
	}
	for _, scope := range session.scopes {
		parts := relativeParts(scope.root, canonical)
		if len(parts) < 4 || parts[0] != ".forks" || parts[2] != "notes" {
			continue
		}
		selected, err := session.selectScope(ctx, filepath.Join(scope.root, ".forks", parts[1], "notes"))
		if err != nil {
			return optionalAddress{}, err
		}
		address, ok, err := addressOf(path, []noteScope{selected})
		return optionalAddress{address, ok}, err
	}
	return optionalAddress{}, nil
}

// existingEdit is note-guards.ts:72-78.
func existingEdit(tool string, operation noteOperation) bool {
	return operation.action == noteActionEdit ||
		operation.action == noteActionDelete ||
		(tool == "write_files" && (operation.action == noteActionWrite || operation.action == noteActionClear))
}

// guardForeign is note-guards.ts:79-95.
func guardForeign(ctx sdk.Context, address noteAddress, operation noteOperation, captured capturedCall) (string, error) {
	session, tool, call := captured.session, captured.tool, captured.call
	if operation.action == noteActionRead {
		return "This is another notes scope. Use notes read with " + jsonObject2("scope", scopeDirectory(address), "path", address.path) + " for a labeled read-only comparison, or omit scope to read this session’s copy.", nil
	}
	if existingEdit(tool, operation) {
		for _, scope := range session.scopes {
			if sameScope(scope, address) {
				return session.guardReference(ctx, address.path, call)
			}
		}
	}
	destination, err := notePath(session.binding, address.path)
	if err != nil {
		return "", err
	}
	return "The foreign note was not changed. This session writes " + destination + ". Retry explicitly there; creation semantics were not changed.", nil
}

// guardOperation is note-guards.ts:96-127; reason "" is TS undefined.
func guardOperation(ctx sdk.Context, operation noteOperation, captured capturedCall) (string, error) {
	session, call, tool, cwd := captured.session, captured.call, captured.tool, captured.cwd
	path, err := toolPath(tool, operation.path, cwd)
	if err != nil {
		return "", err
	}
	found, err := noteAddressOf(ctx, path, session)
	if err != nil {
		return "", err
	}
	if !found.ok {
		if operation.action == noteActionRead {
			return "", nil
		}
		target, err := canonicalPath(path)
		if err != nil {
			return "", err
		}
		for _, scope := range session.scopes {
			forks, err := canonicalPath(filepath.Join(scope.root, ".forks"))
			if err != nil {
				return "", err
			}
			if inside(forks, target) {
				return "Managed notes metadata is not editable through file tools: " + path + ". Use notes for this session’s checkpoint.", nil
			}
		}
		return "", nil
	}
	address := found.address
	if !sameScope(address, session.binding) {
		return guardForeign(ctx, address, operation, captured)
	}
	if existingEdit(tool, operation) {
		owned, err := session.owns(address.path)
		if err != nil {
			return "", err
		}
		if !owned {
			copied, err := sdk.WithFileMutationQueue(path, func() (string, error) {
				return session.adopt(ctx, address.path, call)
			})
			if err != nil {
				return "", err
			}
			if copied == "adopted" {
				return "Private copy created at " + path + "; no requested edit ran. Read this copy, then retry the edit.", nil
			}
		}
	}
	if operation.action != noteActionRead {
		if err := session.prepareWrite(); err != nil {
			return "", err
		}
	}
	return "", nil
}

// recordedFiles is note-guards.ts:128-157. Each result belongs to the scope captured
// before execution, even after /tree.
func recordedFiles(ctx sdk.Context, operations []noteOperation, captured capturedCall) ([]noteTouchFile, error) {
	addresses, err := mapBatch(operations, func(operation noteOperation) (optionalAddress, error) {
		path, err := toolPath(captured.tool, operation.path, captured.cwd)
		if err != nil {
			return optionalAddress{}, err
		}
		return noteAddressOf(ctx, path, captured.session)
	})
	if err != nil {
		return nil, err
	}
	files := make([]noteTouchFile, 0, len(operations))
	for i, operation := range operations {
		if !addresses[i].ok {
			continue
		}
		kind := "write"
		switch operation.action {
		case noteActionRead, noteActionDelete:
			kind = operation.action
		case noteActionClear, noteActionCreateEmpty:
			kind = "clear"
		}
		files = append(files, noteTouchFile{noteAddress: addresses[i].address, kind: kind})
	}
	return files, nil
}

// operationInsideRoots is `operations.some((operation) => store.roots().some((root) =>
// inside(root, canonicalPath(toolPath(...)))))` (note-guards.ts:162-176): the path is parsed
// only once there is a root to compare it with, so with no known root a malformed path never throws.
func operationInsideRoots(store *notesStore, tool string, operations []noteOperation, cwd string) (bool, error) {
	for _, operation := range operations {
		roots := store.roots()
		if len(roots) == 0 {
			continue
		}
		path, err := toolPath(tool, operation.path, cwd)
		if err != nil {
			return false, err
		}
		canonical, err := canonicalPath(path)
		if err != nil {
			return false, err
		}
		for _, root := range roots {
			if inside(root, canonical) {
				return true, nil
			}
		}
	}
	return false, nil
}

func eventString(data map[string]any, key string) (string, bool) {
	value, ok := data[key].(string)
	return value, ok
}

// registerNoteGuards is note-guards.ts:158-204. Parallel tool calls share one map, so it
// is guarded.
func registerNoteGuards(ext *sdk.Extension, store *notesStore) {
	var mu sync.Mutex
	calls := map[string]capturedCall{}

	ext.OnEvent("tool_call", func(ctx sdk.Context, event map[string]any) (any, error) {
		toolName, _ := eventString(event, "toolName")
		toolCallID, _ := eventString(event, "toolCallId")
		operations := noteOperations(toolName, event["input"])
		session, err := store.get(ctx)
		if err != nil {
			if toolName == "notes" {
				return nil, err
			}
			guarded, guardErr := operationInsideRoots(store, toolName, operations, ctx.Cwd())
			if guardErr != nil {
				return nil, guardErr
			}
			if guarded {
				return nil, err
			}
			return nil, nil
		}
		var parent *string
		if value, ok := eventString(event, "parentToolCallId"); ok {
			parent = &value
		}
		call := session.startCall(toolCallID, parent)
		captured := capturedCall{session: session, call: call, cwd: ctx.Cwd(), tool: toolName}
		mu.Lock()
		calls[toolCallID] = captured
		mu.Unlock()
		if toolName == "notes" || len(operations) == 0 {
			return nil, nil
		}
		for _, operation := range operations {
			// Preflight never mutates arguments; a copied reference needs a fresh read before retry.
			reason, err := guardOperation(ctx, operation, captured)
			if err != nil {
				return nil, err
			}
			if reason != "" {
				return map[string]any{"block": true, "reason": reason}, nil
			}
		}
		return nil, nil
	})

	ext.OnToolResult(func(ctx sdk.Context, event map[string]any) (any, error) {
		toolCallID, _ := eventString(event, "toolCallId")
		toolName, _ := eventString(event, "toolName")
		mu.Lock()
		captured, ok := calls[toolCallID]
		mu.Unlock()
		if !ok || toolName == "notes" {
			return nil, nil
		}
		isError, _ := event["isError"].(bool)
		operations := verifiedNoteOperations(toolName, event["input"], noteResult{isError: isError, details: event["details"]})
		files, err := recordedFiles(ctx, operations, captured)
		if err != nil {
			return nil, err
		}
		return nil, captured.session.record(ctx, files, captured.call)
	})

	ext.OnEvent("tool_execution_end", func(ctx sdk.Context, event map[string]any) (any, error) {
		toolCallID, _ := eventString(event, "toolCallId")
		mu.Lock()
		captured, ok := calls[toolCallID]
		delete(calls, toolCallID)
		mu.Unlock()
		if ok {
			captured.session.finishCall(toolCallID)
		}
		return nil, nil
	})
}
