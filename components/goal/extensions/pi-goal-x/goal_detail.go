package pi_goal_x

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"math"
	"strings"
	"unicode/utf16"
)

// ported from goal-detail.ts, including its shared page cache (detailCache)

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

// goalDetailCacheEntries and goalDetailCacheChars bound the shared cache like the original (16 entries, 16,000,000 chars).
const (
	goalDetailCacheEntries = 16
	goalDetailCacheChars   = 16_000_000
)

// goalDetailInputs is the original's inputs array: [goal.id, section, task_id, ...section inputs], compared with ===.
// objective: v1 = objective, v2 = verificationContract; tasks: v1 = the task index (identity), v2 = currentTaskId;
// history: v1 = the ledger revision (identity), v2 unused.
type goalDetailInputs struct {
	id, taskId, v1, v2 any
	section            string
}

// goalDetailCompiled is DetailSource plus the source's UTF-16 units, which every page slices.
type goalDetailCompiled struct {
	source, key string
	units       []uint16
}

type goalDetailCacheEntry struct {
	inputs goalDetailInputs
	result *goalDetailCompiled
}

var (
	goalDetailCache      []goalDetailCacheEntry
	goalDetailCachedUnit int
)

// goalDetailEqual is JavaScript's === on the values a goal record holds; values it cannot compare cheaply never match.
func goalDetailEqual(a, b any) bool {
	_, aList := a.([]any)
	_, bList := b.([]any)
	return !aList && !bList && a == b
}

func (a goalDetailInputs) equal(b goalDetailInputs) bool {
	return a.section == b.section && goalDetailEqual(a.id, b.id) && goalDetailEqual(a.taskId, b.taskId) &&
		goalDetailEqual(a.v1, b.v1) && goalDetailEqual(a.v2, b.v2)
}

// goalDetailCompile is compiledSource: the cached compilation of a section, or nil when the task id is unknown.
func goalDetailCompile(goal *jsObject, q goalDetailQuery, events []*jsObject, revision *ledgerRevision) *goalDetailCompiled {
	var index *goalTaskIndex
	in := goalDetailInputs{id: taskFieldValue(goal, "id"), section: q.section, taskId: undef{}}
	if q.taskId != nil {
		in.taskId = *q.taskId
	}
	switch q.section {
	case "objective":
		in.v1, in.v2 = taskFieldValue(goal, "objective"), taskFieldValue(goal, "verificationContract")
	case "tasks":
		var tasks []any
		if tl := goal.obj("taskList"); tl != nil {
			tasks, _ = tl.vals["tasks"].([]any)
		}
		index = taskIndex(tasks)
		in.v1, in.v2 = index, taskFieldValue(goal, "currentTaskId")
	default:
		if revision != nil {
			in.v1 = revision
		}
	}
	// Arbitrary caller-owned histories remain content checked; only the ledger can supply a generation.
	cacheable := q.section != "history" || revision != nil
	if cacheable {
		for i := len(goalDetailCache) - 1; i >= 0; i-- {
			if goalDetailCache[i].inputs.equal(in) {
				return goalDetailCache[i].result
			}
		}
	}
	source, key, ok := goalDetailSource(goal, q, events, index)
	if !ok {
		return nil
	}
	units := utf16.Encode([]rune(source))
	res := &goalDetailCompiled{source: source, key: key, units: units}
	// Full data remains available even when it exceeds the bounded shared page cache.
	if cacheable && len(units) <= goalDetailCacheChars {
		for len(goalDetailCache) >= goalDetailCacheEntries || goalDetailCachedUnit+len(units) > goalDetailCacheChars {
			goalDetailCachedUnit -= len(goalDetailCache[0].result.units)
			goalDetailCache = goalDetailCache[1:]
		}
		goalDetailCache = append(goalDetailCache, goalDetailCacheEntry{in, res})
		goalDetailCachedUnit += len(units)
	}
	return res
}

// goalDetailSource compiles the section text and its key; ok=false when the task id is unknown. index is the task index for section tasks.
func goalDetailSource(goal *jsObject, q goalDetailQuery, events []*jsObject, index *goalTaskIndex) (source, key string, ok bool) {
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
		rows := index.ordered
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
func goalDetailPage(goal *jsObject, q goalDetailQuery, events []*jsObject, historyRevision *ledgerRevision) goalDetailResult {
	if q.taskId != nil && q.section != "tasks" {
		return goalDetailResult{text: "task_id requires section=tasks."}
	}
	compiled := goalDetailCompile(goal, q, events, historyRevision)
	if compiled == nil {
		return goalDetailResult{text: "Task \"" + *q.taskId + "\" not found."}
	}
	key, units := compiled.key, compiled.units
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
