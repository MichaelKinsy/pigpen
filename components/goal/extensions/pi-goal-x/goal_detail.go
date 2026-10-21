package pi_goal_x

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"math"
	"strings"
	"unicode/utf16"
)

// ported from goal-detail.ts (the shared page cache is omitted: it only reuses pure results)

const goalDetailPageChars = 4000

// goalDetailQuery is GoalDetailQuery; taskId nil = absent, cursor "" = absent.
type goalDetailQuery struct {
	section string // "objective" | "tasks" | "history"
	taskId  *string
	cursor  string
}

// goalDetailResult is GoalDetailPage; hasNext reports nextCursor.
type goalDetailResult struct {
	ok         bool
	text       string
	content    string
	nextCursor string
	hasNext    bool
	totalChars int
}

// goalDetailSource compiles the section text and its key; ok=false when the task id is unknown.
func goalDetailSource(goal *jsObject, q goalDetailQuery, events []*jsObject) (source, key string, ok bool) {
	switch q.section {
	case "objective":
		source = gstr(goal, "objective")
		if vc, _ := goal.str("verificationContract"); vc != "" {
			source += "\n\nVerification contract:\n" + vc
		}
	case "history":
		var parts []string
		for _, e := range events {
			if id, has := e.str("goalId"); has && id == gstr(goal, "id") {
				parts = append(parts, marshalJSON(e, ""))
			}
		}
		source = strings.Join(parts, "\n")
	default:
		var tasks []any
		if tl := goal.obj("taskList"); tl != nil {
			tasks, _ = tl.vals["tasks"].([]any)
		}
		rows := taskIndex(tasks).ordered
		if q.taskId != nil && *q.taskId != "" {
			var sel *goalTaskRow
			for i := range rows {
				if gstr(rows[i].task, "id") == *q.taskId {
					sel = &rows[i]
					break
				}
			}
			if sel == nil {
				return "", "", false
			}
			rows = []goalTaskRow{*sel}
		}
		var lines []string
		for _, r := range rows {
			o := r.task.clone()
			o.set("subtasks", undef{})
			if r.parentId != "" {
				o.set("parent_id", r.parentId)
			} else {
				o.set("parent_id", undef{})
			}
			if gstr(r.task, "id") == gstr(goal, "currentTaskId") {
				o.set("current", true)
			}
			lines = append(lines, marshalJSON(o, ""))
		}
		source = strings.Join(lines, "\n")
	}
	tid := any(undef{})
	if q.taskId != nil {
		tid = *q.taskId
	}
	arr := []any{gstr(goal, "id"), q.section, tid, source}
	sum := sha256.Sum256([]byte(marshalJSON(arr, "")))
	return source, hex.EncodeToString(sum[:]), true
}

// goalDetailPage returns a stable, lossless page of detail text. Offsets are UTF-16 units like the original.
func goalDetailPage(goal *jsObject, q goalDetailQuery, events []*jsObject) goalDetailResult {
	if q.taskId != nil && q.section != "tasks" {
		return goalDetailResult{text: "task_id requires section=tasks."}
	}
	source, key, ok := goalDetailSource(goal, q, events)
	if !ok {
		return goalDetailResult{text: "Task \"" + *q.taskId + "\" not found."}
	}
	units := utf16.Encode([]rune(source))
	offset := 0
	if q.cursor != "" {
		o, valid := goalDetailParseCursor(q.cursor, key, len(units))
		if !valid {
			return goalDetailResult{text: "Invalid or stale cursor: goal details changed or the section/task differs. Restart this section without cursor."}
		}
		offset = o
	}
	end := offset + goalDetailPageChars
	if end > len(units) {
		end = len(units)
	}
	if end < len(units) && units[end-1] >= 0xD800 && units[end-1] <= 0xDBFF {
		end--
	}
	content := string(utf16.Decode(units[offset:end]))
	res := goalDetailResult{ok: true, content: content, totalChars: len(units)}
	tail := "\nEnd of section."
	if end < len(units) {
		cur := marshalJSON(goalDetailCursorObject(key, end), "")
		res.nextCursor = base64.RawURLEncoding.EncodeToString([]byte(cur))
		res.hasNext = true
		tail = "\nMore content: repeat this section/task with cursor=\"" + res.nextCursor + "\"."
	}
	res.text = q.section + " for " + gstr(goal, "id") + " (" + jsNumber(float64(offset)) + "–" + jsNumber(float64(end)) + "/" + jsNumber(float64(len(units))) + " chars)\n" + content + tail
	return res
}

func goalDetailCursorObject(key string, offset int) *jsObject {
	o := newObject()
	o.set("v", 1.0)
	o.set("key", key)
	o.set("offset", float64(offset))
	return o
}

func goalDetailParseCursor(cursor, key string, sourceLen int) (int, bool) {
	if utf16Len(cursor) > 256 {
		return 0, false
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(cursor, "="))
	if err != nil {
		return 0, false
	}
	v, err := parseJSON(raw)
	if err != nil {
		return 0, false
	}
	o, _ := v.(*jsObject)
	if o == nil {
		return 0, false
	}
	ver, _ := o.num("v")
	k, _ := o.str("key")
	off, hasOff := o.num("offset")
	if !o.has("v") || ver != 1 || k != key || !hasOff || !isSafeInteger(off) || off < 0 || off > float64(sourceLen) || math.IsNaN(off) {
		return 0, false
	}
	return int(off), true
}
