package pi_subagents

import (
	"strings"
	"testing"
)

// The original's schemas test reads the schema the original registers; the port's copy is tooldefs.json, the request the
// original made (port/gen-tooldefs.py), so these twins check the schema the model sees.

func subagentParams(t *testing.T) any {
	t.Helper()
	o, _ := asObject(toolDefs()[subagentName].Parameters)
	return o
}

func schemaProp(t *testing.T, schema any, path ...string) *jsObject {
	t.Helper()
	cur := schema
	for _, k := range path {
		o, ok := asObject(cur)
		if !ok {
			return nil
		}
		props, ok := asObject(o.vals["properties"])
		if !ok {
			return nil
		}
		cur = props.vals[k]
	}
	o, _ := asObject(cur)
	return o
}

func descOf(o *jsObject) string {
	if o == nil {
		return ""
	}
	s, _ := o.vals["description"].(string)
	return s
}

func anyOfBranches(o *jsObject) []*jsObject {
	var out []*jsObject
	if o == nil {
		return out
	}
	for _, b := range anyList(o.vals["anyOf"]) {
		if bo, ok := asObject(b); ok {
			out = append(out, bo)
		}
	}
	return out
}

func hasAnyOfType(o *jsObject, typ string) bool {
	for _, b := range anyOfBranches(o) {
		if b.vals["type"] == typ {
			return true
		}
	}
	return false
}

func schemaCheck(schema, value any) bool { ok, _, _ := schemaErrors(schema, value); return ok }

func walkAllSchemas(t *testing.T, visit func(path string, node *jsObject)) {
	t.Helper()
	var walk func(path string, v any)
	walk = func(path string, v any) {
		if arr, ok := asArray(v); ok {
			for i, e := range arr {
				walk(path+"["+jsNumber(float64(i))+"]", e)
			}
			return
		}
		o, ok := asObject(v)
		if !ok {
			return
		}
		visit(path, o)
		for _, k := range o.order() {
			walk(path+"."+k, o.vals[k])
		}
	}
	for name, def := range toolDefs() {
		walk(name, def.Parameters)
	}
}

func TestSubagentParamsSchema(t *testing.T) {
	P := func() any { return subagentParams(t) }
	tw(t, "schemas", "accepts object or false output schema overrides and rejects null", func(t *testing.T) {
		v := func(s string) bool { return schemaCheck(P(), js(t, s)) }
		equal(t, v(`{"agent":"worker","task":"work","outputSchema":{"type":"object"}}`), true, "object")
		equal(t, v(`{"agent":"worker","task":"work","outputSchema":false}`), true, "false")
		equal(t, v(`{"agent":"worker","task":"work","outputSchema":null}`), false, "null")
		equal(t, v(`{"task":"work","chain":[{"agent":"worker","outputSchema":false}]}`), true, "chain item")
		// DynamicCollectSchema (the last assertion upstream) belongs to the dynamic fan-out slice and is not part of the tool's schema.
	})
	tw(t, "schemas", "includes context field and default precedence for fresh/fork execution mode", func(t *testing.T) {
		c := schemaProp(t, P(), "context")
		equal(t, c.vals["type"], "string", "type")
		equal(t, marshalJSON(c.vals["enum"], ""), `["fresh","fork","profile"]`, "enum")
		d := descOf(c)
		for _, re := range []string{`fresh`, `fork`, `profile`, `declared defaultContext`, `defaultSubagentContext wins over each agent defaultContext`, `overrides every child`, `implicit fork`, `else fresh`} {
			polMatches(t, d, re)
		}
	})
	tw(t, "schemas", "exposes named resources plus raw inline and file workflow script modes", func(t *testing.T) {
		wf := schemaProp(t, P(), "workflow")
		equal(t, [2]any{wf.vals["type"], wf.vals["minLength"]}, [2]any{"string", 1.0}, "workflow")
		polMatches(t, descOf(wf), `extension-owned workflow resource`, "i")
		args := schemaProp(t, P(), "args")
		equal(t, [2]any{args.vals["type"], args.vals["maxProperties"]}, [2]any{"object", 16.0}, "args")
		polMatches(t, descOf(args), `bounded plain-JSON`, "i")
		polMatches(t, descOf(args), `inline.*file-backed.*deeply frozen.*persisted.*secrets`, "i")
		ws := schemaProp(t, P(), "workflowScript")
		equal(t, [2]any{ws.vals["type"], ws.vals["minLength"]}, [2]any{"string", 1.0}, "workflowScript")
		for _, re := range []string{`Inline JavaScript statement body`, `top-level await`, `no runs.host`, `guide workflows`} {
			polMatches(t, descOf(ws), re)
		}
		wp := schemaProp(t, P(), "workflowScriptPath")
		equal(t, [2]any{wp.vals["type"], wp.vals["minLength"]}, [2]any{"string", 1.0}, "workflowScriptPath")
		polMatches(t, descOf(wp), `mutually exclusive with workflowScript`, "i")
		polMatches(t, descOf(wp), `request cwd`, "i")
		polMatches(t, descOf(wp), `host reads.*before sandbox`, "i")
		for _, n := range []string{"globalConcurrencyLimit", "maxSubagentSpawnsPerRun"} {
			c := schemaProp(t, P(), n)
			equal(t, [2]any{c.vals["type"], c.vals["minimum"]}, [2]any{"integer", 1.0}, n)
		}
		pf := schemaProp(t, P(), "preflight")
		equal(t, [2]any{pf.vals["type"], pf.vals["additionalProperties"]}, [2]any{"object", false}, "preflight")
		polMatches(t, descOf(pf), `display-only`, "i")
		equal(t, schemaProp(t, pf, "version").vals["minimum"], 1.0, "version min")
		equal(t, schemaProp(t, pf, "version").vals["maximum"], 1.0, "version max")
		equal(t, schemaProp(t, pf, "lanes").vals["maxItems"], 64.0, "lanes")
		cp := schemaProp(t, P(), "chatProgress")
		equal(t, cp.vals["type"], "string", "chatProgress")
		equal(t, marshalJSON(cp.vals["enum"], ""), `["auto","off","live-card"]`, "chatProgress enum")
		polMatches(t, descOf(cp), `same Git repository`, "i")
		polMatches(t, descOf(cp), `async:false`)
		polMatches(t, descOf(cp), `async: omit or auto/off`)
		wt := schemaProp(t, P(), "worktree")
		equal(t, wt.vals["type"], "boolean", "worktree")
		polMatches(t, descOf(wt), `each workflow child`, "i")
		iso := schemaProp(t, P(), "isolation")
		equal(t, iso.vals["type"], "string", "isolation")
		equal(t, marshalJSON(iso.vals["enum"], ""), `["none","worktree"]`, "isolation enum")
		gate := schemaProp(t, P(), "gate")
		equal(t, hasAnyOfType(gate, "string"), true, "gate string")
		equal(t, hasAnyOfType(gate, "object"), true, "gate object")
		for _, b := range anyOfBranches(gate) {
			if b.vals["type"] == "object" {
				equal(t, marshalJSON(b.vals["required"], ""), `["command"]`, "gate required")
				equal(t, marshalJSON(schemaProp(t, b, "output").vals["enum"], ""), `["json"]`, "gate output")
			}
		}
		polMatches(t, descOf(gate), `cannot be combined with acceptance`, "i")
		task := schemaProp(t, P(), "task")
		equal(t, task.vals["type"], "string", "task")
		polMatches(t, descOf(task), `one-child`, "i")
		polMatches(t, descOf(schemaProp(t, P(), "agent")), `one-child`, "i")
		equal(t, schemaProp(t, P(), "clarify") == nil, true, "clarify should not be model-facing")
		out := schemaProp(t, P(), "output")
		equal(t, out != nil, true, "output remains a workflow child default")
		polMatches(t, descOf(out), `relative workflow paths use managed artifact routing`, "i")
		polMatches(t, descOf(out), `Bind durable output here, not task prose`, "i")
		polMatches(t, descOf(out), `outputReference.*outputPathMapping.*artifactPaths`, "i")
	})
	tw(t, "schemas", "omits removed legacy and workflow-child-only fields", func(t *testing.T) {
		for _, n := range []string{"tasks", "chain", "concurrency", "chainDir", "step", "schedule", "scheduleName", "resume"} {
			equal(t, schemaProp(t, P(), n) == nil, true, n+" should not be public")
		}
	})
	tw(t, "schemas", "allows runtime validation of management and control action strings", func(t *testing.T) {
		a := schemaProp(t, P(), "action")
		equal(t, [2]any{a.vals["type"], a.vals["minLength"]}, [2]any{"string", 1.0}, "action")
		_, hasEnum := a.vals["enum"]
		equal(t, hasEnum, false, "enum")
		d := descOf(a)
		for _, re := range []string{`Management/control only; omit for execution`, `validate accepts either script input`, `guide topic tool-reference`} {
			polMatches(t, d, re)
		}
		polNotMatches(t, d, `orchestration\.`)
	})
	tw(t, "schemas", "accepts prompt-free capability discovery on list requests", func(t *testing.T) {
		c := schemaProp(t, P(), "capabilities")
		equal(t, c.vals["type"], "boolean", "type")
		for _, re := range []string{`list:`, `compact`, `system prompt`} {
			polMatches(t, descOf(c), re, "i")
		}
		equal(t, schemaCheck(P(), js(t, `{"action":"list","capabilities":true}`)), true, "true")
		equal(t, schemaCheck(P(), js(t, `{"action":"list","capabilities":"true"}`)), false, `"true"`)
	})
	tw(t, "schemas", "keeps agentContract.version as integer bounds without an enum (Gemini schema subset)", func(t *testing.T) {
		v := schemaProp(t, P(), "agentContract", "version")
		equal(t, [3]any{v.vals["type"], v.vals["minimum"], v.vals["maximum"]}, [3]any{"integer", 1.0, 1.0}, "bounds")
		_, hasEnum := v.vals["enum"]
		equal(t, hasEnum, false, "enum")
	})
	tw(t, "schemas", "documents workflow timeout aliases and omits removed turn budgets", func(t *testing.T) {
		to, mr := schemaProp(t, P(), "timeoutMs"), schemaProp(t, P(), "maxRuntimeMs")
		equal(t, [2]any{to.vals["minimum"], mr.vals["minimum"]}, [2]any{1.0, 1.0}, "minimum")
		for _, re := range []string{`foreground and single async runs`, `use config timeoutMs, else 30m`, `async composites have no default parent deadline`} {
			polMatches(t, descOf(to), re, "i")
		}
		polNotMatches(t, descOf(to), `(?i)foreground-only`)
		polMatches(t, descOf(mr), `timeoutMs`, "i")
		polMatches(t, descOf(mr), `Alias timeoutMs \(same defaults\)`)
		equal(t, schemaProp(t, P(), "turnBudget") == nil, true, "turnBudget")
		tb := schemaProp(t, P(), "toolBudget")
		equal(t, schemaProp(t, tb, "soft").vals["minimum"], 1.0, "soft")
		equal(t, schemaProp(t, tb, "hard").vals["minimum"], 1.0, "hard")
	})
	tw(t, "schemas", "includes root-only reported usage budget", func(t *testing.T) {
		ub := schemaProp(t, P(), "usageBudget")
		equal(t, ub != nil, true, "usageBudget")
		equal(t, schemaCheck(P(), js(t, `{"usageBudget":{}}`)), false, "empty")
		equal(t, schemaCheck(P(), js(t, `{"usageBudget":{"tokens":{"hard":1}}}`)), true, "tokens")
		for _, path := range [][]string{{"tokens", "soft"}, {"tokens", "hard"}, {"costUsd", "soft"}, {"costUsd", "hard"}} {
			equal(t, schemaProp(t, ub, path[0], path[1]).vals["exclusiveMinimum"], 0.0, strings.Join(path, "."))
		}
		polMatches(t, descOf(ub), `root-only`, "i")
		polMatches(t, descOf(ub), `running children are not stopped`, "i")
	})
	tw(t, "schemas", "includes subagent control fields", func(t *testing.T) {
		id := schemaProp(t, P(), "id")
		equal(t, id.vals["type"], "string", "id")
		polMatches(t, descOf(id), `status`, "i")
		polMatches(t, descOf(id), `control`, "i")
		run := schemaProp(t, P(), "runId")
		equal(t, run.vals["type"], "string", "runId")
		polMatches(t, descOf(run), `prefer id`, "i")
		dir := schemaProp(t, P(), "dir")
		equal(t, dir.vals["type"], "string", "dir")
		polMatches(t, descOf(dir), `status`, "i")
		polMatches(t, descOf(dir), `control`, "i")
		view := schemaProp(t, P(), "view")
		equal(t, view.vals["type"], "string", "view")
		equal(t, marshalJSON(view.vals["enum"], ""), `["fleet","transcript"]`, "view enum")
		polMatches(t, descOf(view), `status view`, "i")
		polMatches(t, descOf(view), `transcript`, "i")
		lines := schemaProp(t, P(), "lines")
		equal(t, [2]any{lines.vals["minimum"], lines.vals["maximum"]}, [2]any{1.0, 500.0}, "lines")
		polMatches(t, descOf(lines), `transcript`, "i")
		add := schemaProp(t, P(), "additional")
		equal(t, add.vals["minimum"], 1.0, "additional")
		polMatches(t, descOf(add), `grant-spawn-budget`)
		polMatches(t, descOf(add), `root interactive parent`, "i")
		ctl := schemaProp(t, P(), "control")
		for _, n := range []string{"needsAttentionAfterMs", "activeNoticeAfterMs", "activeNoticeAfterTurns", "activeNoticeAfterTokens", "failedToolAttemptsBeforeAttention"} {
			equal(t, schemaProp(t, ctl, n).vals["minimum"], 1.0, n)
		}
		on, _ := asObject(schemaProp(t, ctl, "notifyOn").vals["items"])
		equal(t, marshalJSON(on.vals["enum"], ""), `["active_long_running","needs_attention"]`, "notifyOn")
		ch, _ := asObject(schemaProp(t, ctl, "notifyChannels").vals["items"])
		equal(t, marshalJSON(ch.vals["enum"], ""), `["event","async","intercom"]`, "notifyChannels")
	})
	tw(t, "schemas", "exposes tolerant wait mode on bg_wait", func(t *testing.T) {
		w, _ := asObject(toolDefs()["bg_wait"].Parameters)
		id, nb, all, stop, to := schemaProp(t, w, "id"), schemaProp(t, w, "nonBlocking"), schemaProp(t, w, "all"), schemaProp(t, w, "stopOnAttention"), schemaProp(t, w, "timeoutMs")
		polMatches(t, descOf(id), `ordinary async subagent runs already notify this session natively`, "i")
		polMatches(t, descOf(id), `same-turn blocking results are truly needed`)
		polMatches(t, descOf(nb), `provider, detached, or other background work without a native completion notification`, "i")
		polMatches(t, descOf(nb), `do not need a subscription`)
		polMatches(t, descOf(all), `same-turn result.*truly needed`)
		polNotMatches(t, descOf(all), `spawn a replacement`)
		equal(t, stop.vals["type"], "boolean", "stopOnAttention")
		polMatches(t, descOf(stop), `idle or long-thinking attention`)
		polMatches(t, descOf(to), `waitTool\.defaultTimeoutMs`)
		polMatches(t, descOf(to), `non-error active-work result`)
	})
	tw(t, "schemas", "does not emit description-only schema nodes", func(t *testing.T) {
		var bad []string
		walkAllSchemas(t, func(path string, n *jsObject) {
			if n.hasOwn("description") && !n.hasOwn("type") && !n.hasOwn("anyOf") {
				bad = append(bad, path)
			}
		})
		equal(t, bad, []string(nil), "paths")
	})
	tw(t, "schemas", "does not emit array-typed schema nodes without items", func(t *testing.T) {
		var bad []string
		walkAllSchemas(t, func(path string, n *jsObject) {
			if n.vals["type"] == "array" && !n.hasOwn("items") {
				bad = append(bad, path)
			}
		})
		equal(t, bad, []string(nil), "paths")
	})
	tw(t, "schemas", "keeps only top-level parameter descriptions to keep the provider payload compact", func(t *testing.T) {
		serialized := marshalJSON(P(), "")
		if n := len(serialized); n > 13000 {
			t.Errorf("expected concise schema at or under 13k chars, got %d", n)
		}
		equal(t, strings.Contains(serialized, `"$ref"`), false, "$ref")
		equal(t, strings.Contains(serialized, `"$defs"`), false, "$defs")
		equal(t, strings.Count(serialized, "Evidence policy;"), 1, "evidence policy once")
		polMatches(t, descOf(schemaProp(t, P(), "agent")), `management target`)
		acc := descOf(schemaProp(t, P(), "acceptance"))
		polMatches(t, acc, `Evidence policy`)
		polMatches(t, acc, `guide tool-reference.*levels, evidence and review.required`)
		mission := descOf(schemaProp(t, P(), "mission"))
		polMatches(t, mission, `exactly one non-empty title or summary`)
		polMatches(t, mission, `goal only true`)
		polMatches(t, mission, `requires budget\.tokens`)
		var nested []string
		var walk func(path string, v any)
		walk = func(path string, v any) {
			if arr, ok := asArray(v); ok {
				for i, e := range arr {
					walk(path+"["+jsNumber(float64(i))+"]", e)
				}
				return
			}
			o, ok := asObject(v)
			if !ok {
				return
			}
			parts := strings.Split(path, ".")
			top := len(parts) == 3 && parts[0] == "SubagentParams" && parts[1] == "properties"
			if _, isStr := o.vals["description"].(string); isStr && !top {
				nested = append(nested, path+".description")
			}
			for _, k := range o.order() {
				walk(path+"."+k, o.vals[k])
			}
		}
		walk("SubagentParams", P())
		equal(t, nested, []string(nil), "nested descriptions")
	})
	tskip(t, "schemas", "preserves TypeBox metadata while pruning provider-visible descriptions", "tests TypeBox's non-enumerable ~kind and ~optional symbols on the original's in-memory schema objects; the Go port holds the JSON the provider receives (tooldefs.json), which cannot carry them")
	tw(t, "schemas", "does not emit provider-rejected schema shapes", func(t *testing.T) {
		var bad []string
		walkAllSchemas(t, func(path string, n *jsObject) {
			if e, ok := asArray(n.vals["enum"]); ok {
				for _, v := range e {
					if _, isStr := v.(string); !isStr {
						bad = append(bad, path+".enum")
						break
					}
				}
			}
			if _, ok := asArray(n.vals["type"]); ok {
				bad = append(bad, path+".type")
			}
			if n.hasOwn("anyOf") && n.hasOwn("type") {
				bad = append(bad, path+".type+anyOf")
			}
			for _, kw := range []string{"allOf", "const", "if", "then", "not"} {
				if n.hasOwn(kw) {
					bad = append(bad, path+"."+kw)
				}
			}
		})
		equal(t, bad, []string(nil), "paths")
	})
	tw(t, "schemas", "uses provider-friendly anyOf unions for flexible fields and chain items", func(t *testing.T) {
		skill := schemaProp(t, P(), "skill")
		_, hasType := skill.vals["type"]
		equal(t, hasType, false, "skill type")
		hasStringArray := false
		for _, b := range anyOfBranches(skill) {
			if items, ok := asObject(b.vals["items"]); b.vals["type"] == "array" && ok && items.vals["type"] == "string" {
				hasStringArray = true
			}
		}
		equal(t, hasStringArray, true, "skill string array")
		equal(t, hasAnyOfType(skill, "boolean") && hasAnyOfType(skill, "string"), true, "skill boolean and string")
		out := schemaProp(t, P(), "output")
		_, hasType = out.vals["type"]
		equal(t, hasType, false, "output type")
		equal(t, hasAnyOfType(out, "string") && hasAnyOfType(out, "boolean"), true, "output string and boolean")
		cfg := schemaProp(t, P(), "config")
		_, hasType = cfg.vals["type"]
		equal(t, hasType, false, "config type")
		objAdditional := false
		for _, b := range anyOfBranches(cfg) {
			objAdditional = objAdditional || (b.vals["type"] == "object" && b.vals["additionalProperties"] == true)
		}
		equal(t, objAdditional, true, "config object")
		equal(t, hasAnyOfType(cfg, "string"), true, "config string")
		acc := schemaProp(t, P(), "acceptance")
		_, hasType = acc.vals["type"]
		equal(t, hasType, false, "acceptance type")
		equal(t, hasAnyOfType(acc, "string") && hasAnyOfType(acc, "boolean"), true, "acceptance string and boolean")
		var level, reviewed, objString *jsObject
		for _, b := range anyOfBranches(acc) {
			if b.vals["type"] != "string" {
				continue
			}
			e := anyList(b.vals["enum"])
			switch {
			case contains(stringsOf(e), "auto"):
				level = b
			case contains(stringsOf(e), "reviewed"):
				reviewed = b
			case b.vals["enum"] == nil:
				objString = b
			}
		}
		equal(t, marshalJSON(level.vals["enum"], ""), `["auto","attested","checked"]`, "levels")
		equal(t, marshalJSON(reviewed.vals["enum"], ""), `["reviewed"]`, "reviewed")
		equal(t, reviewed.vals["deprecated"], true, "deprecated")
		equal(t, objString.vals["pattern"], `^\s*\{`, "object string pattern")
		for _, re := range []string{`omit for read-only/review`, `prefer object`, `false disables; true invalid`, `review\.required`} {
			polMatches(t, descOf(acc), re, "i")
		}
		var obj *jsObject
		for _, b := range anyOfBranches(acc) {
			if b.vals["type"] == "object" {
				obj = b
			}
		}
		equal(t, obj != nil, true, "object branch")
		equal(t, obj.vals["additionalProperties"], true, "object additionalProperties")
		equal(t, strings.Contains(marshalJSON(obj, ""), `"anyOf"`), false, "object branch has no anyOf")
	})
	tw(t, "schemas", "validates representative flexible field values with TypeBox compiler", func(t *testing.T) {
		v := func(s string) bool { return schemaCheck(P(), js(t, s)) }
		for _, field := range []string{"acceptance", "mission", "thinking"} {
			var boolBranch *jsObject
			for _, b := range anyOfBranches(schemaProp(t, P(), field)) {
				if b.vals["type"] == "boolean" {
					boolBranch = b
				}
			}
			equal(t, marshalJSON(boolBranch, ""), `{"type":"boolean"}`, field+" boolean branch")
			equal(t, v(`{"`+field+`":false}`), true, field+" false")
			equal(t, v(`{"`+field+`":true}`), true, field+" true")
			equal(t, v(`{"`+field+`":123}`), false, field+" 123")
		}
		for _, a := range []string{`"auto"`, `"attested"`, `"checked"`, `false`, `{"level":"checked"}`, `"{\"level\":\"checked\"}"`, `"  \n {\"level\":\"checked\"}"`} {
			equal(t, v(`{"agent":"worker","task":"Fix","acceptance":`+a+`}`), true, a+" acceptance should validate")
		}
		for _, a := range []string{`"cheked"`, `"none"`, `"verified"`, `"not-json"`, `"[{\"level\":\"checked\"}]"`} {
			equal(t, v(`{"agent":"worker","task":"Fix","acceptance":`+a+`}`), false, a+" acceptance should not validate")
		}
		valid := []string{
			`{"skill":"review"}`, `{"workflowScript":"return await runs.run(\"one\", {agent: \"reviewer\", task: \"check\"})"}`, `{"workflowScriptPath":"workflows/review.js"}`, `{"skill":false}`,
			`{"action":"get","agent":"worker"}`, `{"workflowScript":"return runs.run('main', { agent: 'worker', task: 'Fix', acceptance: false })","timeoutMs":1000}`,
			`{"action":"steer","id":"run-1","message":"focus on tests"}`, `{"action":"steer","id":"run-1","index":0,"message":"focus on tests"}`, `{"action":"not-a-real-action"}`,
			`{"config":{"name":"reviewer","description":"Review things"}}`, `{"config":"{\"name\":\"reviewer\",\"description\":\"Review things\"}"}`,
			`{"agent":"worker","task":"Fix","acceptance":"{\"level\":\"checked\",\"evidence\":[\"commands-run\"]}"}`,
		}
		for _, s := range valid {
			equal(t, v(s), true, s+" should validate")
		}
		invalid := []string{
			`{"skill":123}`, `{"skill":[123]}`, `{"output":123}`, `{"timeoutMs":0}`, `{"maxRuntimeMs":-1}`, `{"config":[]}`, `{"config":null}`,
			`{"agent":"worker","task":"Fix","toolBudget":{"hard":0}}`, `{"agent":"worker","task":"Fix","toolBudget":{"hard":3,"soft":0}}`,
			`{"agent":"worker","task":"Fix","toolBudget":{"hard":3,"block":[123]}}`, `{"agent":"worker","task":"Fix","toolBudget":{"hard":3,"block":[]}}`,
			`{"agent":"worker","task":"Fix","toolBudget":{"hard":3,"block":"read"}}`,
		}
		for _, s := range invalid {
			equal(t, v(s), false, s+" should not validate")
		}
	})
}
