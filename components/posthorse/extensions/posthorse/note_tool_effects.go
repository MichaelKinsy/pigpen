package posthorse

import "strings"

// Port of src/note-tool-effects.ts: what a file or notes tool call does to a note path.

// noteAction is the TS NoteAction union (note-tool-effects.ts:3-11). It is a plain string
// so other lanes can compare and switch on the literals.
type noteAction = string

const (
	noteActionRead        noteAction = "read"
	noteActionWrite       noteAction = "write"
	noteActionClear       noteAction = "clear"
	noteActionAppend      noteAction = "append"
	noteActionEdit        noteAction = "edit"
	noteActionCreate      noteAction = "create"
	noteActionCreateEmpty noteAction = "create-empty"
	noteActionDelete      noteAction = "delete"
)

// noteOperation is the TS NoteOperation (note-tool-effects.ts:12-16). hasScope carries the
// difference between an absent scope (undefined) and an empty one.
type noteOperation struct {
	path     string
	action   noteAction
	scope    string
	hasScope bool
}

// noteResult is the part of a tool result verifiedNoteOperations reads (note-tool-effects.ts:176-179).
type noteResult struct {
	isError bool
	details any
}

// bareToolName drops a `server:` or `namespace/` prefix (note-tool-effects.ts:17-20).
func bareToolName(name string) string {
	cut := strings.LastIndexAny(name, ":/")
	return name[cut+1:]
}

// patchHeaderAt matches /^\*\*\* (Update File|Add File|Delete File|Move to): ([^\r\n]+)$/mu
// (note-tool-effects.ts:27-29) at a line start. end is the index after the header; the
// character there is a line terminator or the end of input, which is what `$` requires.
func patchHeaderAt(input string, at int) (kind, path string, end int, ok bool) {
	rest := input[at:]
	if !strings.HasPrefix(rest, "*** ") {
		return "", "", 0, false
	}
	rest = rest[4:]
	for _, candidate := range [...]string{"Update File", "Add File", "Delete File", "Move to"} {
		if !strings.HasPrefix(rest, candidate) {
			continue
		}
		tail := rest[len(candidate):]
		if !strings.HasPrefix(tail, ": ") {
			return "", "", 0, false
		}
		tail = tail[2:]
		stop := strings.IndexAny(tail, "\r\n")
		if stop < 0 {
			stop = len(tail)
		}
		if stop == 0 {
			return "", "", 0, false
		}
		return candidate, tail[:stop], len(input) - len(tail) + stop, true
	}
	return "", "", 0, false
}

// nextLineStart is the index after the next JavaScript line terminator (\n, \r, U+2028,
// U+2029) at or after from, the positions where a multiline `^` matches; -1 when none.
func nextLineStart(input string, from int) int {
	for i := from; i < len(input); i++ {
		switch input[i] {
		case '\n', '\r':
			return i + 1
		case 0xe2:
			if i+2 < len(input) && input[i+1] == 0x80 && (input[i+2] == 0xa8 || input[i+2] == 0xa9) {
				return i + 3
			}
		}
	}
	return -1
}

func isSingleNewline(text string) bool {
	return text == "\r\n" || text == "\r" || text == "\n"
}

// patchOperations is note-tool-effects.ts:21-53.
func patchOperations(input any) []noteOperation {
	text, ok := input.(string)
	if !ok {
		return []noteOperation{}
	}
	operations := []noteOperation{}
	updatePath, updateEnd, hasUpdate := "", 0, false
	for at := 0; at >= 0 && at <= len(text); {
		kind, path, end, matched := patchHeaderAt(text, at)
		next := at
		if matched {
			next = end
			if kind == "Move to" {
				// Only an update header immediately followed by this one moves a file (note-tool-effects.ts:35-44).
				if hasUpdate && isSingleNewline(text[updateEnd:at]) {
					operations[len(operations)-1] = noteOperation{path: updatePath, action: noteActionDelete}
					operations = append(operations, noteOperation{path: path, action: noteActionCreate})
				}
				hasUpdate = false
			} else {
				action := noteActionCreate
				switch kind {
				case "Update File":
					action = noteActionEdit
				case "Delete File":
					action = noteActionDelete
				}
				operations = append(operations, noteOperation{path: path, action: action})
				hasUpdate = action == noteActionEdit
				if hasUpdate {
					updatePath, updateEnd = path, end
				}
			}
		}
		at = nextLineStart(text, next)
	}
	return operations
}

// batchOperations is note-tool-effects.ts:54-67.
func batchOperations(tool string, files any) []noteOperation {
	list, _ := arrOf(files)
	operations := make([]noteOperation, 0, len(list))
	for _, value := range list {
		file, ok := recOf(value)
		if !ok {
			continue
		}
		path, ok := file["path"].(string)
		if !ok {
			continue
		}
		if tool == "replace_text" {
			operations = append(operations, noteOperation{path: path, action: noteActionEdit})
			continue
		}
		empty := file["content"] == ""
		if file["mode"] == "create" {
			action := noteActionCreate
			if empty {
				action = noteActionCreateEmpty
			}
			operations = append(operations, noteOperation{path: path, action: action})
			continue
		}
		action := noteActionWrite
		if empty {
			action = noteActionClear
		}
		operations = append(operations, noteOperation{path: path, action: action})
	}
	return operations
}

// notesAction is note-tool-effects.ts:68-78; ok is false for TS undefined.
func notesAction(args rec) (noteAction, bool) {
	switch args["op"] {
	case "write":
		if args["content"] == "" {
			return noteActionClear, true
		}
		return noteActionWrite, true
	case "append":
		return noteActionAppend, true
	case "read":
		return noteActionRead, true
	}
	return "", false
}

// singleOperation is note-tool-effects.ts:79-97.
func singleOperation(tool string, args rec) []noteOperation {
	path, ok := optionalString(args["path"])
	if !ok {
		return []noteOperation{}
	}
	switch tool {
	case "notes":
		action, ok := notesAction(args)
		if !ok {
			return []noteOperation{}
		}
		scope, hasScope := optionalString(args["scope"])
		return []noteOperation{{path: path, action: action, scope: scope, hasScope: hasScope}}
	case "read", "edit":
		return []noteOperation{{path: path, action: tool}}
	case "write":
		action := noteActionWrite
		if args["content"] == "" {
			action = noteActionClear
		}
		return []noteOperation{{path: path, action: action}}
	}
	return []noteOperation{}
}

// noteOperations is note-tool-effects.ts:98-112.
func noteOperations(tool string, input any) []noteOperation {
	args, _ := recOf(input)
	if args["preview"] == true || tool == "preview_patch" {
		return []noteOperation{}
	}
	switch tool {
	case "apply_patch":
		if text, ok := input.(string); ok {
			return patchOperations(text)
		}
		return patchOperations(args["input"])
	case "write_files", "replace_text":
		return batchOperations(tool, args["files"])
	}
	return singleOperation(tool, args)
}

// noteOperationKind is note-tool-effects.ts:113-129.
func noteOperationKind(action noteAction) string {
	switch action {
	case noteActionRead:
		return "read"
	case noteActionWrite, noteActionCreate:
		return "full-write"
	case noteActionEdit, noteActionAppend:
		return "partial-write"
	}
	return "clear"
}

// committedAction is note-tool-effects.ts:130-148. file is the matching `details.files`
// element, or nil when none matched.
func committedAction(file any, operation *noteOperation, path string) noteAction {
	fallback := noteActionEdit
	if operation != nil {
		fallback = operation.action
	}
	record, ok := recOf(file)
	if !ok {
		return fallback
	}
	// modifiedFiles is path-qualified; a partly committed move can still have status "failed".
	if record["operation"] == "delete" || (record["operation"] == "move" && record["path"] == path) {
		return noteActionDelete
	}
	if bytesAfter, ok := record["bytesAfter"].(float64); ok && bytesAfter == 0 {
		return noteActionClear
	}
	return fallback
}

// committedOperations is note-tool-effects.ts:149-174; ok is false when `modifiedFiles` is
// not an array (TS undefined).
func committedOperations(details rec, operations []noteOperation) ([]noteOperation, bool) {
	modified, ok := arrOf(details["modifiedFiles"])
	if !ok {
		return nil, false
	}
	files, _ := arrOf(details["files"])
	committed := make([]noteOperation, 0, len(modified))
	for _, value := range modified {
		path, ok := value.(string)
		if !ok {
			continue
		}
		var file any
		for _, candidate := range files {
			if record, ok := recOf(candidate); ok && (record["path"] == path || record["moveTo"] == path) {
				file = candidate
				break
			}
		}
		var operation *noteOperation
		for i := range operations {
			if operations[i].path == path {
				operation = &operations[i]
				break
			}
		}
		committed = append(committed, noteOperation{path: path, action: committedAction(file, operation, path)})
	}
	return committed, true
}

// verifiedNoteOperations: pi-apply-edits reports committed paths even when a later
// publication failed (note-tool-effects.ts:175-197).
func verifiedNoteOperations(tool string, input any, result noteResult) []noteOperation {
	operations := noteOperations(tool, input)
	details, hasDetails := recOf(result.details)
	if hasDetails && details["preview"] == true {
		return []noteOperation{}
	}
	if hasDetails && (tool == "replace_text" || tool == "write_files" || tool == "apply_patch") {
		if committed, ok := committedOperations(details, operations); ok {
			return committed
		}
	}
	if result.isError {
		return []noteOperation{}
	}
	return operations
}
