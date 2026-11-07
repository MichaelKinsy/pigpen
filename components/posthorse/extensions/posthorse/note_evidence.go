package posthorse

import (
	"path/filepath"
	"sort"
)

// Port of src/note-evidence.ts.

// notesRecovery is NotesRecovery (note-evidence.ts:17).
type notesRecovery struct {
	binding *noteBinding
	entries []rec
}

// noteEvidence is NoteEvidence (note-evidence.ts:21). retained is a set of entry ids; directories is
// the optional insertion-ordered directory set (nil = undefined).
type noteEvidence struct {
	branch      []rec
	allEntries  []rec
	projected   []projectedEntry
	retained    map[string]struct{}
	directories []string
}

// noteEvidenceResult is the result of sessionNoteTouches; defaultDir "" is undefined.
type noteEvidenceResult struct {
	touches    []noteTouch
	defaultDir string
	visibleIds map[string]struct{}
}

// entryID is `entry.id`; ok is false for undefined.
func entryID(entry rec) (string, bool) { return str(entry, "id") }

// projectedNotes: note-evidence.ts:30. Compaction removes context, not provenance. Only
// active-branch edits suppress old receipts.
func projectedNotes(evidence noteEvidence) []scannedNoteEntry {
	edits := contextEdits(evidence.branch)
	omitted := map[string]struct{}{}
	for _, p := range evidence.projected {
		if len(p.messages) == 0 {
			if id, ok := entryID(p.sourceEntry); ok {
				omitted[id] = struct{}{}
			}
		}
	}
	out := make([]scannedNoteEntry, 0, len(evidence.branch))
	for _, entry := range evidence.branch {
		id, _ := entryID(entry)
		edit, edited := edits[id]
		replacement, hasReplacement := edit["replacement"]
		if _, skip := omitted[id]; skip || (edited && hasReplacement && replacement == nil) {
			continue
		}
		effective := entry
		if edited && hasReplacement {
			if t, _ := str(entry, "type"); t == "message" {
				message := rec{}
				if m, ok := recOf(entry["message"]); ok {
					for k, v := range m {
						message[k] = v
					}
				}
				r, _ := recOf(replacement)
				if content, present := r["content"]; present {
					message["content"] = content
				} else {
					delete(message, "content")
				}
				effective = make(rec, len(entry))
				for k, v := range entry {
					effective[k] = v
				}
				effective["message"] = message
			}
		}
		editID := id
		if edited {
			if eid, ok := entryID(edit); ok {
				editID = eid
			}
		}
		_, retained := evidence.retained[id]
		out = append(out, scannedNoteEntry{entry: effective, entryId: editID, inRetained: retained})
	}
	return out
}

// receiptKey: note-evidence.ts:51.
func receiptKey(entry rec) (string, bool) {
	t, _ := str(entry, "type")
	if t == "message" {
		if message, ok := recOf(entry["message"]); ok {
			if role, _ := str(message, "role"); role == "toolResult" {
				return str(message, "toolCallId")
			}
		}
		return "", false
	}
	if t == "custom_message" {
		if details, ok := recOf(entry["details"]); ok {
			return optionalString(details["runId"])
		}
	}
	return "", false
}

// effectReceipts: note-evidence.ts:58.
func effectReceipts(branch []rec) map[string]rec {
	receipts := map[string]rec{}
	for _, entry := range branch {
		if key, ok := receiptKey(entry); ok {
			// A delivered completion wins over the launch, including when an edit omits it.
			receipts[key] = entry
		}
	}
	return receipts
}

// recordedTouches: note-evidence.ts:69.
func recordedTouches(evidence noteEvidence, visible []scannedNoteEntry) []noteTouch {
	receipts := effectReceipts(evidence.branch)
	shown := make(map[string]*scannedNoteEntry, len(visible))
	var shownUndefined *scannedNoteEntry
	for i := range visible {
		if id, ok := entryID(visible[i].entry); ok {
			shown[id] = &visible[i]
		} else {
			shownUndefined = &visible[i]
		}
	}
	seen := map[[3]string]struct{}{}
	var out []noteTouch
	for _, record := range touchRecords(evidence.allEntries) {
		var item *scannedNoteEntry
		if receipt, ok := receipts[record.rootToolCallId]; ok {
			if id, ok := entryID(receipt); ok {
				item = shown[id]
			} else {
				item = shownUndefined
			}
		} else {
			item = shownUndefined
		}
		key := [3]string{record.owner.id, record.owner.timestamp, record.toolCallId}
		if _, dup := seen[key]; item == nil || dup {
			continue
		}
		seen[key] = struct{}{}
		receiptID := item.entryId
		if id, ok := entryID(item.entry); ok {
			receiptID = id
		}
		for _, file := range record.files {
			kind := "observed-write"
			if file.kind == "read" {
				kind = "read"
			} else if file.kind == "delete" || file.kind == "clear" {
				kind = "clear"
			}
			owner := record.owner
			out = append(out, noteTouch{
				notePath:   file.path,
				absPath:    filepath.Join(scopeDirectory(file), file.path),
				kind:       kind,
				entryId:    item.entryId,
				receiptId:  receiptID,
				inRetained: false,
				owner:      &owner,
			})
		}
	}
	return out
}

// touchKey: note-evidence.ts:106. `??` falls back only for undefined, which is "" here.
func touchKey(touch noteTouch) [2]string {
	first := touch.receiptId
	if first == "" {
		first = touch.entryId
	}
	second := touch.absPath
	if second == "" {
		second = touch.notePath
	}
	return [2]string{first, second}
}

// sessionNoteTouches: note-evidence.ts:109.
func sessionNoteTouches(evidence noteEvidence) noteEvidenceResult {
	visible := projectedNotes(evidence)
	var directories []string
	seenDir := map[string]struct{}{}
	add := func(d string) {
		if _, dup := seenDir[d]; !dup {
			seenDir[d] = struct{}{}
			directories = append(directories, d)
		}
	}
	for _, binding := range bindingsOf(evidence.allEntries) {
		add(scopeDirectory(binding))
	}
	for _, d := range evidence.directories {
		add(d)
	}
	legacy, defaultDir := collectTouches(visible, directories)
	recorded := recordedTouches(evidence, visible)
	recordedByReceipt := make(map[[2]string]noteTouch, len(recorded))
	for _, touch := range recorded {
		recordedByReceipt[touchKey(touch)] = touch
	}
	covered := map[[2]string]struct{}{}
	touches := make([]noteTouch, 0, len(legacy)+len(recorded))
	for _, touch := range legacy {
		key := touchKey(touch)
		covered[key] = struct{}{}
		touch.owner = recordedByReceipt[key].owner
		touches = append(touches, touch)
	}
	for _, touch := range recorded {
		if _, ok := covered[touchKey(touch)]; !ok {
			touches = append(touches, touch)
		}
	}
	order := make(map[string]int, len(visible))
	visibleIds := make(map[string]struct{}, len(visible))
	for i, item := range visible {
		id, ok := entryID(item.entry)
		if !ok {
			id = item.entryId
		}
		order[id] = i
		visibleIds[idOrEmpty(item.entry)] = struct{}{}
	}
	rank := func(t noteTouch) int {
		id := t.receiptId
		if id == "" {
			id = t.entryId
		}
		return order[id]
	}
	sort.SliceStable(touches, func(i, j int) bool { return rank(touches[i]) < rank(touches[j]) })
	return noteEvidenceResult{touches: touches, defaultDir: defaultDir, visibleIds: visibleIds}
}

func idOrEmpty(entry rec) string {
	id, _ := entryID(entry)
	return id
}

// scopeRecoveryTouches: note-evidence.ts:145. Inherited history describes source bytes, not
// necessarily the bytes snapshotted at fork time.
func scopeRecoveryTouches(touches []noteTouch, recovery *notesRecovery) ([]noteTouch, error) {
	if recovery == nil || recovery.binding.kind == "project" {
		return touches, nil
	}
	owned := ownedPaths(recovery.binding, recovery.entries)
	scopes := []noteScope{recovery.binding.scopeOf()}
	for _, binding := range bindingsOf(recovery.entries) {
		scopes = append(scopes, binding.scopeOf())
	}
	if recovery.binding.source != nil {
		scopes = append(scopes, *recovery.binding.source)
	}
	index := newScopeIndex(scopes)
	var err error
	out := make([]noteTouch, len(touches))
	for i, touch := range touches {
		var address noteAddress
		var found bool
		if touch.absPath != "" {
			if address, found, err = index.addressOf(touch.absPath); err != nil {
				return nil, err
			}
		}
		if found && sameScope(address, recovery.binding) {
			out[i] = touch
			continue
		}
		currentOwner := touch.owner != nil && sameOwner(*touch.owner, recovery.binding.owner)
		if _, ok := owned[touch.notePath]; ok && !currentOwner {
			path, err := notePath(recovery.binding, touch.notePath)
			if err != nil {
				return nil, err
			}
			touch.absPath = path
			if touch.kind != "read" && touch.kind != "clear" {
				touch.kind = "observed-write"
			}
			touch.inRetained = false
			out[i] = touch
			continue
		}
		if !found {
			out[i] = touch
			continue
		}
		touch.kind = "read"
		touch.referenceScope = scopeDirectory(address)
		touch.inRetained = false
		out[i] = touch
	}
	return out, nil
}
