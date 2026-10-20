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
