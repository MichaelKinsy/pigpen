// Times the original's QuestionnaireSession (TypeScript, loaded with Pi's jiti) on the same input and flow as the Go
// benchmarks in extensions/rpiv-ask-user-question/internal/ui/bench_test.go, for the PR's performance section.
//
//   JITI=<path to jiti lib/jiti.cjs> node --expose-gc port/perf/bench-original.mjs [runs]
//
// Prints one line per benchmark: "<name> ns/op=<n> heapBytes/op=<n>" (the mean of the iterations; heapBytes/op is the
// growth of the V8 heap's allocated total, measured around a forced collection, so it is a lower bound).
import { createRequire } from "node:module";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import v8 from "node:v8";

const here = dirname(fileURLToPath(import.meta.url));
const oracle = resolve(here, "../oracle");
const require = createRequire(import.meta.url);
const { createJiti } = require(process.env.JITI ?? "jiti");
const jiti = createJiti(join(oracle, "bench.ts"), { interopDefault: true });
const { initTheme } = await jiti.import("@earendil-works/pi-coding-agent");
// The preview pane's markdown reads Pi's global theme (getMarkdownTheme); the rest uses the identity theme below.
initTheme("dark");
const { QuestionnaireSession } = await jiti.import(join(oracle, "state/questionnaire-session.ts"));

const identity = (_c, s) => s;
const theme = { fg: identity, bg: identity, bold: (s) => s, italic: (s) => s, underline: (s) => s, strikethrough: (s) => s, inverse: (s) => s };
const DOWN = "\x1b[B", UP = "\x1b[A", ENTER = "<ENTER>", TAB = "\t";
const keybindings = {
  matches(data, name) {
    switch (name) {
      case "tui.select.up": return data === UP;
      case "tui.select.down": return data === DOWN;
      case "tui.select.confirm": return data === ENTER;
      case "tui.input.newLine": return data === "\x1b\r";
      case "tui.editor.cursorUp": return data === UP;
      case "tui.editor.cursorDown": return data === DOWN;
      case "tui.select.cancel": return data === "\x1b";
      case "tui.editor.deleteToLineStart": return data === "\x15";
      case "app.editor.external": return data === "\x07";
      default: return false;
    }
  },
};
const params = { questions: [
  { question: "Which library should we use for dates?", header: "Library", options: [
    { label: "date-fns", description: "Modular and tree-shakeable", preview: "## date-fns\n\n- small\n- `format(new Date(), 'yyyy')`\n\n```ts\nimport { format } from 'date-fns';\n```" },
    { label: "dayjs", description: "Moment-compatible, 2 kB", preview: "## dayjs\n\nA **tiny** library with a [moment](https://momentjs.com) API." },
    { label: "luxon", description: "Immutable, timezone aware", preview: "## luxon\n\nUses `Intl` for time zones." } ] },
  { question: "Which checks should run?", header: "Checks", multiSelect: true, options: [
    { label: "lint", description: "Static analysis" }, { label: "unit", description: "Unit tests" }, { label: "e2e", description: "End to end tests" } ] },
  { question: "Merge strategy?", header: "Merge", options: [
    { label: "squash", description: "One commit" }, { label: "rebase", description: "Linear history" } ] },
] };
const itemsByTab = params.questions.map((q) => [
  ...q.options.map((o) => ({ kind: "option", label: o.label, description: o.description })),
  { kind: "other", label: "Type something." },
]);
const flow = [DOWN, ENTER, " ", DOWN, " ", TAB, ENTER, ENTER];

function make(results) {
  return new QuestionnaireSession({
    tui: { terminal: { columns: 120, rows: 40 }, requestRender() {} }, theme, params, itemsByTab,
    done: (r) => results.push(r), keybindings, editInput: async () => undefined, collapseKey: "off", canReopenWhileHidden: false,
  });
}
const heap = () => v8.getHeapStatistics().total_heap_size + v8.getHeapStatistics().malloced_memory;
function bench(name, n, body) {
  for (let i = 0; i < Math.min(30, n); i++) body();
  global.gc?.();
  const t0 = process.hrtime.bigint();
  for (let i = 0; i < n; i++) body();
  const ns = Number(process.hrtime.bigint() - t0) / n;
  console.log(`${name} ns/op=${Math.round(ns)}`);
}
const n = Number(process.argv[2] ?? 300);
{
  const s = make([]); const c = s.component; c.render(120);
  bench("Render", n, () => { c.invalidate(); c.render(120); });
}
{
  const s = make([]); const c = s.component; c.render(120);
  bench("Keystroke", n, () => { s.dispatch(DOWN); c.render(120); s.dispatch(UP); c.render(120); });
}
bench("FullFlow", n, () => {
  const results = []; const s = make(results); const c = s.component; c.render(120);
  for (const k of flow) { s.dispatch(k); c.render(120); }
  if (results.length === 0) throw new Error("the flow did not finish");
});
