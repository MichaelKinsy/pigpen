package posthorse

import "sort"

// Port of src/recovery-notes.ts.

const (
	maxWrittenNotes = 4 // recovery-notes.ts:5
	maxReadNotes    = 3 // recovery-notes.ts:6
)

// retainedSpan is RetainedSpan (recovery-notes.ts:8).
type retainedSpan struct {
	firstId string
	lastId  string
}

// contentProof is ContentProof (recovery-notes.ts:9). kind is "full",
// "partial", "compacted" or "effect-only"; updatedAt "" = undefined.
type contentProof struct {
	kind      string
	entryId   string
	updatedAt string
}

// trackedNote is TrackedNote (recovery-notes.ts:14). kind is "read" or "written".
type trackedNote struct {
	notePath       string
	absPath        string
	referenceScope string
	entryId        string
	order          int
	kind           string
	inRetained     bool         // kind "read"
	proof          contentProof // kind "written"
}

// applyReadTouch: recovery-notes.ts:24.
func applyReadTouch(previous *trackedNote, touch noteTouch, order int) trackedNote {
	target := noteTarget{notePath: touch.notePath, absPath: touch.absPath, referenceScope: touch.referenceScope}
	if previous != nil {
		if target.absPath == "" {
			target.absPath = previous.absPath
		}
		if target.referenceScope == "" {
			target.referenceScope = previous.referenceScope
		}
	}
	if previous != nil && previous.kind == "written" {
		next := *previous
		next.notePath, next.absPath, next.referenceScope = target.notePath, target.absPath, target.referenceScope
		return next
	}
	return trackedNote{
		notePath: target.notePath, absPath: target.absPath, referenceScope: target.referenceScope,
		kind: "read", entryId: touch.entryId, inRetained: touch.inRetained, order: order,
	}
}

// retainedEdit: recovery-notes.ts:38.
func retainedEdit(previous *trackedNote, entryId string) contentProof {
	if previous != nil && previous.kind == "written" && previous.proof.kind == "full" {
		proof := previous.proof
		proof.updatedAt = entryId
		return proof
	}
	return contentProof{kind: "partial", entryId: entryId}
}

// applyTouch: recovery-notes.ts:43. ok=false means the note is dropped ("clear").
func applyTouch(previous *trackedNote, touch noteTouch, order int) (trackedNote, bool) {
	var proof contentProof
	switch touch.kind {
	case "clear":
		return trackedNote{}, false
	case "read":
		return applyReadTouch(previous, touch, order), true
	case "full-write":
		if touch.inRetained {
			proof = contentProof{kind: "full", entryId: touch.entryId}
		} else {
			proof = contentProof{kind: "compacted"}
		}
	case "partial-write":
		if touch.inRetained {
			proof = retainedEdit(previous, touch.entryId)
		} else {
			proof = contentProof{kind: "compacted"}
		}
	case "observed-write":
		proof = contentProof{kind: "effect-only"}
	}
	absPath := touch.absPath
	if absPath == "" && previous != nil {
		absPath = previous.absPath
	}
	return trackedNote{
		notePath: touch.notePath, absPath: absPath, referenceScope: touch.referenceScope,
		kind: "written", entryId: touch.entryId, proof: proof, order: order,
	}, true
}

// formatNoteLabel: recovery-notes.ts:78.
func formatNoteLabel(note *trackedNote) string {
	if note.referenceScope != "" {
		return note.notePath + " (read-only reference; notes read {\"scope\":" + safeJsonStringify(note.referenceScope) +
			",\"path\":" + safeJsonStringify(note.notePath) + "})"
	}
	if note.absPath != "" && note.absPath != note.notePath {
		return note.notePath + " (" + note.absPath + ")"
	}
	return note.notePath
}

// formatWrittenNoteLine: recovery-notes.ts:86.
func formatWrittenNoteLine(note *trackedNote, span *retainedSpan) string {
	label := formatNoteLabel(note)
	proof := note.proof
	if proof.kind == "effect-only" {
		return "- Active note (written): " + label + " [verified effect at entry " + note.entryId + "; full content is not retained — read this note now]"
	}
	if span == nil {
		return "- Active note (written): " + label + " [compacted at entry " + note.entryId + " — read this note now]"
	}
	switch proof.kind {
	case "full":
		updated := ""
		if proof.updatedAt != "" {
			updated = ", updated at entry " + proof.updatedAt
		}
		return "- Active note (written): " + label + " [latest full write is in the retained tail above at entry " + proof.entryId + updated + "]"
	case "partial":
		return "- Active note (written): " + label + " [only partial edits are in the retained tail above at entry " + proof.entryId + " — read full note now]"
	default: // "compacted"
		return "- Active note (written): " + label + " [compacted before entry " + span.firstId + " — read this note now]"
	}
}

// formatReadNoteLine: recovery-notes.ts:107.
func formatReadNoteLine(note *trackedNote, span *retainedSpan) string {
	label := formatNoteLabel(note)
	if span != nil && note.inRetained {
		return "- Referenced note (read): " + label + " [read in the retained tail above at entry " + note.entryId + "]"
	}
	if span != nil {
		return "- Referenced note (read): " + label + " [compacted before entry " + span.firstId + "]"
	}
	return "- Referenced note (read): " + label + " [compacted at entry " + note.entryId + "]"
}

// formatNotesAction: recovery-notes.ts:116.
func formatNotesAction(primaryWritten *trackedNote, span *retainedSpan) string {
	if primaryWritten == nil {
		return "Read the referenced note again if its context is still needed, and keep one current-state note for this task."
	}
	if span != nil && primaryWritten.proof.kind == "full" {
		return "Your latest full write to " + primaryWritten.notePath + " is already visible above at entry " + primaryWritten.proof.entryId +
			"; do not rewrite it unless new findings after entry " + span.lastId + " require it."
	}
	return "Read " + primaryWritten.notePath + " (notes read or read on its path) before continuing to restore the saved goal, progress, decisions, and next steps, then keep that note current as work progresses."
}

// trackedNotes: recovery-notes.ts:128. Newest touch first.
func trackedNotes(touches []noteTouch, defaultDir string) []trackedNote {
	tracked := make(map[string]trackedNote)
	for order, touch := range touches {
		if touch.absPath == "" && defaultDir != "" {
			touch.absPath = defaultDir + "/" + touch.notePath
		}
		scope := touch.referenceScope
		if scope == "" {
			scope = touch.absPath
		}
		key := scope + "\x00" + touch.notePath
		var previous *trackedNote
		if p, ok := tracked[key]; ok {
			previous = &p
		}
		if next, ok := applyTouch(previous, touch, order); ok {
			tracked[key] = next
		} else {
			delete(tracked, key)
		}
	}
	out := make([]trackedNote, 0, len(tracked))
	for _, note := range tracked {
		out = append(out, note)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].order > out[j].order })
	return out
}

// sessionNotesOptions are the options of renderSessionNotes (recovery-notes.ts:152).
type sessionNotesOptions struct {
	retained map[string]struct{}
	span     *retainedSpan
	maxChars int
	notes    *notesRecovery
}

// renderSessionNotes: recovery-notes.ts:148.
func renderSessionNotes(chainEntries, allEntries []rec, projected []projectedEntry, options sessionNotesOptions) (string, bool, error) {
	all := allEntries
	if options.notes != nil {
		all = options.notes.entries
	}
	collected := sessionNoteTouches(noteEvidence{
		branch:     chainEntries,
		allEntries: all,
		projected:  projected,
		retained:   options.retained,
	})
	touches, err := scopeRecoveryTouches(collected.touches, options.notes)
	if err != nil {
		return "", false, err
	}
	notes := trackedNotes(touches, collected.defaultDir)
	if len(notes) == 0 {
		return "", false, nil
	}
	var lines []string
	var primary *trackedNote
	written, read := 0, 0
	for i := range notes {
		if notes[i].kind == "written" && written < maxWrittenNotes {
			if written == 0 {
				primary = &notes[i]
			}
			lines = append(lines, formatWrittenNoteLine(&notes[i], options.span))
			written++
		}
	}
	for i := range notes {
		if notes[i].kind == "read" && read < maxReadNotes {
			lines = append(lines, formatReadNoteLine(&notes[i], options.span))
			read++
		}
	}
	const header = "Task notes in this session:"
	action := formatNotesAction(primary, options.span)
	selected := make([]string, 0, len(lines))
	// [header, ...selected, line, action].join("\n").length, kept incrementally in UTF-16 units.
	total := u16len(header) + 1 + u16len(action)
	for _, line := range lines {
		if total+1+u16len(line) <= options.maxChars {
			selected = append(selected, line)
			total += 1 + u16len(line)
		}
	}
	if len(selected) == 0 {
		return "", false, nil
	}
	out := header
	for _, line := range selected {
		out += "\n" + line
	}
	return out + "\n" + action, true, nil
}
