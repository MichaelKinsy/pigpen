package warden

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// Open loops (src/loops.ts): things the agent promised to do later in this session ("after CI passes, bump the
// version"). The agent adds and closes them with the warden_loops tool; warden keeps them in one small file per
// session under its data folder, so they survive compaction and resume, and shows the open ones in the compaction
// appendix, in one reminder at the end of a run, and in one message on resume. Code only, no judge request. A loop
// never reaches another session or another project: the file is keyed by both.

const (
	loopChars = 160
	// loopsShown: open loops shown in the appendix, the reminder, and the resume message.
	loopsShown = 8
	loopsChars = 600
	// maxOpenLoops: a session with this many open loops is tracking tasks, not promises; one more is refused until some close.
	maxOpenLoops = 30
)

// loopActions is LOOP_ACTIONS, in the order the tool schema lists them.
var loopActions = []string{"add", "done", "drop", "list"}

// Loop is one promise. Fields are in the order the original writes them (id, text, when, status, at, reason, closedAt).
type Loop struct {
	ID int `json:"id"`
	// Text is redacted, at most loopChars characters.
	Text string `json:"text"`
	// When is the condition that makes it due, such as "after CI passes"; redacted.
	When   string `json:"when,omitempty"`
	Status string `json:"status"` // open | done | dropped
	At     int64  `json:"at"`
	// Reason is why a dropped loop was dropped; redacted.
	Reason   string `json:"reason,omitempty"`
	ClosedAt *int64 `json:"closedAt,omitempty"`
}

// LoopStore is one session's file.
type LoopStore struct {
	Next  int    `json:"next"`
	Loops []Loop `json:"loops"`
}

func emptyLoopStore() LoopStore { return LoopStore{Next: 1, Loops: []Loop{}} }

var reUnsafeSessionChar = regexp.MustCompile(`[^\w.-]`)

// loopsPath is <data folder>/loops/<first 12 hex of sha256(cwd)>/<session id>.json: the working directory's hash keeps
// another project's session with the same id apart, and the id keeps another session of this project apart.
func loopsPath(cwd, session, dataDir string) string {
	sum := sha256.Sum256([]byte(cwd))
	return filepath.Join(dataDir, "loops", hex.EncodeToString(sum[:])[:12], reUnsafeSessionChar.ReplaceAllString(session, "_")+".json")
}

// readLoops: a missing file is an empty store; an unreadable or malformed one is an error, so a write never replaces
// loops it could not read.
func readLoops(path string) (LoopStore, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return emptyLoopStore(), nil
		}
		return LoopStore{}, err
	}
	var raw any
	if err := json.Unmarshal(data, &raw); err != nil {
		return LoopStore{}, err
	}
	obj, ok := raw.(map[string]any)
	list, isList := obj["loops"].([]any)
	if !ok || !isList {
		return LoopStore{}, errors.New("the loops file has no loops list")
	}
	loops := []Loop{}
	highest := 0
	for _, item := range list {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		id, okID := m["id"].(float64)
		text, okText := m["text"].(string)
		at, okAt := m["at"].(float64)
		status, _ := m["status"].(string)
		if !okID || !okText || !okAt || (status != "open" && status != "done" && status != "dropped") {
			continue
		}
		loop := Loop{ID: int(id), Text: text, Status: status, At: int64(at)}
		loop.When, _ = m["when"].(string)
		loop.Reason, _ = m["reason"].(string)
		if closed, ok := m["closedAt"].(float64); ok {
			c := int64(closed)
			loop.ClosedAt = &c
		}
		if loop.ID > highest {
			highest = loop.ID
		}
		loops = append(loops, loop)
	}
	next := highest + 1
	if n, ok := obj["next"].(float64); ok {
		next = int(n)
	}
	return LoopStore{Next: next, Loops: loops}, nil
}

// writeLoops writes the whole file to a temporary file and renames it, so a crash never leaves half a file.
func writeLoops(path string, store LoopStore) error {
	if store.Loops == nil {
		store.Loops = []Loop{}
	}
	data, err := prettyJSON(store)
	if err != nil {
		return err
	}
	return WriteFileAtomic(path, data)
}

// loopReply is the result of one change: the reply for the agent, and the new store when it changed.
type loopReply struct {
	Reply string
	Store *LoopStore
}

// updateLoops reads, applies, and writes one change under the file's queue, so two warden_loops calls in one process
// run in turn and cannot lose each other's update.
func updateLoops(path string, change func(LoopStore) loopReply) (string, error) {
	return Serialize(path, func() (string, error) {
		store, err := readLoops(path)
		if err != nil {
			return "", err
		}
		result := change(store)
		if result.Store != nil {
			if err := writeLoops(path, *result.Store); err != nil {
				return "", err
			}
		}
		return result.Reply, nil
	})
}

func openLoops(store LoopStore) []Loop {
	var open []Loop
	for _, l := range store.Loops {
		if l.Status == "open" {
			open = append(open, l)
		}
	}
	return open
}

// oneLine collapses white space and redacts.
func oneLine(text string) string { return Redact(jsTrim(jsCollapseSpace(text))) }

func loopLine(l Loop) string {
	line := "- #" + itoaInt(l.ID) + " " + l.Text
	if l.When != "" {
		line += " (when: " + l.When + ")"
	}
	return line
}

// formatOpenLoops lists the open loops, oldest first, at most loopsShown lines and loopsChars characters; whole
// lines only, with a count of the ones left out. Empty when none is open.
func formatOpenLoops(loops []Loop) string {
	var open []Loop
	for _, l := range loops {
		if l.Status == "open" {
			open = append(open, l)
		}
	}
	var lines []string
	length := 0
	for i, l := range open {
		if i >= loopsShown {
			break
		}
		line := loopLine(l)
		// Room for the count line of the ones left out.
		if length+utf16Len(line)+1 > loopsChars-40 {
			break
		}
		lines = append(lines, line)
		length += utf16Len(line) + 1
	}
	if len(lines) < len(open) {
		lines = append(lines, "- … and "+itoaInt(len(open)-len(lines))+" more (warden_loops list)")
	}
	return strings.Join(lines, "\n")
}

// loopsFingerprint is the open list as it stands: a reminder already sent for the same list is not sent again.
func loopsFingerprint(loops []Loop) string {
	var parts []string
	for _, l := range loops {
		if l.Status == "open" {
			parts = append(parts, itoaInt(l.ID)+":"+l.Text+":"+l.When)
		}
	}
	return strings.Join(parts, "\n")
}

// loopInput is one warden_loops call as the model sent it; fields are the decoded JSON values.
type loopInput map[string]any

func (in loopInput) str(key string) (string, bool) {
	s, ok := in[key].(string)
	return s, ok
}

// loopID is `typeof id === "number" ? id : typeof id === "string" ? Number(id.replace(/^#/, "")) : NaN`.
func (in loopInput) loopID() (float64, bool) {
	switch v := in["id"].(type) {
	case float64:
		return v, true
	case string:
		return jsNumber(strings.TrimPrefix(v, "#"))
	}
	return 0, false
}

// jsNumber is Number(text) for the spellings an id can have: trimmed, decimal, and the empty string is 0.
func jsNumber(text string) (float64, bool) {
	t := jsTrim(text)
	if t == "" {
		return 0, true
	}
	if f, err := strconv.ParseFloat(t, 64); err == nil && !strings.ContainsAny(t, "_xXpP") && !strings.EqualFold(strings.TrimLeft(t, "+-"), "inf") && !strings.EqualFold(strings.TrimLeft(t, "+-"), "infinity") && !strings.EqualFold(strings.TrimLeft(t, "+-"), "nan") {
		return f, true
	}
	if strings.HasPrefix(t, "0x") || strings.HasPrefix(t, "0X") {
		if n, err := strconv.ParseUint(t[2:], 16, 64); err == nil {
			return float64(n), true
		}
	}
	if strings.HasPrefix(t, "0b") || strings.HasPrefix(t, "0B") {
		if n, err := strconv.ParseUint(t[2:], 2, 64); err == nil {
			return float64(n), true
		}
	}
	if strings.HasPrefix(t, "0o") || strings.HasPrefix(t, "0O") {
		if n, err := strconv.ParseUint(t[2:], 8, 64); err == nil {
			return float64(n), true
		}
	}
	return 0, false
}

// formatLoopNumber prints a JavaScript number the way template literals do for the ids a reply can show.
func formatLoopNumber(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }

// applyLoopAction applies one warden_loops call. Store is nil when nothing changes.
func applyLoopAction(store LoopStore, in loopInput, now int64) loopReply {
	action, _ := in.str("action")
	switch action {
	case "list":
		open := openLoops(store)
		closed := len(store.Loops) - len(open)
		suffix := ""
		if closed > 0 {
			suffix = " (" + itoaInt(closed) + " closed)"
		}
		if len(open) > 0 {
			if closed > 0 {
				suffix = "\n(" + itoaInt(closed) + " closed)"
			}
			return loopReply{Reply: "Open loops:\n" + formatOpenLoops(open) + suffix}
		}
		return loopReply{Reply: "No open loops" + suffix + "."}
	case "add":
		var text, when string
		if s, ok := in.str("text"); ok {
			text = oneLine(s)
		}
		if s, ok := in.str("when"); ok {
			when = oneLine(s)
		}
		if text == "" {
			return loopReply{Reply: "not added: give the loop's text"}
		}
		if utf16Len(text) > loopChars || utf16Len(when) > loopChars {
			return loopReply{Reply: "not added: the text and the condition are at most " + itoaInt(loopChars) + " characters each"}
		}
		if len(openLoops(store)) >= maxOpenLoops {
			return loopReply{Reply: "not added: " + itoaInt(maxOpenLoops) + " loops are open; close the finished ones with done or drop first"}
		}
		loop := Loop{ID: store.Next, Text: text, When: when, Status: "open", At: now}
		reply := "added #" + itoaInt(loop.ID) + ": " + text
		if when != "" {
			reply += " (when: " + when + ")"
		}
		loops := append(append([]Loop(nil), store.Loops...), loop)
		return loopReply{Reply: reply, Store: &LoopStore{Next: store.Next + 1, Loops: loops}}
	case "done", "drop":
		id, idOK := in.loopID()
		var found *Loop
		for i := range store.Loops {
			if idOK && float64(store.Loops[i].ID) == id {
				found = &store.Loops[i]
				break
			}
		}
		if found == nil {
			shown := "?"
			if idOK {
				shown = formatLoopNumber(id)
			}
			return loopReply{Reply: "no loop #" + shown + " in this session"}
		}
		if found.Status != "open" {
			return loopReply{Reply: "#" + itoaInt(found.ID) + " is already " + found.Status}
		}
		var reason string
		if s, ok := in.str("reason"); ok {
			reason = utf16Slice(oneLine(s), 0, loopChars)
		}
		if action == "drop" && reason == "" {
			return loopReply{Reply: "not dropped: say why #" + itoaInt(found.ID) + " is no longer needed"}
		}
		closed := *found
		closed.Status = "dropped"
		verb := "dropped"
		if action == "done" {
			closed.Status, verb = "done", "done"
		}
		if reason != "" && action == "drop" {
			closed.Reason = reason
		}
		at := now
		closed.ClosedAt = &at
		loops := make([]Loop, len(store.Loops))
		for i, l := range store.Loops {
			loops[i] = l
			if l.ID == found.ID {
				loops[i] = closed
			}
		}
		return loopReply{Reply: verb + " #" + itoaInt(found.ID) + ": " + found.Text, Store: &LoopStore{Next: store.Next, Loops: loops}}
	}
	return loopReply{Reply: "unknown action; use one of " + strings.Join(loopActions, ", ")}
}

// formatLoopsForUser is `/warden loops`: every loop of the session, open first, with how each closed.
func formatLoopsForUser(store LoopStore) string {
	if len(store.Loops) == 0 {
		return "No loops in this session. The agent adds one with warden_loops when it promises to do something later."
	}
	open := openLoops(store)
	var closed []Loop
	for _, l := range store.Loops {
		if l.Status != "open" {
			closed = append(closed, l)
		}
	}
	var lines []string
	if len(open) > 0 {
		lines = append(lines, "Open loops ("+itoaInt(len(open))+"):")
	} else {
		lines = append(lines, "No open loops.")
	}
	for _, l := range open {
		lines = append(lines, loopLine(l))
	}
	if len(closed) > 0 {
		lines = append(lines, "Closed ("+itoaInt(len(closed))+"):")
		for _, l := range closed {
			line := loopLine(l) + ": " + l.Status
			if l.Reason != "" {
				line += ", " + l.Reason
			}
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n")
}
