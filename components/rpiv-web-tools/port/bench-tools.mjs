// Times the original's web_search and web_fetch execute() on the same inputs as bench_test.go (a Brave answer with
// two results; a 20 KB HTML page), with fetch replaced by a canned responder. Run from port/ with Node 24:
//   JITI=<path of jiti's lib/jiti.mjs> HOME=<scratch> BRAVE_SEARCH_API_KEY=KEY node bench-tools.mjs <calls>
// Prints one line per tool: name, warm median us, warm p90 us, cold first call us.
// The original is TypeScript with .js import specifiers: load it the way Pi does, through jiti (JITI names its entry).
const { createJiti } = await import(process.env.JITI ?? "jiti");
const registerWebTools = await createJiti(import.meta.url, { moduleCache: false }).import(new URL("./oracle/index.ts", import.meta.url).pathname, { default: true });

const searchBody = JSON.stringify({ web: { results: [
  { title: "T1", url: "https://one", description: "S1" },
  { title: "T2", url: "https://two", description: "S2" }] } });
const page = "<html><head><title>Bench</title></head><body>" + "<p>Hello &amp; welcome to <b>the</b> page</p>\n".repeat(450) + "</body></html>";
globalThis.fetch = async (url) =>
  String(url).includes("api.search.brave.com")
    ? new Response(searchBody, { status: 200, headers: { "content-type": "application/json" } })
    : new Response(page, { status: 200, headers: { "content-type": "text/html" } });

const tools = new Map();
registerWebTools({ registerTool: (t) => tools.set(t.name, t), registerCommand() {}, on() {}, registerShortcut() {} });
const ctx = { hasUI: false, ui: {}, cwd: process.cwd() };
const calls = Number(process.argv[2] ?? 200);
async function time(name, params) {
  const run = async () => {
    const t0 = process.hrtime.bigint();
    await tools.get(name).execute("tc", params, undefined, undefined, ctx);
    return Number(process.hrtime.bigint() - t0) / 1e3;
  };
  const cold = await run();
  const xs = [];
  for (let i = 0; i < calls; i++) xs.push(await run());
  xs.sort((a, b) => a - b);
  console.log([name, xs[xs.length >> 1], xs[Math.floor(xs.length * 0.9)], cold].join("\t"));
}
await time("web_search", { query: "go news" });
await time("web_fetch", { url: "https://example.com/p" });

// The card layout: the original's renderResult component for an expanded web_fetch result of 20 lines, rendered at
// 80 columns (bench_test.go's BenchmarkRenderLayout times the same text).
const theme = { fg: (name, text) => `\x1b[38;5;33m${text}\x1b[39m`, bold: (text) => `\x1b[1m${text}\x1b[22m` };
const lines = Array.from({ length: 20 }, (_, i) => `line ${i + 1} of the fetched page, long enough to take a good part of the row it is on`).join("\n");
const result = { content: [{ type: "text", text: lines }], details: { title: "Bench" } };
const render = () => tools.get("web_fetch").renderResult(result, { expanded: true }, theme, undefined).render(80);
{
  const t0 = process.hrtime.bigint();
  render();
  const cold = Number(process.hrtime.bigint() - t0) / 1e3;
  const xs = [];
  for (let i = 0; i < calls; i++) {
    const s = process.hrtime.bigint();
    render();
    xs.push(Number(process.hrtime.bigint() - s) / 1e3);
  }
  xs.sort((a, b) => a - b);
  console.log(["render_layout", xs[xs.length >> 1], xs[Math.floor(xs.length * 0.9)], cold].join("\t"));
}
