import assert from "node:assert/strict";
import { test } from "node:test";
import { pigToolCalls } from "../server/tool-calls.ts";

const call = (over) => ({ id: "c1", name: "edit", title: "edit", kind: "edit", status: "completed", input: null, output: null, locations: [], ...over });
const run = (toolCall) => pigToolCalls.toolCall(toolCall, { sessionId: "s" });

test("a single replacement becomes old and new strings", () => {
  const out = run(call({ input: { path: "a.txt", edits: [{ oldText: "x", newText: "y" }] }, locations: ["/w/a.txt"] }));
  assert.deepEqual(out.input, { path: "a.txt", edits: [{ oldText: "x", newText: "y" }], filePath: "a.txt", oldString: "x", newString: "y" });
});

test("several replacements keep the file path and no partial strings", () => {
  const edits = [{ oldText: "a", newText: "b" }, { oldText: "c", newText: "d" }];
  const out = run(call({ input: { path: "a.txt", edits } }));
  assert.deepEqual(out.input, { path: "a.txt", edits, filePath: "a.txt" });
});

test("a write carries its content as the new string", () => {
  const out = run(call({ name: "write", input: { path: "n.txt", content: "hello" } }));
  assert.deepEqual(out.input, { path: "n.txt", content: "hello", filePath: "n.txt", newString: "hello" });
});

test("the location stands in for a missing path", () => {
  const out = run(call({ input: { edits: [] }, locations: ["/w/b.txt"] }));
  assert.equal(out.input.filePath, "/w/b.txt");
});

test("other kinds and malformed payloads pass through unchanged", () => {
  const read = call({ kind: "read", name: "read", input: { path: "a" } });
  assert.equal(run(read), read);
  const empty = call({ input: null });
  assert.equal(run(empty), empty);
  const noPath = call({ input: { edits: [] }, locations: [] });
  assert.equal(run(noPath), noPath);
  const odd = call({ input: { path: "a", edits: [{ oldText: 1, newText: 2 }] } });
  assert.deepEqual(run(odd).input, { path: "a", edits: [{ oldText: 1, newText: 2 }], filePath: "a" });
});

test("a failed call with no raw output gets a reason, because the wire contract refuses a null error", () => {
  const bash = call({ kind: "execute", name: "bash", title: "echo paseo-ok", status: "failed", input: { command: "echo paseo-ok" }, output: null });
  assert.deepEqual(run(bash).output, { message: "echo paseo-ok failed" });
  const withOutput = call({ kind: "execute", status: "failed", output: { content: "boom" } });
  assert.equal(run(withOutput), withOutput);
  const done = call({ kind: "execute", status: "completed", output: null });
  assert.equal(run(done), done);
});
