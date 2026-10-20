package pi_subagents

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/dop251/goja"
)

var workflowBindingNamespace = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9._-]{0,62})/[1-9][0-9]{0,8}$`)

const workflowRunsAllPermissiveWarning = "runs.all received runs.run(...) launch promise(s) or thenable(s). Those children were already launched individually, so runs.all cannot apply batch fingerprint validation, batch grouping in the fleet view, or collectFailure semantics; a failed child will throw at the runs.all boundary instead of returning as { ok: false }. To get full runs.all semantics, pass config objects: runs.all(items.map((item) => ({ key: item.key, agent: item.agent, task: item.task }))). "

func runsAllKeyAccessError(prop string) string {
	return "Cannot read runs.all result property '" + prop + "'. runs.all resolves to an ordered array, not a key map. Use results[0], array destructuring, or results.map((result) => result.output), not results." + prop + "."
}

// wrapRunsAllResults is wrapRunsAllResults (scripted-workflow.ts:324): an ordered array that refuses keyed reads.
func (s *wfSandbox) wrapRunsAllResults(results *goja.Object) goja.Value {
	proxy := s.vm.NewProxy(results, &goja.ProxyTrapConfig{
		Get: func(target *goja.Object, prop string, receiver goja.Value) goja.Value {
			if prop == "then" || prop == "toJSON" {
				return goja.Undefined()
			}
			if s.realm.call(s.realm.reflectHas, goja.Undefined(), target, s.str(prop)).ToBoolean() || isArrayIndexProperty(prop) {
				return s.realm.call(s.realm.reflectGet, goja.Undefined(), target, s.str(prop), receiver)
			}
			s.throwError(runsAllKeyAccessError(prop))
			return nil
		},
	})
	pv := s.vm.ToValue(proxy)
	s.runsAllTargets[pv.ToObject(s.vm)] = results
	return pv
}

func isArrayIndexProperty(prop string) bool {
	n, ok := isArrayIndexString(prop)
	_ = n
	if !ok {
		return false
	}
	v, err := strconv.ParseUint(prop, 10, 64)
	return err == nil && v < 4294967295
}

// ---- promise helpers on the script realm ----

func (s *wfSandbox) thenP(p goja.Value, onF, onR func(goja.Value) goja.Value) goja.Value {
	var f, r goja.Value = goja.Undefined(), goja.Undefined()
	if onF != nil {
		f = s.native("", 1, func(c goja.FunctionCall) goja.Value { return onF(c.Argument(0)) })
	}
	if onR != nil {
		r = s.native("", 1, func(c goja.FunctionCall) goja.Value { return onR(c.Argument(0)) })
	}
	return s.callFn(s.realm.promiseThen, p, f, r)
}

func (s *wfSandbox) resolved(v goja.Value) goja.Value {
	return s.callFn(s.promiseResolve, s.realm.promiseCtor, v)
}

// ---- lanes (scripted-workflow.ts:311-573) ----

type wfLaneStage struct {
	key          string
	generatedKey string
	resume       goja.Value // undefined or the string "previous"
	params       *goja.Object
}

type wfLane struct {
	key    string
	stages []*wfLaneStage
}

func (s *wfSandbox) validateLaneKey(v goja.Value, owner string) string {
	k, ok := isJSString(v)
	if !ok || !workflowKeyPattern.MatchString(k) {
		s.throwError(owner + " key must be 1-128 characters using letters, numbers, '.', '_' or '-', and start with a letter or number.")
	}
	return k
}

func (s *wfSandbox) validateLaneStageBounds(params *goja.Object, label string) {
	if task, ok := isJSString(params.Get("task")); ok && len(task) > wfMaxLaneTaskBytes {
		s.throwError(label + " task exceeds 1 MiB when UTF-8 encoded.")
	}
	for _, f := range []string{"cwd", "output", "sessionDir"} {
		if v, ok := isJSString(params.Get(f)); ok && len(v) > wfMaxLanePathBytes {
			s.throwError(label + " " + f + " exceeds 32 KiB when UTF-8 encoded.")
		}
	}
}

// restObject is `const { key, resume, ...params } = stage`.
func (s *wfSandbox) restObject(stage *goja.Object, drop ...string) *goja.Object {
	out := s.vm.NewObject()
	skip := map[string]bool{}
	for _, d := range drop {
		skip[d] = true
	}
	for _, k := range s.realm.keysOf(stage) {
		if skip[k] {
			continue
		}
		_ = out.DefineDataProperty(k, stage.Get(k), goja.FLAG_TRUE, goja.FLAG_TRUE, goja.FLAG_TRUE)
	}
	return out
}

func (s *wfSandbox) validateLaneSpecs(specs goja.Value) []*wfLane {
	arr, ok := specs.(*goja.Object)
	if !ok || goja.IsNull(specs) || !s.realm.array(arr) {
		s.throwError("runs.lanes(lanes) requires an array.")
	}
	n := int(arr.Get("length").ToInteger())
	if n == 0 {
		s.throwError("runs.lanes(lanes) requires at least one lane.")
	}
	if n > wfMaxLanes {
		s.throwError("runs.lanes supports at most " + strconv.Itoa(wfMaxLanes) + " lanes.")
	}
	s.checkJSON(arr, "runs.lanes lanes")
	if len(stableRunJSON(s.toGo(arr))) > wfMaxLaneSpecBytes {
		s.throwError("runs.lanes canonical JSON exceeds 64 KiB.")
	}
	generatedKeys := map[string]bool{}
	validationFingerprints := map[string]string{}
	stageCount := 0
	var normalized []*wfLane
	for li := range n {
		laneV := arr.Get(strconv.Itoa(li))
		laneLabel := "runs.lanes lane " + strconv.Itoa(li)
		lane, ok := laneV.(*goja.Object)
		if !ok || goja.IsNull(laneV) || s.realm.array(lane) {
			s.throwError(laneLabel + " must be an object.")
		}
		for _, f := range s.realm.keysOf(lane) {
			if f != "key" && f != "stages" {
				s.throwError(laneLabel + " contains unsupported fields.")
			}
		}
		laneKey := s.validateLaneKey(lane.Get("key"), laneLabel)
		stagesV, ok := lane.Get("stages").(*goja.Object)
		if !ok || goja.IsNull(lane.Get("stages")) || !s.realm.array(stagesV) || stagesV.Get("length").ToInteger() == 0 {
			s.throwError(laneLabel + " stages must contain at least one stage.")
		}
		sn := int(stagesV.Get("length").ToInteger())
		if sn > wfMaxLaneStages {
			s.throwError(laneLabel + " supports at most " + strconv.Itoa(wfMaxLaneStages) + " stages.")
		}
		stageKeys := map[string]bool{}
		l := &wfLane{key: laneKey}
		for si := range sn {
			stageCount++
			if stageCount > wfMaxLaneStageCount {
				s.throwError("runs.lanes supports at most " + strconv.Itoa(wfMaxLaneStageCount) + " total stages.")
			}
			stageV := stagesV.Get(strconv.Itoa(si))
			stageLabel := laneLabel + " stage " + strconv.Itoa(si)
			stage, ok := stageV.(*goja.Object)
			if !ok || goja.IsNull(stageV) || s.realm.array(stage) {
				s.throwError(stageLabel + " must be an object.")
			}
			stageKey := s.validateLaneKey(stage.Get("key"), stageLabel)
			if stageKeys[stageKey] {
				s.throwError(laneLabel + " contains duplicate stage key '" + stageKey + "'.")
			}
			stageKeys[stageKey] = true
			generatedKey := laneKey + "." + stageKey
			s.validateLaneKey(s.str(generatedKey), stageLabel+" generated")
			if generatedKeys[generatedKey] {
				s.throwError("runs.lanes generated child key '" + generatedKey + "' is duplicated.")
			}
			generatedKeys[generatedKey] = true
			resume := stage.Get("resume")
			resumeStr, resumeIsStr := isJSString(resume)
			if !isUndef(resume) && !(resumeIsStr && resumeStr == "previous") {
				if si == 0 && resumeIsStr {
					s.throwError(stageLabel + " cannot resume a retained run id in runs.lanes; use runs.run(key, { resume: id }) outside lanes, or start the lane with an agent stage and use resume: \"previous\" later.")
				}
				s.throwError(stageLabel + " resume must be 'previous'.")
			}
			if si == 0 && resumeIsStr && resumeStr == "previous" {
				s.throwError(stageLabel + " cannot resume previous without a predecessor stage.")
			}
			params := s.restObject(stage, "key", "resume")
			validationParams := params
			if resumeIsStr && resumeStr == "previous" {
				validationParams = s.restObject(stage, "key", "resume")
				_ = validationParams.Set("resume", "retained-run-placeholder")
			}
			s.validateLaneStageBounds(validationParams, stageLabel)
			s.validateRunCall(s.str(generatedKey), validationParams, stageLabel, validationFingerprints)
			if existing, ok := s.runFingerprints[generatedKey]; ok && (resumeIsStr && resumeStr == "previous" || existing != stableRunJSON(canonicalRunParams(s.toGo(params).(*jsObject)))) {
				s.throwError("runs.lanes generated child key '" + generatedKey + "' is already used with incompatible launch params.")
			}
			l.stages = append(l.stages, &wfLaneStage{key: stageKey, generatedKey: generatedKey, resume: resume, params: params})
		}
		normalized = append(normalized, l)
	}
	return normalized
}

func (s *wfSandbox) laneStageParams(stage *wfLaneStage, previous goja.Value) goja.Value {
	if str, ok := isJSString(stage.resume); !ok || str != "previous" {
		return s.restObject(stage.params)
	}
	po, isObj := previous.(*goja.Object)
	if !isObj || goja.IsNull(previous) {
		return nil
	}
	id, ok := isJSString(po.Get("runId"))
	if !ok || jsTrim(id) == "" {
		return nil
	}
	out := s.restObject(stage.params)
	_ = out.Set("resume", jsTrim(id))
	return out
}

func (s *wfSandbox) structuredOutput(result goja.Value) *goja.Object {
	r, ok := result.(*goja.Object)
	if !ok || goja.IsNull(result) || s.realm.array(r) {
		return nil
	}
	so, ok := r.Get("structuredOutput").(*goja.Object)
	if !ok || goja.IsNull(r.Get("structuredOutput")) || s.realm.array(so) {
		return nil
	}
	return so
}

func (s *wfSandbox) laneStageVerdict(result goja.Value) string {
	so := s.structuredOutput(result)
	if so == nil {
		return ""
	}
	if v, ok := isJSString(so.Get("verdict")); ok && jsTrim(v) != "" {
		return s.laneBoardText(jsTrim(v))
	}
	return ""
}

func (s *wfSandbox) laneStageIsBlocked(result goja.Value) bool {
	so := s.structuredOutput(result)
	if so == nil {
		return false
	}
	v, ok := isJSString(so.Get("verdict"))
	return ok && v == "blocked"
}

func (s *wfSandbox) laneStageRecord(stageKey string, child goja.Value, forcedState string) *jsObject {
	var result *goja.Object
	if r, ok := child.(*goja.Object); ok && !goja.IsNull(child) && !s.realm.array(r) {
		result = r
	}
	truthy := func(name string) bool {
		return result != nil && result.Get(name) != nil && result.Get(name).ToBoolean()
	}
	okVal := false
	if result != nil {
		b, isB := isJSBool(result.Get("ok"))
		okVal = isB && b
	}
	verdict := s.laneStageVerdict(child)
	blocked := s.laneStageIsBlocked(child)
	state := forcedState
	if state == "" {
		stateV := ""
		if result != nil {
			stateV, _ = isJSString(result.Get("state"))
		}
		switch {
		case stateV == "running":
			state = "running"
		case truthy("stopped"):
			state = "stopped"
		case truthy("detached"):
			state = "detached"
		case okVal && blocked:
			state = "blocked"
		case okVal:
			state = "completed"
		default:
			state = "failed"
		}
	}
	errText := ""
	if state != "completed" && state != "running" {
		switch {
		case state == "blocked" && blocked:
			errText = "Stage returned a blocked verdict."
		default:
			errText = "Stage did not complete successfully."
			if result != nil {
				if e, ok := isJSString(result.Get("error")); ok {
					errText = e
				} else if o, ok := isJSString(result.Get("output")); ok {
					errText = o
				}
			}
		}
	}
	rec := newObject()
	rec.set("key", stageKey)
	if result != nil {
		if id, ok := isJSString(result.Get("runId")); ok && jsTrim(id) != "" {
			rec.set("runId", s.laneBoardText(jsTrim(id)))
		}
		rec.set("ok", okVal)
	}
	rec.set("state", state)
	if result != nil {
		if ref, ok := isJSString(result.Get("outputReference")); ok {
			if p := s.laneBoardPath(ref); p != "" {
				rec.set("outputReference", p)
			}
		}
	}
	if verdict != "" {
		rec.set("verdict", verdict)
	}
	if errText != "" {
		if t := s.laneBoardText(errText); t != "" {
			rec.set("error", t)
		}
	}
	return rec
}

func (s *wfSandbox) laneFailure(stageKey string, err goja.Value) goja.Value {
	text := ""
	if o, ok := err.(*goja.Object); ok && s.realm.isError(o) {
		text, _ = isJSString(o.Get("message"))
	} else {
		text = s.realm.safeString(err)
	}
	o := newObject()
	o.set("key", stageKey)
	o.set("ok", false)
	o.set("output", text)
	o.set("error", text)
	o.set("artifactPaths", []any{})
	return s.toJS(o)
}

// runCollected is runCollected (scripted-workflow.ts:415).
func (s *wfSandbox) runCollected(key string, params goja.Value, observe func([]wfObservation) goja.Value, generatedLaneKey string) goja.Value {
	s.validateRunCall(s.str(key), params, "runs.lanes stage", s.runFingerprints)
	launched := s.runHostCall(key, s.toGo(params).(*jsObject), true, nil, generatedLaneKey)
	observe([]wfObservation{{key: key, operation: "run", callID: launched.callID}})
	return s.thenP(launched.promise, s.decorateChild, nil)
}

func (s *wfSandbox) runLane(lane *wfLane, firstResult goja.Value, observe func([]wfObservation) goja.Value) goja.Value {
	var records []any
	appendSkipped := func(start int) {
		for i := start; i < len(lane.stages); i++ {
			r := newObject()
			r.set("key", lane.stages[i].key)
			r.set("state", "skipped")
			records = append(records, r)
		}
	}
	finish := func(state, failedStage string) goja.Value {
		o := newObject()
		o.set("key", lane.key)
		o.set("state", state)
		if failedStage != "" {
			o.set("failedStage", failedStage)
		}
		recs := records
		if recs == nil {
			recs = []any{}
		}
		o.set("stages", recs)
		return s.toJS(o)
	}
	blockedRec := func(stage *wfLaneStage, msg string) {
		r := newObject()
		r.set("key", stage.key)
		r.set("state", "blocked")
		r.set("error", msg)
		records = append(records, r)
	}
	var visit func(index int, previous goja.Value) goja.Value
	visit = func(index int, previous goja.Value) goja.Value {
		if index >= len(lane.stages) {
			return s.resolved(finish("complete", ""))
		}
		stage := lane.stages[index]
		if index == 0 {
			rec := s.laneStageRecord(stage.key, previous, "")
			records = append(records, rec)
			if st, _ := rec.str("state"); st != "completed" {
				appendSkipped(index + 1)
				return s.resolved(finish("blocked", stage.key))
			}
			return visit(index+1, previous)
		}
		prevObj, prevIsObj := previous.(*goja.Object)
		prevRunID := ""
		if prevIsObj {
			if id, ok := isJSString(prevObj.Get("runId")); ok {
				prevRunID = id
			}
		}
		if rs, ok := isJSString(stage.resume); ok && rs == "previous" && (!prevIsObj || goja.IsNull(previous) || jsTrim(prevRunID) == "") {
			blockedRec(stage, "Previous stage did not return a retained run id.")
			appendSkipped(index + 1)
			return s.resolved(finish("blocked", stage.key))
		}
		prevOK := false
		if prevIsObj && !goja.IsNull(previous) {
			b, isB := isJSBool(prevObj.Get("ok"))
			prevOK = isB && b
		}
		if !prevOK || s.laneStageIsBlocked(previous) {
			blockedRec(stage, "Previous stage did not complete successfully.")
			appendSkipped(index + 1)
			return s.resolved(finish("blocked", stage.key))
		}
		params := s.laneStageParams(stage, previous)
		if params == nil {
			blockedRec(stage, "Previous stage did not return a retained run id.")
			appendSkipped(index + 1)
			return s.resolved(finish("blocked", stage.key))
		}
		var launched goja.Value
		failed := false
		var failure goja.Value
		func() {
			defer func() {
				if r := recover(); r != nil {
					failure = s.panicValue(r)
					failed = true
				}
			}()
			launched = s.runCollected(stage.generatedKey, params, observe, lane.key)
		}()
		if failed {
			records = append(records, s.laneStageRecord(stage.key, s.laneFailure(stage.generatedKey, failure), ""))
			appendSkipped(index + 1)
			return s.resolved(finish("blocked", stage.key))
		}
		return s.thenP(launched, func(result goja.Value) goja.Value {
			rec := s.laneStageRecord(stage.key, result, "")
			records = append(records, rec)
			if st, _ := rec.str("state"); st == "completed" {
				return visit(index+1, result)
			}
			appendSkipped(index + 1)
			return finish("blocked", stage.key)
		}, func(err goja.Value) goja.Value {
			records = append(records, s.laneStageRecord(stage.key, s.laneFailure(stage.generatedKey, err), ""))
			appendSkipped(index + 1)
			return finish("blocked", stage.key)
		})
	}
	return visit(0, firstResult)
}

// panicValue is the script value a recovered panic carries (a thrown script value, or an error made of a Go one).
func (s *wfSandbox) panicValue(r any) goja.Value {
	switch x := r.(type) {
	case goja.Value:
		return x
	case *goja.Exception:
		return x.Value()
	case error:
		return s.vm.NewGoError(x)
	}
	return s.str(strings.TrimSpace(stringOf(r)))
}

func stringOf(r any) string {
	if s, ok := r.(string); ok {
		return s
	}
	b, _ := json.Marshal(r)
	return string(b)
}

func (s *wfSandbox) planStringMetadata(params *goja.Object, o *jsObject) {
	for _, f := range []string{"phase", "label", "agent"} {
		if v, ok := isJSString(params.Get(f)); ok && jsTrim(v) != "" {
			o.set(f, jsTrim(v))
		}
	}
}

func (s *wfSandbox) runLanes(specs goja.Value) goja.Value {
	lanes := s.validateLaneSpecs(specs)
	plan := make([]any, len(lanes))
	for li, lane := range lanes {
		stages := make([]any, len(lane.stages))
		for si, st := range lane.stages {
			o := newObject()
			o.set("key", st.key)
			o.set("generatedKey", st.generatedKey)
			s.planStringMetadata(st.params, o)
			if as, ok := isJSString(st.params.Get("as")); ok && jsTrim(as) != "" {
				o.set("outputName", jsTrim(as))
			}
			if os := st.params.Get("outputSchema"); os != nil && os.ToBoolean() {
				o.set("structured", true)
			}
			stages[si] = o
		}
		lo := newObject()
		lo.set("key", lane.key)
		lo.set("stages", stages)
		plan[li] = lo
	}
	s.port.lanePlan(plan)
	items := make([]any, len(lanes))
	for i, lane := range lanes {
		first := lane.stages[0]
		item := s.vm.NewObject()
		_ = item.Set("key", first.generatedKey)
		for _, k := range s.realm.keysOf(first.params) {
			_ = item.DefineDataProperty(k, first.params.Get(k), goja.FLAG_TRUE, goja.FLAG_TRUE, goja.FLAG_TRUE)
		}
		items[i] = item
	}
	laneKeys := make([]string, len(lanes))
	for i, l := range lanes {
		laneKeys[i] = l.key
	}
	_, launched := s.launchRunsAll(s.vm.NewArray(items...), laneKeys)
	firstResults := make([]goja.Value, len(launched))
	for i, l := range launched {
		firstResults[i] = s.thenP(l.promise, s.decorateChild, nil)
	}
	var tracked goja.Value
	observe := func(obs []wfObservation) goja.Value { return s.trackRun(obs, tracked) }
	chains := make([]any, len(lanes))
	for i, lane := range lanes {
		lane := lane
		chains[i] = s.thenP(firstResults[i], func(result goja.Value) goja.Value {
			return s.runLane(lane, result, observe)
		}, func(err goja.Value) goja.Value {
			return s.runLane(lane, s.laneFailure(lane.stages[0].generatedKey, err), observe)
		})
	}
	aggregate := s.callFn(s.promiseAll, s.realm.promiseCtor, s.vm.NewArray(chains...))
	var obs []wfObservation
	for _, l := range launched {
		obs = append(obs, wfObservation{key: l.key, operation: "run", callID: l.callID})
	}
	tracked = s.trackRun(obs, aggregate)
	return tracked
}

// ---- runs.all (scripted-workflow.ts:733-794) ----

type wfAllCall struct {
	key    string
	params *jsObject
}

type wfAllLaunch struct {
	key     string
	callID  int // 0 for a promise passed in
	promise goja.Value
}

func (s *wfSandbox) thenableInfo(item goja.Value, index int) *wfAllLaunch {
	o, ok := item.(*goja.Object)
	if !ok || goja.IsNull(item) || s.realm.array(o) {
		return nil
	}
	if _, callable := goja.AssertFunction(o.Get("then")); !callable {
		return nil
	}
	key := "runs.all[" + strconv.Itoa(index) + "]"
	callID := 0
	if t := s.promiseTracker(item); t != nil {
		for _, ob := range t.observations {
			if ob.operation == "run" {
				key = ob.key
				callID = ob.callID
				break
			}
		}
	}
	return &wfAllLaunch{key: key, callID: callID, promise: item}
}

func (s *wfSandbox) launchRunsAll(itemsV goja.Value, generatedLaneKeys []string) ([]wfAllCall, []wfAllLaunch) {
	items, ok := itemsV.(*goja.Object)
	if !ok || goja.IsNull(itemsV) || !s.realm.array(items) {
		s.throwError("runs.all(items) requires an array.")
	}
	n := int(items.Get("length").ToInteger())
	anyThenable := false
	for i := range n {
		if !s.realm.hasOwnProp(items, strconv.Itoa(i)) {
			continue
		}
		if s.thenableInfo(items.Get(strconv.Itoa(i)), i) != nil {
			anyThenable = true
			break
		}
	}
	if anyThenable {
		return s.launchRunsAllPermissive(items, n)
	}
	fingerprints := map[string]string{}
	for k, v := range s.runFingerprints {
		fingerprints[k] = v
	}
	var calls []wfAllCall
	for i := range n {
		idx := strconv.Itoa(i)
		if !s.realm.hasOwnProp(items, idx) {
			s.throwError("runs.all items must not contain sparse entries.")
		}
		item := items.Get(idx)
		io, ok := item.(*goja.Object)
		if !ok || goja.IsNull(item) || s.realm.array(io) {
			s.throwError("runs.all item " + idx + " must be an object.")
		}
		params := s.restObject(io, "key")
		s.validateRunCall(io.Get("key"), params, "runs.all item "+idx, fingerprints)
		key, _ := isJSString(io.Get("key"))
		calls = append(calls, wfAllCall{key: key, params: s.toGo(params).(*jsObject)})
	}
	s.runFingerprints = fingerprints
	s.nextCallID++
	batch := newObject()
	batch.set("id", "batch-"+strconv.Itoa(s.nextCallID))
	callList := make([]any, len(calls))
	for i, c := range calls {
		co := newObject()
		co.set("key", c.key)
		co.set("params", c.params)
		callList[i] = co
	}
	batch.set("calls", callList)
	launched := make([]wfAllLaunch, len(calls))
	for i, c := range calls {
		gk := ""
		if i < len(generatedLaneKeys) {
			gk = generatedLaneKeys[i]
		}
		l := s.runHostCall(c.key, c.params, true, batch, gk)
		launched[i] = wfAllLaunch{key: l.key, callID: l.callID, promise: l.promise}
	}
	return calls, launched
}

func (s *wfSandbox) launchRunsAllPermissive(items *goja.Object, n int) ([]wfAllCall, []wfAllLaunch) {
	if !s.warnedPermissive {
		s.warnedPermissive = true
		s.port.console("warn", workflowRunsAllPermissiveWarning[:len(workflowRunsAllPermissiveWarning)-1])
	}
	fingerprints := map[string]string{}
	for k, v := range s.runFingerprints {
		fingerprints[k] = v
	}
	var calls []wfAllCall
	launched := make([]*wfAllLaunch, 0, n)
	for i := range n {
		idx := strconv.Itoa(i)
		if !s.realm.hasOwnProp(items, idx) {
			s.throwError("runs.all items must not contain sparse entries.")
		}
		item := items.Get(idx)
		io, ok := item.(*goja.Object)
		if !ok || goja.IsNull(item) || s.realm.array(io) {
			s.throwError("runs.all item " + idx + " must be an object or a runs.run(...) launch promise.")
		}
		if th := s.thenableInfo(item, i); th != nil {
			calls = append(calls, wfAllCall{key: th.key, params: newObject()})
			launched = append(launched, th)
			continue
		}
		params := s.restObject(io, "key")
		s.validateRunCall(io.Get("key"), params, "runs.all item "+idx, fingerprints)
		key, _ := isJSString(io.Get("key"))
		calls = append(calls, wfAllCall{key: key, params: s.toGo(params).(*jsObject)})
		launched = append(launched, nil)
	}
	s.runFingerprints = fingerprints
	out := make([]wfAllLaunch, len(launched))
	for i, l := range launched {
		if l != nil {
			out[i] = *l
			continue
		}
		r := s.runHostCall(calls[i].key, calls[i].params, false, nil, "")
		out[i] = wfAllLaunch{key: r.key, callID: r.callID, promise: r.promise}
	}
	return calls, out
}

// ---- runs.host, steer, status, ref ----

func (s *wfSandbox) validateHostCommand(keyV, paramsV goja.Value) string {
	key := s.validateLaneKey(keyV, "runs.host")
	label := "runs.host('" + key + "')"
	params, ok := paramsV.(*goja.Object)
	if !ok || goja.IsNull(paramsV) || s.realm.array(params) {
		s.throwError(label + " params must be an object.")
	}
	var unknown []string
	for _, k := range s.realm.keysOf(params) {
		if !workflowHostAllowed[k] {
			unknown = append(unknown, k)
		}
	}
	if len(unknown) > 0 {
		hint := ""
		for _, u := range unknown {
			if u == "cwd" {
				hint = workflowHostCwdHint
			}
		}
		s.throwError(label + " params have unsupported fields: " + strings.Join(unknown, ", ") + "." + hint)
	}
	if k, ok := isJSString(params.Get("kind")); !ok || k != "command" {
		s.throwError(label + " kind must be 'command'.")
	}
	cmd, ok := isJSString(params.Get("command"))
	if !ok || jsTrim(cmd) == "" || strings.Contains(cmd, "\x00") || len(jsTrim(cmd)) > 16384 {
		s.throwError(label + " command must be a non-empty string of at most 16384 bytes without NUL.")
	}
	to := params.Get("timeoutMs")
	if !isJSInteger(to) || to.ToFloat() < 1 || to.ToFloat() > 86400000 {
		s.throwError(label + " timeoutMs must be an integer from 1 to 86400000.")
	}
	if out := params.Get("output"); !isUndef(out) {
		str, ok := isJSString(out)
		if !ok || jsTrim(str) == "" || workflowRelativePathBad.MatchString(str) || workflowControlChars.MatchString(str) || len(jsTrim(str)) > 240 {
			s.throwError(label + " output must be a bounded relative path without traversal or control characters.")
		}
	}
	if role := params.Get("role"); !isUndef(role) {
		if r, ok := isJSString(role); !ok || (r != "ci" && r != "gate") {
			s.throwError(label + " role must be 'ci' or 'gate'.")
		}
	}
	if prov := params.Get("provider"); !isUndef(prov) {
		str, ok := isJSString(prov)
		if !ok || jsTrim(str) == "" || strings.ContainsAny(str, "\r\n\x00") || len(jsTrim(str)) > 64 {
			s.throwError(label + " provider must be a non-empty single-line string of at most 64 bytes.")
		}
	}
	s.checkJSON(params, label+" params")
	if s.hostKeys[key] {
		s.throwError("Workflow key '" + key + "' is already in use.")
	}
	if _, ok := s.runFingerprints[key]; ok {
		s.throwError("Workflow key '" + key + "' is already in use.")
	}
	if s.hostKeyCount >= 32 {
		s.throwError("workflowScript supports at most 32 runs.host calls.")
	}
	s.hostKeys[key] = true
	s.hostKeyCount++
	return key
}

var workflowControlChars = regexp.MustCompile(`[\x00-\x1f\x7f]`)

func (s *wfSandbox) runsObject() goja.Value {
	runs := s.vm.NewObject()
	def := func(name string, length int, fn func(call goja.FunctionCall) goja.Value) {
		_ = runs.DefineDataProperty(name, s.native(name, length, fn), goja.FLAG_TRUE, goja.FLAG_TRUE, goja.FLAG_TRUE)
	}
	def("run", 2, func(call goja.FunctionCall) goja.Value {
		key, params := call.Argument(0), call.Argument(1)
		s.validateRunCall(key, params, "runs.run", s.runFingerprints)
		k, _ := isJSString(key)
		launched := s.runHostCall(k, s.toGo(params).(*jsObject), false, nil, "")
		p := s.thenP(launched.promise, s.decorateChild, nil)
		return s.trackRun([]wfObservation{{key: k, operation: "run", callID: launched.callID}}, p)
	})
	def("all", 1, func(call goja.FunctionCall) goja.Value {
		calls, launched := s.launchRunsAll(call.Argument(0), nil)
		obs := make([]wfObservation, 0, len(launched))
		promises := make([]any, len(launched))
		for i, l := range launched {
			if l.callID != 0 {
				obs = append(obs, wfObservation{key: l.key, operation: "run", callID: l.callID})
			}
			promises[i] = l.promise
		}
		keys := calls
		combined := s.trackCombinator(s.vm.NewArray(promises...), func(values goja.Value) goja.Value {
			all := s.callFn(s.promiseAll, s.realm.promiseCtor, values)
			return s.thenP(all, func(results goja.Value) goja.Value {
				ro := results.ToObject(s.vm)
				n := int(ro.Get("length").ToInteger())
				decorated := make([]any, n)
				for i := range n {
					decorated[i] = s.decorateChild(ro.Get(strconv.Itoa(i)))
				}
				_ = keys
				return s.wrapRunsAllResults(s.vm.NewArray(decorated...))
			}, nil)
		})
		return s.trackRun(obs, combined)
	})
	def("lanes", 1, func(call goja.FunctionCall) goja.Value { return s.runLanes(call.Argument(0)) })
	def("host", 2, func(call goja.FunctionCall) goja.Value {
		key := s.validateHostCommand(call.Argument(0), call.Argument(1))
		args := newObject()
		args.set("key", key)
		args.set("params", s.toGo(call.Argument(1)))
		return s.hostCall("host", args, &wfObservation{key: key, operation: "host"})
	})
	def("steer", 2, func(call goja.FunctionCall) goja.Value {
		keyV, msgV := call.Argument(0), call.Argument(1)
		key, ok := isJSString(keyV)
		if !ok || !workflowKeyPattern.MatchString(key) {
			s.throwError("runs.steer has an invalid key.")
		}
		msg, ok := isJSString(msgV)
		if !ok || jsTrim(msg) == "" {
			s.throwError("runs.steer message must be a non-empty string.")
		}
		var optV goja.Value = s.vm.NewObject()
		if len(call.Arguments) > 2 && !isUndef(call.Arguments[2]) {
			optV = call.Arguments[2]
		}
		opts, ok := optV.(*goja.Object)
		if !ok || goja.IsNull(optV) || s.realm.array(opts) {
			s.throwError("runs.steer options must be an object.")
		}
		for _, o := range s.realm.keysOf(opts) {
			if o != "mode" && o != "index" && o != "ackTimeoutMs" {
				s.throwError("runs.steer options contain unsupported field '" + o + "'.")
			}
		}
		if mode := opts.Get("mode"); !isUndef(mode) {
			m, ok := isJSString(mode)
			if !ok || (m != "steer" && m != "follow_up" && m != "auto") {
				s.throwError("runs.steer mode must be 'steer', 'follow_up', or 'auto'.")
			}
		}
		if idx := opts.Get("index"); !isUndef(idx) {
			if !isJSInteger(idx) || idx.ToFloat() < 0 || idx.ToFloat() > 1000000 {
				s.throwError("runs.steer index must be an integer between 0 and 1000000.")
			}
		}
		if ack := opts.Get("ackTimeoutMs"); !isUndef(ack) {
			if !isJSInteger(ack) || ack.ToFloat() < 1 {
				s.throwError("runs.steer ackTimeoutMs must be a positive integer.")
			}
		}
		args := newObject()
		args.set("key", key)
		args.set("message", jsTrim(msg))
		args.set("options", s.toGo(opts))
		return s.hostCall("steer", args, &wfObservation{key: key, operation: "steer"})
	})
	def("status", 1, func(call goja.FunctionCall) goja.Value {
		args := newObject()
		v := call.Argument(0)
		if str, ok := isJSString(v); ok {
			args.set("keyOrRunId", str)
		} else {
			args.set("keyOrRunId", undef{})
		}
		return s.hostCall("status", args, nil)
	})
	def("ref", 1, func(call goja.FunctionCall) goja.Value { return s.str(s.formatRef(call.Argument(0))) })
	def("refs", 1, func(call goja.FunctionCall) goja.Value {
		arr, ok := call.Argument(0).(*goja.Object)
		if !ok || goja.IsNull(call.Argument(0)) || !s.realm.array(arr) {
			s.throwError("runs.refs(results) requires an array.")
		}
		n := int(arr.Get("length").ToInteger())
		parts := make([]string, n)
		for i := range n {
			parts[i] = s.formatRef(arr.Get(strconv.Itoa(i)))
		}
		return s.str(strings.Join(parts, "\n"))
	})
	s.realm.call(s.realm.freeze, goja.Undefined(), runs)
	return runs
}

func (s *wfSandbox) validateStateKey(v goja.Value) string {
	k, ok := isJSString(v)
	if !ok || !workflowKeyPattern.MatchString(k) {
		s.throwError("state key must be 1-128 characters using letters, numbers, '.', '_' or '-', and start with a letter or number.")
	}
	return k
}

func (s *wfSandbox) stateObject() goja.Value {
	st := s.vm.NewObject()
	_ = st.DefineDataProperty("get", s.native("get", 1, func(call goja.FunctionCall) goja.Value {
		args := newObject()
		args.set("key", s.validateStateKey(call.Argument(0)))
		return s.hostCall("state.get", args, nil)
	}), goja.FLAG_TRUE, goja.FLAG_TRUE, goja.FLAG_TRUE)
	_ = st.DefineDataProperty("set", s.native("set", 2, func(call goja.FunctionCall) goja.Value {
		key := s.validateStateKey(call.Argument(0))
		s.checkJSON(call.Argument(1), "state.set('"+key+"') value")
		args := newObject()
		args.set("key", key)
		args.set("value", s.toGo(call.Argument(1)))
		return s.hostCall("state.set", args, nil)
	}), goja.FLAG_TRUE, goja.FLAG_TRUE, goja.FLAG_TRUE)
	s.realm.call(s.realm.freeze, goja.Undefined(), st)
	return st
}

func (s *wfSandbox) consoleObject() goja.Value {
	c := s.vm.NewObject()
	for _, level := range []string{"log", "info", "warn", "error"} {
		level := level
		_ = c.DefineDataProperty(level, s.native(level, 0, func(call goja.FunctionCall) goja.Value {
			parts := make([]string, len(call.Arguments))
			for i, a := range call.Arguments {
				if str, ok := isJSString(a); ok {
					parts[i] = str
				} else {
					parts[i] = s.realm.inspect(a)
				}
			}
			s.port.console(level, strings.Join(parts, " "))
			return goja.Undefined()
		}), goja.FLAG_TRUE, goja.FLAG_TRUE, goja.FLAG_TRUE)
	}
	s.realm.call(s.realm.freeze, goja.Undefined(), c)
	return c
}

func (s *wfSandbox) emitFunction() goja.Value {
	return s.native("emit", 1, func(call goja.FunctionCall) goja.Value {
		v := s.unwrapRunsAllResults(call.Argument(0), map[*goja.Object]goja.Value{})
		s.checkJSON(v, "emit")
		s.port.emit(s.toGo(v))
		return goja.Undefined()
	})
}

// ---- the run ----

func (s *wfSandbox) isSyntaxError(v goja.Value) bool {
	o, ok := v.(*goja.Object)
	if !ok {
		return false
	}
	if ctor, ok := s.vm.Get("SyntaxError").(*goja.Object); ok {
		if s.vm.InstanceOf(v, ctor) {
			return true
		}
	}
	if n, ok := isJSString(o.Get("name")); ok && n == "SyntaxError" {
		return true
	}
	return false
}

func workflowSyntaxErrorText(details string) string {
	return strings.Join([]string{
		"workflowScript must be valid JavaScript.",
		"If task text contains Markdown fences or backticks, use an array joined with \"\\n\" or escaped strings instead of a raw backtick template literal.",
		"",
		"Original SyntaxError:",
		details,
	}, "\n")
}

// postError is postWorkflowError (scripted-workflow.ts:876).
func (s *wfSandbox) postError(v goja.Value) {
	if s.finished {
		return
	}
	kind := "script"
	if o, ok := v.(*goja.Object); ok {
		if k, ok := s.errorKinds[o]; ok {
			kind = k
		}
	}
	text := s.realm.formatError(v)
	if s.isSyntaxError(v) {
		text = workflowSyntaxErrorText(text)
	}
	s.finished = true
	s.port.fail(text, kind, "")
}

func (s *wfSandbox) fail(message, kind string) {
	s.finished = true
	s.port.fail(message, kind, "")
}

// start compiles and runs the script (the "start" message, scripted-workflow.ts:1006-1090).
func (s *wfSandbox) start(m wfStart) {
	defer func() {
		if r := recover(); r != nil {
			s.postError(s.panicValue(r))
		}
	}()
	global := s.vm.GlobalObject()
	setGlobal := func(name string, v goja.Value) {
		_ = global.DefineDataProperty(name, v, goja.FLAG_TRUE, goja.FLAG_TRUE, goja.FLAG_TRUE)
	}
	if _, err := s.vm.RunString(workflowHardening); err != nil {
		s.postError(s.str(err.Error()))
		return
	}
	s.installJSONParse()
	setGlobal("runs", s.runsObject())
	setGlobal("Promise", s.workflowPromise())
	setGlobal("emit", s.emitFunction())
	setGlobal("console", s.consoleObject())
	if m.stateEnabled {
		setGlobal("state", s.stateObject())
	}
	argv := s.toJS(m.argsOrEmpty())
	s.deepFreeze(argv)
	setGlobal("args", argv)

	wrapped := workflowWrap(m.script)
	s.realm.wrapped = wrapped
	root, perr := workflowParse(wrapped)
	if perr != nil {
		if perr.Message == "async-generator" {
			s.port.fail("Error: "+workflowNestedAsyncError, "validation", "")
			s.finished = true
			return
		}
		details := "SyntaxError: " + perr.Message + " (" + strconv.Itoa(perr.Line) + ":" + strconv.Itoa(perr.Column) + ")"
		s.port.fail(workflowSyntaxErrorText(details), "validation", "")
		s.finished = true
		return
	}
	if hasNestedAsync(root) {
		s.port.fail("Error: "+workflowNestedAsyncError, "validation", "")
		s.finished = true
		return
	}
	prg, err := goja.Compile("workflow-script.js", wrapped, false)
	if err != nil {
		s.port.fail(workflowSyntaxErrorText("SyntaxError: "+err.Error()), "validation", "")
		s.finished = true
		return
	}
	res, err := s.vm.RunProgram(prg)
	if err != nil {
		s.runError(err)
		return
	}
	if p, ok := res.Export().(*goja.Promise); ok {
		s.topPromise = p
	}
}

func (m wfStart) argsOrEmpty() *jsObject {
	if m.args == nil {
		return newObject()
	}
	return m.args
}

// hasNestedAsync is assertPortableWorkflowScript's walk (scripted-workflow.ts:888-918).
func hasNestedAsync(root *esNode) bool {
	var wrapper *esNode
	if body := root.list("body"); len(body) > 0 && body[0] != nil {
		if expr := body[0].node("expression"); expr != nil {
			wrapper = expr.node("callee")
		}
	}
	found := false
	walkAst(root, func(n *esNode) {
		if n != wrapper && n.flag("async") && isFunctionNode(n) {
			found = true
		}
	}, true)
	return found
}

func (s *wfSandbox) runError(err error) {
	switch e := err.(type) {
	case *goja.Exception:
		s.postError(e.Value())
	case *goja.StackOverflowError:
		s.fail("RangeError: Maximum call stack size exceeded", "script")
	case *goja.InterruptedError:
		s.finished = true
	default:
		s.fail(err.Error(), "script")
	}
}

// afterJob settles the run once the script's promise or an unhandled rejection decided it. It runs after every job, when the
// microtask queue is drained (the point where Node reports an unhandled rejection).
func (s *wfSandbox) afterJob() {
	if s.finished {
		return
	}
	if s.topPromise != nil {
		switch s.topPromise.State() {
		case goja.PromiseStateFulfilled:
			s.complete(s.topPromise.Result())
			return
		case goja.PromiseStateRejected:
			s.dropUnhandled(s.topPromise)
			s.postError(s.topPromise.Result())
			return
		}
	}
	for _, p := range s.unhandled {
		if p == s.topPromise {
			continue
		}
		s.postError(p.Result())
		return
	}
}

func (s *wfSandbox) dropUnhandled(p *goja.Promise) {
	for i, u := range s.unhandled {
		if u == p {
			s.unhandled = append(s.unhandled[:i], s.unhandled[i+1:]...)
			return
		}
	}
}

func (s *wfSandbox) complete(value goja.Value) {
	s.finished = true
	var persisted goja.Value
	if value == nil || isUndef(value) {
		persisted = goja.Null()
	} else {
		persisted = s.omitUndefined(value, map[*goja.Object]bool{})
	}
	if m := s.assertJsonValue(persisted, "return", map[*goja.Object]bool{}); m != "" {
		s.port.fail("Error: "+m, "return-serialization", "return-serialization")
		return
	}
	s.port.complete(s.toGo(persisted))
}

var _ = utf8.RuneError

// installJSONParse makes JSON.parse fail with V8's messages (workflowJSONError); valid input goes to the engine's own parser.
func (s *wfSandbox) installJSONParse() {
	jsonObj := s.vm.Get("JSON").ToObject(s.vm)
	orig := s.realm.mustFn("JSON.parse")
	parse := s.native("parse", 2, func(call goja.FunctionCall) goja.Value {
		text := call.Argument(0).String()
		if msg := workflowJSONError(text); msg != "" {
			ctor := s.vm.Get("SyntaxError").ToObject(s.vm)
			e, err := s.vm.New(ctor, s.str(msg))
			if err != nil {
				s.rethrow(err)
			}
			panic(e)
		}
		return s.callFn(orig, jsonObj, call.Arguments...)
	})
	_ = jsonObj.DefineDataProperty("parse", parse, goja.FLAG_TRUE, goja.FLAG_TRUE, goja.FLAG_FALSE)
}
