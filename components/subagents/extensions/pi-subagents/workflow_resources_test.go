package pi_subagents

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"
)

// Twins of test/unit/workflow-resources.test.ts.
const resourcesFile = "workflow-resources"

func wantMatch(t *testing.T, got, pattern string) {
	t.Helper()
	if !regexp.MustCompile(pattern).MatchString(got) {
		t.Errorf("%q does not match /%s/", got, pattern)
	}
}

func mustResource(t *testing.T, name string, args any, session string) *ResolvedWorkflowResource {
	t.Helper()
	r, msg := ResolveWorkflowResource(name, args, session)
	if r == nil {
		t.Fatalf("resolve %q failed: %s", name, msg)
	}
	return r
}

func register(t *testing.T, session string, def WorkflowResourceDefinition) WorkflowResourceRegistration {
	t.Helper()
	reg, err := RegisterWorkflowResource(session, def)
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

func TestWorkflowResourcesTwins(t *testing.T) {
	tw(t, resourcesFile, "scopes registrations by session and snapshots definitions, args and issued grants", func(t *testing.T) {
		args := jo("nested", jo("task", "original"))
		expansion := WorkflowResourceExpansion{Script: "return 'original';", HostCommands: []WorkflowResourceHostAuthority{{Key: "check", Command: " node check.mjs "}, {Key: "other", Command: "node other.mjs"}}}
		def := WorkflowResourceDefinition{Name: "acme.check", Version: 2, Resolve: func(in *jsObject) WorkflowResourceExpansion {
			in.vals["nested"].(*jsObject).set("task", "changed")
			return expansion
		}}
		reg := register(t, "one", def)
		otherDef := def
		otherDef.Resolve = func(*jsObject) WorkflowResourceExpansion { return WorkflowResourceExpansion{Script: "return 'two';"} }
		other := register(t, "two", otherDef)
		defer other.Dispose()
		defer reg.Dispose()
		def.Version = 99
		def.Resolve = func(*jsObject) WorkflowResourceExpansion {
			return WorkflowResourceExpansion{Error: "replacement must not run"}
		}
		if _, err := RegisterWorkflowResource("one", def); err == nil || !strings.Contains(err.Error(), "already registered") {
			t.Fatalf("duplicate registration error = %v", err)
		}
		if r, _ := ResolveWorkflowResource("acme.check", args, ""); r != nil {
			t.Fatal("resolved without a session")
		}
		if r, _ := ResolveWorkflowResource("acme.check", args, "wrong"); r != nil {
			t.Fatal("resolved in the wrong session")
		}
		resolved := mustResource(t, "acme.check", args, "one")
		if got := args.vals["nested"].(*jsObject).vals["task"]; got != "original" {
			t.Fatalf("args were mutated: %v", got)
		}
		if resolved.Provenance.Version != 2 {
			t.Fatalf("version = %d", resolved.Provenance.Version)
		}
		expansion.Script = "return 'changed';"
		expansion.HostCommands[0].Command = "node changed.mjs"
		reg.Dispose()
		if r, _ := ResolveWorkflowResource("acme.check", args, "one"); r != nil {
			t.Fatal("resolved after dispose")
		}
		replacement := register(t, "one", WorkflowResourceDefinition{Name: "acme.check", Version: 2, Resolve: func(*jsObject) WorkflowResourceExpansion { return WorkflowResourceExpansion{Script: "return 'new';"} }})
		defer replacement.Dispose()
		reg.Dispose() // disposing the first registration again must not remove the replacement
		mustResource(t, "acme.check", jo(), "one")
		mustResource(t, "acme.check", jo(), "two")
		if _, _, msg := ConsumeWorkflowResourcePermit(resolved.Permit, resolved.Script); msg != "" {
			t.Fatalf("consume: %s", msg)
		}
		if resolved.Script != "return 'original';" {
			t.Fatalf("script = %q", resolved.Script)
		}
		for _, c := range []struct{ key, cmd string }{{"check", "node check.mjs"}, {"other", "node other.mjs"}} {
			if msg := AuthorizeWorkflowResourceHost(resolved.Permit, c.key, c.cmd); msg != "" {
				t.Errorf("%s: %s", c.key, msg)
			}
		}
		for _, c := range []struct{ key, cmd string }{{"check", "node other.mjs"}, {"other", "node check.mjs"}, {"check", "node changed.mjs"}} {
			wantMatch(t, AuthorizeWorkflowResourceHost(resolved.Permit, c.key, c.cmd), "not allowed")
		}
		_, _, msg := ConsumeWorkflowResourcePermit(resolved.Permit, resolved.Script)
		wantMatch(t, msg, "already consumed")
	})

	tw(t, resourcesFile, "lets trusted validation select fixed commands while task text remains data", func(t *testing.T) {
		reg := register(t, "binding", WorkflowResourceDefinition{Name: "acme.review-check", Version: 1, Resolve: func(args *jsObject) WorkflowResourceExpansion {
			for _, k := range args.order() {
				if k != "task" && k != "check" {
					return WorkflowResourceExpansion{Error: "Only task and check=quick are supported."}
				}
			}
			task, ok := args.vals["task"].(string)
			if !ok || args.vals["check"] != "quick" {
				return WorkflowResourceExpansion{Error: "Only task and check=quick are supported."}
			}
			return WorkflowResourceExpansion{Script: "return " + marshalJSON(task, "") + ";", HostCommands: []WorkflowResourceHostAuthority{{Key: "check", Command: "node check.mjs --quick"}}}
		}})
		defer reg.Dispose()
		task := `"); await runs.host("injected", { command: "bad" }); //`
		resolved := mustResource(t, "acme.review-check", jo("task", task, "check", "quick"), "binding")
		res := wfRun(t, RunWorkflowScriptOptions{
			Script: resolved.Script,
			Launch: func(context.Context, string, *jsObject, WorkflowAdmission) *WorkflowFuture[*jsObject] {
				return WorkflowRejected[*jsObject](errors.New("No child expected"))
			},
			Status: func(context.Context, string) *WorkflowFuture[*jsObject] {
				return WorkflowRejected[*jsObject](errors.New("No status expected"))
			},
		})
		if res.Value != task {
			t.Fatalf("value = %v", res.Value)
		}
		bad := []*jsObject{
			jo("task", task, "check", "quick; bad"),
			jo("task", task, "check", "quick", "flags", "--extra"),
			jo("task", task, "check", "quick", "command", "bad"),
			jo("task", time.Now(), "check", "quick"),
			jo("task", strings.Repeat("x", 16385), "check", "quick"),
		}
		for i, args := range bad {
			if r, _ := ResolveWorkflowResource("acme.review-check", args, "binding"); r != nil {
				t.Errorf("case %d resolved", i)
			}
		}
	})

	tw(t, resourcesFile, "rejects invalid registrations and protects builtin names", func(t *testing.T) {
		good := WorkflowResourceDefinition{Name: "acme.check", Version: 1, Resolve: func(*jsObject) WorkflowResourceExpansion { return WorkflowResourceExpansion{Script: "return true;"} }}
		with := func(f func(d *WorkflowResourceDefinition)) WorkflowResourceDefinition { d := good; f(&d); return d }
		cases := []struct {
			session string
			def     WorkflowResourceDefinition
		}{
			{" ", good},
			{"validation", with(func(d *WorkflowResourceDefinition) { d.Name = "run-ci" })},
			{"validation", with(func(d *WorkflowResourceDefinition) { d.Name = "review" })},
			{"validation", with(func(d *WorkflowResourceDefinition) { d.Name = "bad name" })},
			{"validation", with(func(d *WorkflowResourceDefinition) { d.Version = 0 })},
			{"validation", with(func(d *WorkflowResourceDefinition) { d.Resolve = nil })},
		}
		// The original also feeds `trusted: true` and `issuerPackage: "trusted"`; the typed Go definition has no such fields, so
		// they cannot be supplied at all.
		for i, c := range cases {
			reg, err := RegisterWorkflowResource(c.session, c.def)
			if err == nil {
				reg.Dispose()
				t.Errorf("case %d registered", i)
			}
		}
	})

	tw(t, resourcesFile, "contains throws, async results and malformed expansions without issuing permits", func(t *testing.T) {
		// Asynchronous results (a rejected promise, a thenable) and the extra-field/non-string-error shapes cannot be
		// returned by a Go Resolve; the panic, empty expansion and malformed grants remain.
		long := strings.Repeat("x", 5000)
		invalid := []func(*jsObject) WorkflowResourceExpansion{
			func(*jsObject) WorkflowResourceExpansion { panic(errors.New(long)) },
			func(*jsObject) WorkflowResourceExpansion { return WorkflowResourceExpansion{} },
			func(*jsObject) WorkflowResourceExpansion { return WorkflowResourceExpansion{Script: " "} },
			func(*jsObject) WorkflowResourceExpansion {
				return WorkflowResourceExpansion{Script: "return true;", HostCommands: []WorkflowResourceHostAuthority{{Key: "a", Command: "node a.mjs"}, {Key: "a", Command: "node b.mjs"}}}
			},
			func(*jsObject) WorkflowResourceExpansion {
				return WorkflowResourceExpansion{Script: "return true;", HostCommands: []WorkflowResourceHostAuthority{{Key: "../a", Command: "node a.mjs"}}}
			},
			func(*jsObject) WorkflowResourceExpansion {
				return WorkflowResourceExpansion{Script: "return true;", HostCommands: []WorkflowResourceHostAuthority{{Key: "a", Command: "node\x00a"}}}
			},
		}
		for i, resolve := range invalid {
			reg := register(t, "invalid", WorkflowResourceDefinition{Name: "acme.invalid", Version: 1, Resolve: resolve})
			r, msg := ResolveWorkflowResource("acme.invalid", jo(), "invalid")
			reg.Dispose()
			if r != nil {
				t.Errorf("case %d issued a resource", i)
			}
			if jsLen(msg) > 4096 {
				t.Errorf("case %d error is %d units", i, jsLen(msg))
			}
		}
	})

	tskip(t, resourcesFile, "shares registrations across evaluated module copies", "tests that two evaluated copies of a TypeScript module share one registry through a global symbol; a Go package has exactly one registry variable")

	tw(t, resourcesFile, "resolves and executes an extension-owned named workflow script", func(t *testing.T) {
		resolved := mustResource(t, "run-ci", jo("command", "npm test", "timeoutMs", float64(1000)), "")
		wantMatch(t, resolved.Script, `runs\.host\("ci"`)
		want := WorkflowResourceProvenance{Kind: "workflow", Name: "run-ci", Version: 1, Invocation: "named", Expansion: "resolved", ID: resolved.Provenance.ID}
		if resolved.Provenance != want || resolved.Provenance.ID == "" {
			t.Fatalf("provenance = %+v", resolved.Provenance)
		}
		if msg := AuthorizeWorkflowResourceHost(resolved.Permit, "ci", "npm test"); msg != "Workflow resource authority is unavailable." {
			t.Fatalf("before consume: %q", msg)
		}
		if _, _, msg := ConsumeWorkflowResourcePermit(resolved.Permit, resolved.Script); msg != "" {
			t.Fatal(msg)
		}
		if msg := AuthorizeWorkflowResourceHost(resolved.Permit, "ci", "npm test"); msg != "" {
			t.Fatalf("after consume: %q", msg)
		}
		opts := wfOptions(resolved.Script)
		opts.Host = func(ctx context.Context, key string, p WorkflowHostCommandParams) *WorkflowFuture[WorkflowHostCommandResult] {
			if key != "ci" || p.Command != "npm test" {
				t.Errorf("host call %q %q", key, p.Command)
			}
			r := hostOK(key, "ci.log")
			r.Stdout = "ok"
			return WorkflowResolved(r)
		}
		res := wfRun(t, opts)
		if ok, _ := res.Value.(*jsObject).flag("ok"); !ok {
			t.Fatalf("value = %v", res.Value)
		}
	})

	tw(t, resourcesFile, "does not authorize raw equivalent scripts or unconsumed/forged permits", func(t *testing.T) {
		if msg := AuthorizeWorkflowResourceHost(&WorkflowResourcePermit{}, "ci", "npm test"); msg != "Workflow resource authority is unavailable." {
			t.Fatalf("forged: %q", msg)
		}
		raw := `return await runs.host("ci", { kind: "command", command: "npm test", timeoutMs: 1000 });`
		resolved := mustResource(t, "run-ci", jo("command", "npm test", "timeoutMs", float64(1000)), "")
		if msg := AuthorizeWorkflowResourceHost(resolved.Permit, "ci", "npm test"); msg != "Workflow resource authority is unavailable." {
			t.Fatalf("unconsumed: %q", msg)
		}
		if raw == resolved.Script {
			t.Fatal("raw script equals the expansion")
		}
	})

	tw(t, resourcesFile, "rejects missing metadata, mismatched scripts, and unauthorized host combinations", func(t *testing.T) {
		resolved := mustResource(t, "run-ci", jo("command", "npm test"), "")
		if _, _, msg := ConsumeWorkflowResourcePermit(resolved.Permit, resolved.Script+"\n"); msg != "Workflow resource permit does not match the resolved workflow script." {
			t.Fatalf("mismatch: %q", msg)
		}
		if msg := AuthorizeWorkflowResourceHost(resolved.Permit, "shell", "npm test"); msg != "Workflow resource authority is unavailable." {
			t.Fatalf("unconsumed: %q", msg)
		}
		if _, _, msg := ConsumeWorkflowResourcePermit(resolved.Permit, resolved.Script); msg != "" {
			t.Fatal(msg)
		}
		wantMatch(t, AuthorizeWorkflowResourceHost(resolved.Permit, "shell", "npm test"), "not allowed")
		wantMatch(t, AuthorizeWorkflowResourceHost(resolved.Permit, "ci", "git status"), "not allowed")
		if msg := AuthorizeWorkflowResourceHost(resolved.Permit, "ci", "npm test"); msg != "" {
			t.Fatalf("allowed pair: %q", msg)
		}
		review := mustResource(t, "review", jo("task", "Review"), "")
		if _, _, msg := ConsumeWorkflowResourcePermit(review.Permit, review.Script); msg != "" {
			t.Fatal(msg)
		}
		wantMatch(t, AuthorizeWorkflowResourceHost(review.Permit, "ci", "npm test"), "not allowed")
	})

	tw(t, resourcesFile, "validates resource names and bounded arguments before creating permits", func(t *testing.T) {
		for _, c := range []struct {
			name string
			args *jsObject
		}{
			{"unknown", jo()},
			{"run ci", jo()},
			{"run-ci", jo("command", "rm -rf /")},
			{"run-ci", jo("timeoutMs", float64(0))},
			{"run-ci", jo("extra", true)},
			{"review", jo("task", "")},
			{"review", jo("task", "Review", "extra", true)},
			{"review", jo("task", strings.Repeat("x", 16*1024+1))},
		} {
			if r, _ := ResolveWorkflowResource(c.name, c.args, ""); r != nil {
				t.Errorf("%s %s resolved", c.name, marshalJSON(c.args, ""))
			}
		}
	})

	tskip(t, resourcesFile, "rejects argument validation failures even when the error message is empty", "feeds a `task` getter that throws an Error with an empty message; Go args are plain values, so argument normalization cannot throw (the resolver-panic containment is twinned by 'contains throws, async results and malformed expansions without issuing permits')")

	tw(t, resourcesFile, "reports unauthorized raw host execution through the workflow primitive when no host is supplied", func(t *testing.T) {
		we := wfFail(t, wfOptions(`return await runs.host("ci", { kind: "command", command: "npm test", timeoutMs: 1000 });`))
		wantMatch(t, we.Message, `runs\.host is unavailable`)
	})
}
