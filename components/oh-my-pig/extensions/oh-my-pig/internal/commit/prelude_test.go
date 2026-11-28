package commit

import (
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/approval"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/eval"
)

// upstream: packages/coding-agent/test/commit-selective-prelude.test.ts

// TestCommitJavaScriptFacade runs upstream's commit-selective-prelude.test.ts cases
// against our embedded prelude.js using node or bun.
func TestCommitJavaScriptFacade(t *testing.T) {
	runtimeBin := ""
	if p, err := exec.LookPath("bun"); err == nil {
		runtimeBin = p
	} else if p, err := exec.LookPath("node"); err == nil {
		runtimeBin = p
	} else {
		t.Skip("neither bun nor node on PATH")
	}

	script := `
const assert = require("node:assert");
const vm = require("node:vm");
const source = ` + mustJSON(commitJavascript) + `;

function facade(diff = "patch") {
	const calls = [];
	const printed = [];
	const context = vm.createContext({
		__omp_log__: (_level, text) => printed.push(text),
		__omp_prelude__: async (_name, parameters) => {
			calls.push(JSON.parse(JSON.stringify(parameters)));
			if (parameters.action === "diff") return diff;
			return { text: "summary", details: { id: "d1", remaining: { id: "d2" } } };
		},
	});
	vm.runInContext(source, context);
	const run = code => vm.runInContext("(async () => { " + code + " })()", context);
	return { calls, printed, run };
}

(async () => {
	// 1. rejects unknown option names
	{
		const { calls, run } = facade();
		await assert.rejects(run('await commit.draft({ cwd: "../ws" })'), /draft\(\): unknown option "cwd"; expected agents, repo, silent/);
		assert.deepEqual(calls, []);
		await run('globalThis.d = await commit.draft({ repo: "../ws", agents: "self" })');
		assert.deepEqual(calls, [{ action: "draft", agents: "self", repo: "../ws" }]);
		await assert.rejects(run('await d.commit("fix: x", { no_verify: true })'), /commit\(\): unknown option "no_verify"; expected body, fixes, amend, noVerify, silent/);
		await assert.rejects(run('await d.take("a.ts", { unit: ["1a"] })'), /take\(\): unknown option "unit"/);
		assert.equal(calls.length, 1);
	}
	// 2. accepts documented positional arguments
	{
		const { calls, run } = facade();
		await run('globalThis.d = await commit.draft("ParserFix", { repo: "../ws" })');
		await run('await d.only("a.ts", "b.ts", { silent: true })');
		await run('await d.only(["c.ts", "d.ts"])');
		await run('await d.take("a.ts", "2a", 3)');
		await run('await d.drop("a.ts", { units: ["1b"] })');
		await run('await d.commit("fix: x", ["- body"], { fixes: 12 })');
		assert.deepEqual(calls, [
			{ action: "draft", agents: "ParserFix", repo: "../ws" },
			{ action: "only", draft: "d1", globs: ["a.ts", "b.ts"] },
			{ action: "only", draft: "d1", globs: ["c.ts", "d.ts"] },
			{ action: "take", draft: "d1", path: "a.ts", units: ["2a", "3"] },
			{ action: "drop", draft: "d1", path: "a.ts", units: ["1b"] },
			{ action: "commit", draft: "d1", subject: "fix: x", body: ["- body"], fixes: [12] },
		]);
		await assert.rejects(run('await d.commit("fix: x", ["- a"], { body: ["- b"] })'), /commit\(\): body given both positionally and as an option/);
		await assert.rejects(run('await d.take("a.ts", "1a", { units: ["2a"] })'), /take\(\): units given both positionally and as an option/);
		await assert.rejects(run('await d.show("a.ts", "b.ts")'), /show\(\) takes at most 1 positional argument/);
		assert.equal(calls.length, 6);
	}
	// 3. diff returns patch even when folded
	{
		const plain = facade("-old\n+new\n");
		await plain.run('globalThis.d = await commit.draft()');
		assert.equal(await plain.run('return await d.diff()'), "-old\n+new\n");

		const folded = facade({ text: "!…[-old-]{+new+}…\n", details: { patch: "-long old\n+long new\n" } });
		await folded.run('globalThis.d = await commit.draft()');
		assert.equal(await folded.run('return await d.diff()'), "-long old\n+long new\n");
		assert.equal(folded.printed.at(-1), "!…[-old-]{+new+}…\n");
	}
	console.log("JS_FACADE_OK");
})().catch(err => { console.error(err); process.exit(1); });
`
	cmd := exec.Command(runtimeBin, "-e", script)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("JS facade test failed: %v\n%s", err, string(out))
	}
	if !strings.Contains(string(out), "JS_FACADE_OK") {
		t.Fatalf("missing JS_FACADE_OK in output:\n%s", string(out))
	}
}

func TestCommitPythonFacade(t *testing.T) {
	pyBin, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not on PATH")
	}
	script := `
import asyncio, json

calls = []
async def _omp_prelude(name, params):
    assert name == "commit"
    calls.append(params)
    if params["action"] == "diff":
        return {"text": "!folded", "details": {"patch": "-old\n+new\n"}}
    return {"text": "summary", "details": {"id": "d1", "remaining": {"id": "d2"}}}

` + commitPython + `

async def main():
    d = await commit.draft("ParserFix", repo="../ws", silent=True)
    assert d.id == "d1"
    await d.only(["a.ts", "b.ts"], silent=True)
    await d.take("a.ts", "2a", 3, silent=True)
    await d.drop("a.ts", "1b", grep="foo", silent=True)
    patch = await d.diff(silent=True)
    assert patch == "-old\n+new\n"
    res = await d.commit("fix: x", ["- body"], fixes=12, no_verify=True, silent=True)
    assert d.id == "d2"
    assert res["remaining"]["id"] == "d2"
    assert calls == [
        {"agents": "ParserFix", "repo": "../ws", "action": "draft"},
        {"globs": ["a.ts", "b.ts"], "draft": "d1", "action": "only"},
        {"path": "a.ts", "units": ["2a", "3"], "draft": "d1", "action": "take"},
        {"path": "a.ts", "units": ["1b"], "grep": "foo", "draft": "d1", "action": "drop"},
        {"draft": "d1", "action": "diff"},
        {"subject": "fix: x", "body": ["- body"], "fixes": [12], "noVerify": True, "draft": "d1", "action": "commit"},
    ], calls
    print("PY_FACADE_OK")

asyncio.run(main())
`
	cmd := exec.Command(pyBin, "-c", script)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Python facade test failed: %v\n%s", err, string(out))
	}
	if !strings.Contains(string(out), "PY_FACADE_OK") {
		t.Fatalf("missing PY_FACADE_OK in output:\n%s", string(out))
	}
}

func TestParseCommitParamsAndDescribeCall(t *testing.T) {
	if got := describeCall(json.RawMessage(`{"action":"draft","agents":"ParserFix","repo":"../other"}`)); got != "commit.draft(ParserFix, repo=../other)" {
		t.Fatalf("describeCall draft = %q", got)
	}
	if got := describeCall(json.RawMessage(`{"action":"take","draft":"d1","path":"CHANGELOG.md","units":["1a"],"grep":"foo"}`)); got != "commit.take(CHANGELOG.md 1a /foo/)" {
		t.Fatalf("describeCall take = %q", got)
	}
	if got := describeCall(json.RawMessage(`{"action":"only","draft":"d1","globs":["a.ts","b.ts"]}`)); got != "commit.only(a.ts, b.ts)" {
		t.Fatalf("describeCall only = %q", got)
	}
	if got := describeCall(json.RawMessage(`{"action":"commit","draft":"d1","subject":"fix: a"}`)); got != `commit.commit("fix: a")` {
		t.Fatalf("describeCall commit = %q", got)
	}
	if got := describeCall(json.RawMessage(`{"action":"diff","draft":"d1"}`)); got != "commit.diff()" {
		t.Fatalf("describeCall diff = %q", got)
	}
	if _, err := parseCommitParams(json.RawMessage(`{"action":"draft","extra":1}`)); err == nil {
		t.Fatalf("expected error for unknown key")
	}
	if _, err := parseCommitParams(json.RawMessage(`{"action":"commit","draft":"d1","subject":""}`)); err == nil {
		t.Fatalf("expected error for empty subject")
	}
	if _, err := parseCommitParams(json.RawMessage(`{"action":"bogus"}`)); err == nil {
		t.Fatalf("expected error for unknown action")
	}
}

func TestSessionDraftStoreCap(t *testing.T) {
	store := newSessionDraftStore()
	for i := range maxDraftsPerSession + 3 {
		d := &Draft{ID: "d" + itoa(i)}
		store.remember("s1", d, ScopeDefault())
	}
	if _, _, ok := store.lookup("s1", "d0"); ok {
		t.Fatalf("expected oldest draft d0 to be evicted past MAX_DRAFTS=%d", maxDraftsPerSession)
	}
	if _, _, ok := store.lookup("s1", "d2"); ok {
		t.Fatalf("expected draft d2 to be evicted")
	}
	if _, _, ok := store.lookup("s1", "d3"); !ok {
		t.Fatalf("expected draft d3 to remain")
	}
	if _, _, ok := store.lookup("s1", "d10"); !ok {
		t.Fatalf("expected newest draft d10 to remain")
	}
	// Invoking show on an evicted draft through the prelude handler returns omp's exact expiration message.
	p := PreludeDefinition(store)
	if _, err := p.Invoke(eval.PreludeCall{}, json.RawMessage(`{"action":"show","draft":"d0"}`)); err == nil ||
		err.Error() != "Draft d0 expired or was committed; call commit.draft() again." {
		t.Fatalf("evicted draft error = %v", err)
	}
	store.endSession("s1")
	if _, _, ok := store.lookup("s1", "d10"); ok {
		t.Fatalf("expected endSession to clear drafts")
	}
}

func mustJSON(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// TestCommitPreludeApprovalTiersAndDenial verifies upstream prelude-definition.ts:189-192:
// `commit` action and invalid params map to tier "exec"; draft/show/take/drop/only/diff
// map to tier "read". When approval denies `commit`, no git commit is created and index
// and worktree remain untouched.
func TestCommitPreludeApprovalTiersAndDenial(t *testing.T) {
	store := newSessionDraftStore()
	p := PreludeDefinition(store)
	if p.ApprovalFor == nil {
		t.Fatalf("expected PreludeDefinition to set ApprovalFor")
	}

	readCases := []string{
		`{"action":"draft"}`,
		`{"action":"draft","agents":"self"}`,
		`{"action":"show","draft":"d1"}`,
		`{"action":"take","draft":"d1","path":"a.txt"}`,
		`{"action":"drop","draft":"d1","path":"a.txt"}`,
		`{"action":"only","draft":"d1","globs":["*.go"]}`,
		`{"action":"diff","draft":"d1"}`,
	}
	for _, raw := range readCases {
		if got := p.ApprovalTier(json.RawMessage(raw)); got != "read" {
			t.Fatalf("ApprovalTier(%s) = %q, want read", raw, got)
		}
	}
	execCases := []string{
		`{"action":"commit","draft":"d1","subject":"feat: x"}`,
		`{"action":"commit","draft":"d1","subject":"feat: x","noVerify":true}`,
		`{"action":"bogus"}`,
		`{"action":"draft","unknown":1}`,
		`{"action":"commit","draft":"d1","subject":""}`,
		`{}`,
		`not-json`,
	}
	for _, raw := range execCases {
		if got := p.ApprovalTier(json.RawMessage(raw)); got != "exec" {
			t.Fatalf("ApprovalTier(%s) = %q, want exec", raw, got)
		}
	}

	// End-to-end denial check: in read-only mode (or explicit deny), approval.CheckPrelude
	// allows draft ("read") and rejects commit ("exec"), leaving HEAD, index, and worktree untouched.
	prevSetting := approval.Setting
	prevSession := approval.SessionSetting
	t.Cleanup(func() {
		approval.Setting = prevSetting
		approval.SessionSetting = prevSession
	})
	settingFn := func(_ sdk.Context, key string) any {
		if key == "tools.approvalMode" {
			return "write"
		}
		return nil
	}
	approval.Setting = settingFn
	approval.SessionSetting = settingFn

	ws := newEmptyRepo(t)
	writeFileMust(t, filepath.Join(ws.root, "a.txt"), []byte("one\n"))
	testGit(t, ws.root, nil, "add", ".")
	testGit(t, ws.root, nil, "commit", "-qm", "seed")
	ws.write(t, "Main", "", "a.txt", "one\ntwo\n")
	ws.journal.Flush()

	headBefore := testGit(t, ws.root, nil, "rev-parse", "HEAD")
	indexBefore := testGit(t, ws.root, nil, "ls-files", "--stage")
	workBefore := readFileMust(t, filepath.Join(ws.root, "a.txt"))

	draftParams := json.RawMessage(`{"action":"draft"}`)
	if err := approval.CheckPrelude(sdk.Context{}, "commit", p.ApprovalTier(draftParams), draftParams); err != nil {
		t.Fatalf("draft should be allowed in read-only mode: %v", err)
	}
	draft, err := CreateDraft(DraftSource{Cwd: ws.root, Journal: ws.journal, Caller: "Main"})
	if err != nil {
		t.Fatal(err)
	}
	store.remember("_default", draft, ScopeDefault())

	commitParams := json.RawMessage(`{"action":"commit","draft":"` + draft.ID + `","subject":"feat: denied","noVerify":true}`)
	if err := approval.CheckPrelude(sdk.Context{}, "commit", p.ApprovalTier(commitParams), commitParams); err == nil {
		t.Fatalf("expected commit to be blocked by approval.CheckPrelude in read-only mode")
	}
	// Verify git repo untouched.
	if got := testGit(t, ws.root, nil, "rev-parse", "HEAD"); got != headBefore {
		t.Fatalf("HEAD changed after denied commit: %s != %s", got, headBefore)
	}
	if got := testGit(t, ws.root, nil, "ls-files", "--stage"); got != indexBefore {
		t.Fatalf("index changed after denied commit")
	}
	if got := readFileMust(t, filepath.Join(ws.root, "a.txt")); got != workBefore {
		t.Fatalf("worktree changed after denied commit")
	}
	// Also test through eval's Prelude registry check.
	eval.RegisterPrelude(p)
	if got, ok := eval.LookupPrelude("commit"); !ok || got.ApprovalTier(commitParams) != "exec" {
		t.Fatalf("registered commit prelude ApprovalTier mismatch")
	}
}
