package pi_goal_x

import (
	crand "crypto/rand"
	"fmt"
	"math"
	"math/rand"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// A goal is a *jsObject: the goal file is JSON.stringify of a JavaScript object, so the order of its keys (insertion order, kept
// through the spreads the original uses) is part of the file format. The functions below mirror goal-record.ts and
// goal-scheduler-state.ts.

// Injection points: tests pin the clock, the id and the scheduler generation.
var (
	clockMs = func() int64 { return time.Now().UnixMilli() }
	newUUID = func() string {
		b := make([]byte, 16)
		_, _ = crand.Read(b)
		b[6] = b[6]&0x0f | 0x40
		b[8] = b[8]&0x3f | 0x80
		return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
	}
	randFloat = rand.Float64
)

func nowIso() string { return isoFromMs(clockMs()) }

func isoFromMs(ms int64) string { return time.UnixMilli(ms).UTC().Format("2006-01-02T15:04:05.000Z") }

var unsafeIDChars = regexp.MustCompile(`[^a-zA-Z0-9_-]`)

// safeIdPart is value.replace(/[^a-zA-Z0-9_-]/g, "_").slice(0, 80) || "goal": a replaced character counts once per UTF-16 unit.
func safeIdPart(value string) string {
	var b strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
			b.WriteRune(r)
		case r >= 0x10000:
			b.WriteString("__")
		default:
			b.WriteByte('_')
		}
	}
	s := b.String()
	if len(s) > 80 {
		s = s[:80]
	}
	if s == "" {
		return "goal"
	}
	return s
}

const base36 = "0123456789abcdefghijklmnopqrstuvwxyz"

func toBase36(n int64) string { return strconv.FormatInt(n, 36) }

// newGoalId is `${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 8)}`.
var newGoalId = func() string {
	var b strings.Builder
	f := randFloat()
	for i := 0; i < 6; i++ {
		f *= 36
		d := int(f)
		b.WriteByte(base36[d])
		f -= float64(d)
	}
	return toBase36(clockMs()) + "-" + b.String()
}

func normalizeRelPath(p string) string { return strings.Join(splitSeps(p), "/") }

func splitSeps(p string) []string {
	return regexp.MustCompile(`[\\/]+`).Split(p, -1)
}

func isSafeInteger(f float64) bool { return f == math.Trunc(f) && math.Abs(f) <= 9007199254740991 }

func emptyUsage() *jsObject {
	u := newObject()
	u.set("tokensUsed", 0.0)
	u.set("activeSeconds", 0.0)
	return u
}

func deepClone(v any) any {
	switch x := v.(type) {
	case *jsObject:
		c := newObject()
		for _, k := range x.keys {
			c.set(k, deepClone(x.vals[k]))
		}
		return c
	case []any:
		c := make([]any, len(x))
		for i, e := range x {
			c[i] = deepClone(e)
		}
		return c
	}
	return v
}

func cloneTask(v any) any {
	t, ok := v.(*jsObject)
	if !ok {
		return v
	}
	c := t.clone()
	if subs, ok := t.vals["subtasks"].([]any); ok {
		out := make([]any, len(subs))
		for i, s := range subs {
			out[i] = cloneTask(s)
		}
		c.set("subtasks", out)
	} else {
		c.set("subtasks", undef{})
	}
	return c
}

// cloneGoal is {...goal, ...(scheduler ? {scheduler: structuredClone} : {}), usage: {...usage}, taskList: ...}.
func cloneGoal(g *jsObject) *jsObject {
	c := g.clone()
	if g.has("scheduler") {
		c.set("scheduler", deepClone(g.vals["scheduler"]))
	}
	if u := g.obj("usage"); u != nil {
		c.set("usage", u.clone())
	} else {
		c.set("usage", g.vals["usage"])
	}
	if tl := g.obj("taskList"); tl != nil {
		n := tl.clone()
		var tasks []any
		if ts, ok := tl.vals["tasks"].([]any); ok {
			tasks = make([]any, len(ts))
			for i, t := range ts {
				tasks[i] = cloneTask(t)
			}
		}
		n.set("tasks", tasks)
		c.set("taskList", n)
	} else {
		c.set("taskList", undef{})
	}
	return c
}

// goalFocusDetails builds the persisted session focus entry.
func goalFocusDetails(goalID *string, reason string) *jsObject {
	o := newObject()
	o.set("version", 1.0)
	if goalID != nil && *goalID != "" {
		o.set("focusedGoalId", safeIdPart(*goalID))
	} else {
		o.set("focusedGoalId", nil)
	}
	o.set("reason", reason)
	return o
}

var focusReasons = map[string]bool{"created": true, "selected": true, "unfocused": true, "resumed": true, "completed": true, "cleared": true, "aborted": true, "migrated": true}

type focusEntry struct {
	goalID     *string
	reason     string
	hasStorage bool
	root       string
}

func normalizeGoalFocusEntry(v any) *focusEntry {
	raw, ok := v.(*jsObject)
	if !ok {
		return nil
	}
	if ver, _ := raw.num("version"); ver != 1 || !raw.has("version") {
		return nil
	}
	e := &focusEntry{reason: "selected"}
	if id, ok := raw.str("focusedGoalId"); ok && jsTrim(id) != "" {
		s := safeIdPart(id)
		e.goalID = &s
	}
	if r, ok := raw.str("reason"); ok && focusReasons[r] {
		e.reason = r
	}
	if root, ok := raw.str("storageRoot"); ok {
		e.hasStorage, e.root = true, root
	}
	return e
}

// createGoal builds a fresh goal. Key order: id, objective, status, autoContinue, usage, sisyphus, skipAuditor, revision, createdAt, updatedAt.
func createGoal(objective string, autoContinue, sisyphus bool, skipAuditor bool, nowMs int64) *jsObject {
	ts := isoFromMs(nowMs)
	g := newObject()
	g.set("id", newGoalId())
	g.set("objective", objective)
	g.set("status", "active")
	g.set("autoContinue", autoContinue)
	g.set("usage", emptyUsage())
	g.set("sisyphus", sisyphus)
	if skipAuditor {
		g.set("skipAuditor", true)
	} else {
		g.set("skipAuditor", undef{})
	}
	g.set("revision", 0.0)
	g.set("createdAt", ts)
	g.set("updatedAt", ts)
	return g
}

func normalizeUsage(v any) *jsObject {
	raw, ok := v.(*jsObject)
	if !ok {
		return emptyUsage()
	}
	pick := func(k string) float64 {
		if f, ok := raw.num(k); ok && !math.IsInf(f, 0) && !math.IsNaN(f) {
			return math.Max(0, math.Floor(f))
		}
		return 0
	}
	u := newObject()
	u.set("tokensUsed", pick("tokensUsed"))
	u.set("activeSeconds", pick("activeSeconds"))
	return u
}

func optStr(raw *jsObject, k string) any {
	if s, ok := raw.str(k); ok {
		return s
	}
	return nil
}

func normalizeTaskItem(raw *jsObject) *jsObject {
	id, _ := raw.str("id")
	id = jsTrim(id)
	title, _ := raw.str("title")
	title = jsTrim(title)
	if id == "" || title == "" {
		return nil
	}
	status := "pending"
	if s, _ := raw.str("status"); s == "complete" || s == "skipped" {
		status = s
	}
	var subtasks any
	if arr, ok := raw.vals["subtasks"].([]any); ok {
		subs := []any{}
		for _, item := range arr {
			if o, ok := item.(*jsObject); ok {
				if t := normalizeTaskItem(o); t != nil {
					subs = append(subs, t)
				}
			}
		}
		if len(subs) > 0 {
			subtasks = subs
		}
	}
	t := newObject()
	t.set("id", id)
	t.set("title", title)
	t.set("status", status)
	t.setOpt("completedAt", optStr(raw, "completedAt"))
	t.setOpt("skippedAt", optStr(raw, "skippedAt"))
	t.setOpt("evidence", optStr(raw, "evidence"))
	t.setOpt("skipReason", optStr(raw, "skipReason"))
	t.setOpt("verificationContract", optStr(raw, "verificationContract"))
	if b, _ := raw.flag("lightweightSubtasks"); b {
		t.set("lightweightSubtasks", true)
	} else {
		t.set("lightweightSubtasks", undef{})
	}
	t.setOpt("subtasks", subtasks)
	return t
}

func normalizeTaskList(v any) *jsObject {
	raw, ok := v.(*jsObject)
	if !ok {
		return nil
	}
	arr, ok := raw.vals["tasks"].([]any)
	if !ok {
		return nil
	}
	tasks := []any{}
	for _, item := range arr {
		// The original: `item && typeof item !== "object" || Array.isArray(item) ? undefined : normalizeTaskItem(item)`. A
		// falsy item reaches normalizeTaskItem and yields nothing; a primitive or an array is dropped.
		if o, ok := item.(*jsObject); ok {
			if t := normalizeTaskItem(o); t != nil {
				tasks = append(tasks, t)
			}
		}
	}
	if len(tasks) == 0 {
		return nil
	}
	l := newObject()
	l.set("tasks", tasks)
	b, _ := raw.flag("blockCompletion")
	l.set("blockCompletion", b)
	if s, ok := raw.str("proposedAt"); ok {
		l.set("proposedAt", s)
	} else {
		l.set("proposedAt", nowIso())
	}
	return l
}

func normalizePositiveSafeInteger(v any) any {
	if f, ok := v.(float64); ok && isSafeInteger(f) && f >= 1 {
		return f
	}
	return nil
}

func currentTaskIdIsPending(tasks []any, id string) bool {
	if id == "" || tasks == nil {
		return false
	}
	for _, x := range tasks {
		t, _ := x.(*jsObject)
		if t == nil {
			continue
		}
		if tid, _ := t.str("id"); tid == id {
			s, _ := t.str("status")
			return s == "pending"
		}
		if subs, ok := t.vals["subtasks"].([]any); ok && currentTaskIdIsPending(subs, id) {
			return true
		}
	}
	return false
}

var goalStatuses = map[string]bool{"complete": true, "paused": true, "budget_limited": true, "blocked": true}

// normalizeGoalRecord is goal-record.ts normalizeGoalRecord; nil stands for null. Key order: id, objective, status, autoContinue,
// usage, sisyphus, createdAt, updatedAt, activePath, archivedPath, stopReason, pauseReason, pauseSuggestedAction, skipAuditor,
// revision, tokenBudget, [scheduler], taskList, currentTaskId, verificationContract.
func normalizeGoalRecord(v any) *jsObject {
	raw, ok := v.(*jsObject)
	if !ok {
		return nil
	}
	obj, _ := raw.str("objective")
	obj = jsTrim(obj)
	if obj == "" {
		return nil
	}
	ts := nowIso()
	status := "active"
	if s, _ := raw.str("status"); goalStatuses[s] {
		status = s
	}
	auto := true
	if b, ok := raw.flag("autoContinue"); ok {
		auto = b
	}
	sisyphus, _ := raw.flag("sisyphus")
	taskList := normalizeTaskList(raw.vals["taskList"])
	var currentTask any
	if c, ok := raw.str("currentTaskId"); ok {
		var tasks []any
		if taskList != nil {
			tasks, _ = taskList.vals["tasks"].([]any)
		}
		if currentTaskIdIsPending(tasks, jsTrim(c)) {
			currentTask = jsTrim(c)
		}
	}
	g := newObject()
	if id, ok := raw.str("id"); ok && id != "" {
		g.set("id", safeIdPart(id))
	} else {
		g.set("id", newGoalId())
	}
	g.set("objective", obj)
	g.set("status", status)
	g.set("autoContinue", auto)
	g.set("usage", normalizeUsage(raw.vals["usage"]))
	g.set("sisyphus", sisyphus)
	for _, k := range []string{"createdAt", "updatedAt"} {
		if s, ok := raw.str(k); ok {
			g.set(k, s)
		} else {
			g.set(k, ts)
		}
	}
	g.setOpt("activePath", optStr(raw, "activePath"))
	g.setOpt("archivedPath", optStr(raw, "archivedPath"))
	if s, _ := raw.str("stopReason"); s == "agent" || s == "user" {
		g.set("stopReason", s)
	} else {
		g.set("stopReason", undef{})
	}
	for _, k := range []string{"pauseReason", "pauseSuggestedAction"} {
		if s, ok := raw.str(k); ok && jsTrim(s) != "" {
			g.set(k, s)
		} else {
			g.set(k, undef{})
		}
	}
	if b, _ := raw.flag("skipAuditor"); b {
		g.set("skipAuditor", true)
	} else {
		g.set("skipAuditor", undef{})
	}
	if f, ok := raw.num("revision"); ok && isSafeInteger(f) && f >= 0 {
		g.set("revision", f)
	} else {
		g.set("revision", 0.0)
	}
	g.setOpt("tokenBudget", normalizePositiveSafeInteger(raw.vals["tokenBudget"]))
	if _, present := raw.vals["scheduler"]; present && raw.has("scheduler") {
		g.set("scheduler", normalizeGoalScheduler(raw.vals["scheduler"]))
	}
	if taskList != nil {
		g.set("taskList", taskList)
	} else {
		g.set("taskList", undef{})
	}
	g.setOpt("currentTaskId", currentTask)
	g.setOpt("verificationContract", optStr(raw, "verificationContract"))
	return g
}

// ---- goal-scheduler-state.ts ----

func newGoalScheduler(owner string) *jsObject {
	s := newObject()
	s.set("version", 1.0)
	s.set("owner", owner)
	s.set("generation", newUUID())
	s.set("used", 0.0)
	s.set("phase", "idle")
	s.set("repairUsed", false)
	return s
}

func invalidScheduler() *jsObject {
	s := newGoalScheduler("invalid")
	s.set("phase", "interrupted")
	s.set("used", 9007199254740991.0)
	return s
}

func isNonNegInt(v any) bool {
	f, ok := v.(float64)
	return ok && isSafeInteger(f) && f >= 0
}

func isText(v any) bool {
	s, ok := v.(string)
	return ok && s != "" && utf16Len(s) <= 2000
}

func utf16Len(s string) int {
	n := 0
	for _, r := range s {
		if r >= 0x10000 {
			n += 2
		} else {
			n++
		}
	}
	return n
}

func inList(s string, list ...string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// normalizeGoalScheduler validates a persisted scheduler record; corrupt data becomes an interrupted record with the allowance
// spent, and a decision.nextAction left by older versions is dropped.
func normalizeGoalScheduler(v any) any {
	s, ok := v.(*jsObject)
	if !ok {
		return invalidScheduler()
	}
	phase, _ := s.str("phase")
	ver, _ := s.num("version")
	repair, repairOK := s.flag("repairUsed")
	_ = repair
	if ver != 1 || !isText(s.vals["owner"]) || !isText(s.vals["generation"]) || !isNonNegInt(s.vals["used"]) || !repairOK ||
		!inList(phase, "idle", "ready", "waiting", "claimed", "running", "interrupted") {
		return invalidScheduler()
	}
	dec := s.obj("decision")
	if s.has("decision") && dec != nil {
		kind, _ := dec.str("kind")
		purpose, _ := dec.str("purpose")
		if kind != "wait" && (kind != "ready" || !inList(purpose, "ready", "repair", "kickoff", "recovery")) {
			return invalidScheduler()
		}
	} else if s.has("decision") && !truthy(s.vals["decision"]) {
		// falsy decision: no check
	} else if s.has("decision") {
		return invalidScheduler()
	}
	if w := s.obj("wait"); s.has("wait") && w != nil {
		dl, dlOK := w.num("deadline")
		if !isText(w.vals["id"]) || !isText(w.vals["token"]) || !isText(w.vals["reason"]) || !dlOK || !isSafeInteger(dl) || dl < 0 || dl > 8640000000000000 {
			return invalidScheduler()
		}
		iv, hasIv := w.vals["intervalMs"]
		if w.has("intervalMs") {
			if f, _ := iv.(float64); !isNonNegInt(iv) || f < 1000 || !isNonNegInt(w.vals["remainingChecks"]) || !isNonNegInt(w.vals["nextCheckAt"]) {
				return invalidScheduler()
			}
		} else if (w.has("remainingChecks")) || (w.has("nextCheckAt")) {
			return invalidScheduler()
		}
		_ = hasIv
		if w.has("signalled") {
			if _, ok := w.vals["signalled"].(bool); !ok {
				return invalidScheduler()
			}
		}
	} else if s.has("wait") && truthy(s.vals["wait"]) {
		return invalidScheduler()
	}
	if d := s.obj("dispatch"); s.has("dispatch") && d != nil {
		kind, _ := d.str("kind")
		if !isText(d.vals["id"]) || !isNonNegInt(d.vals["claimedAt"]) || !inList(kind, "ready", "check", "wake", "repair", "kickoff", "recovery") {
			return invalidScheduler()
		}
	} else if s.has("dispatch") && truthy(s.vals["dispatch"]) {
		return invalidScheduler()
	}
	if (phase == "claimed" || phase == "running") && !(s.has("dispatch") && truthy(s.vals["dispatch"])) {
		return invalidScheduler()
	}
	kind := ""
	if dec != nil {
		kind, _ = dec.str("kind")
	}
	if phase == "ready" && kind != "ready" {
		return invalidScheduler()
	}
	if phase == "waiting" && (!(s.has("wait") && truthy(s.vals["wait"])) || kind != "wait") {
		return invalidScheduler()
	}
	n := deepClone(s).(*jsObject)
	if d := n.obj("decision"); d != nil {
		if _, ok := d.vals["nextAction"]; ok {
			delete(d.vals, "nextAction")
			d.keys = removeKey(d.keys, "nextAction")
		}
	}
	return n
}

func removeKey(keys []string, k string) []string {
	out := keys[:0:0]
	for _, x := range keys {
		if x != k {
			out = append(out, x)
		}
	}
	return out
}

// truthy is JavaScript truthiness for a decoded JSON value.
func truthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case float64:
		return x != 0 && !math.IsNaN(x)
	case string:
		return x != ""
	case undef:
		return false
	}
	return true
}

var _ = utf8.RuneError
