// Writes extensions/rpiv-web-tools/testdata/render-differential.json: the original's renderCall and renderResult
// (port/oracle/web-tools.ts) run on a set of calls and results, the returned pi-tui Text component rendered with
// Pi's own render(width) at several widths. The Go port's lines must equal them (render_differential_test.go).
// Run with Node 24 from the Package root, JITI naming jiti's entry as bench-tools.mjs does:
//   JITI=<jiti/lib/jiti.mjs> node port/gen-render-differential.mjs
import { writeFileSync } from "node:fs";

const { createJiti } = await import(process.env.JITI ?? "jiti");
const registerWebTools = await createJiti(import.meta.url, { moduleCache: false }).import(new URL("./oracle/index.ts", import.meta.url).pathname, { default: true });
const { Text } = await createJiti(new URL("./oracle/index.ts", import.meta.url).href, { moduleCache: false }).import("@earendil-works/pi-tui");
const tools = new Map();
registerWebTools({ registerTool: (t) => tools.set(t.name, t), registerCommand() {}, on() {}, registerShortcut() {} });

// A theme that colors like Pi's: every fg and bold call wraps its text in SGR sequences (the code is per color name).
const colors = { toolTitle: 33, accent: 36, dim: 90, warning: 33, success: 32, muted: 37 };
const theme = {
  fg: (name, text) => `\x1b[38;5;${colors[name] ?? 39}m${text}\x1b[39m`,
  bold: (text) => `\x1b[1m${text}\x1b[22m`,
};

const widths = [1, 2, 3, 5, 8, 16, 33, 60, 120];
const longWord = "x".repeat(70);
const results = (n, title = (i) => `Result number ${i}`) => Array.from({ length: n }, (_, i) => ({ title: title(i + 1), url: `https://x/${i}`, snippet: "" }));
const lines = (n) => Array.from({ length: n }, (_, i) => `line ${i + 1}`).join("\n");

const cases = [
  { tool: "web_search", phase: "call", args: { query: "ai news" } },
  { tool: "web_search", phase: "call", args: { query: "ai news", provider: "tavily" } },
  { tool: "web_search", phase: "call", args: { query: "a long query with many words that has to wrap at the narrow widths of a terminal" } },
  { tool: "web_search", phase: "call", args: { query: longWord } },
  { tool: "web_search", phase: "call", args: { query: "tab\tseparated  and   spaced" } },
  { tool: "web_search", phase: "call", args: { query: "caf\u00e9 \u4e2d\u6587\u6d4b\u8bd5 \ud83d\ude00 mixed width" } },
  { tool: "web_search", phase: "call", args: { query: "family \ud83d\udc68\u200d\ud83d\udc69\u200d\ud83d\udc67 flag \ud83c\uddef\ud83c\uddf5 heart \u2764\ufe0f thumbs \ud83d\udc4d\ud83c\udffd e\u0301 caf\u00e9 \u03a9" } },
  { tool: "web_search", phase: "call", args: { query: "\u65e5\u672c\u8a9e\u306e\u30c6\u30ad\u30b9\u30c8\u3068\ud55c\uad6d\uc5b4 mixed \u4e2d\u6587" } },
  { tool: "web_search", phase: "call", args: { query: "" } },
  { tool: "web_search", phase: "call", args: { query: "   " } },
  { tool: "web_search", phase: "call", args: { query: "two\nlines" } },
  { tool: "web_search", phase: "result", options: { isPartial: true }, result: { content: [], details: {} } },
  { tool: "web_search", phase: "result", options: {}, result: { content: [], details: { resultCount: 0, results: [] } } },
  { tool: "web_search", phase: "result", options: {}, result: { content: [], details: { resultCount: 1, results: results(1) } } },
  { tool: "web_search", phase: "result", options: { expanded: false }, result: { content: [], details: { resultCount: 3, results: results(3) } } },
  { tool: "web_search", phase: "result", options: { expanded: true }, result: { content: [], details: { resultCount: 4, results: results(4) } } },
  { tool: "web_search", phase: "result", options: { expanded: true }, result: { content: [], details: { resultCount: 7, results: results(7) } } },
  { tool: "web_search", phase: "result", options: { expanded: true }, result: { content: [], details: { resultCount: 2, results: results(2, () => "A title long enough to wrap " + longWord) } } },
  { tool: "web_search", phase: "result", options: { expanded: true }, result: { content: [], details: { resultCount: 2, results: results(2, (i) => `\t  tabbed \u4e2d\u6587 ${i}`) } } },
  { tool: "web_search", phase: "result", options: { expanded: true }, result: { content: [], details: { resultCount: 2, results: [{ title: "", url: "", snippet: "" }, { title: "  ", url: "", snippet: "" }] } } },
  { tool: "web_fetch", phase: "call", args: { url: "https://example.com/page" } },
  { tool: "web_fetch", phase: "call", args: { url: "https://example.com/" + "segment/".repeat(12) } },
  { tool: "web_fetch", phase: "call", args: { url: "https://example.com/" + longWord } },
  { tool: "web_fetch", phase: "result", options: { isPartial: true }, result: { content: [], details: {} } },
  { tool: "web_fetch", phase: "result", options: {}, result: { content: [{ type: "text", text: "body" }], details: { url: "https://x" } } },
  { tool: "web_fetch", phase: "result", options: {}, result: { content: [{ type: "text", text: "body" }], details: { url: "https://x", title: "My Page" } } },
  { tool: "web_fetch", phase: "result", options: {}, result: { content: [{ type: "text", text: "body" }], details: { url: "https://x", title: "A page title that is long enough to wrap at narrow widths", truncation: { truncated: true } } } },
  { tool: "web_fetch", phase: "result", options: { expanded: true }, result: { content: [{ type: "text", text: lines(10) }], details: { url: "https://x" } } },
  { tool: "web_fetch", phase: "result", options: { expanded: true }, result: { content: [{ type: "text", text: lines(20) }], details: { url: "https://x", title: "T" } } },
  { tool: "web_fetch", phase: "result", options: { expanded: true }, result: { content: [{ type: "text", text: "tab\tindented\n\n    spaces here\n" + longWord + "\ncaf\u00e9 \u4e2d\u6587 \ud83d\ude00\n\n\nend" }], details: { url: "https://x" } } },
  { tool: "web_fetch", phase: "result", options: { expanded: true }, result: { content: [{ type: "text", text: "\ud83d\udc68\u200d\ud83d\udc69\u200d\ud83d\udc67 \ud83c\uddef\ud83c\uddf5 \u2764\ufe0f \ud83d\udc4d\ud83c\udffd e\u0301\n\u65e5\u672c\u8a9e\u30c6\u30ad\u30b9\u30c8 wide \ud55c\uad6d\uc5b4" }], details: { url: "https://x" } } },
  { tool: "web_fetch", phase: "result", options: { expanded: true }, result: { content: [{ type: "text", text: "" }], details: { url: "https://x" } } },
  { tool: "web_fetch", phase: "result", options: { expanded: true }, result: { content: [{ type: "image", data: "", mimeType: "image/png" }], details: { url: "https://x" } } },
  { tool: "web_fetch", phase: "result", options: { expanded: true }, result: { content: [], details: undefined } },
];

// Generated text: a fixed-seed mix of words, space and tab runs, newlines, CJK, emoji sequences, combining marks and
// escape sequences, as a query (call) and as page content (expanded result).
let seed = 12345;
const rand = (n) => { seed = (seed * 1103515245 + 12345) & 0x7fffffff; return seed % n; };
const pieces = ["word", "longer-word-here", "x".repeat(25), " ", "  ", "   ", "\t", "\n", "\n\n", "caf\u00e9", "e\u0301", "\u4e2d\u6587", "\u65e5\u672c\u8a9e",
  "\ud55c\uad6d\uc5b4", "\ud83d\ude00", "\ud83d\udc68\u200d\ud83d\udc69\u200d\ud83d\udc67", "\ud83c\uddef\ud83c\uddf5", "\u2764\ufe0f", "\ud83d\udc4d\ud83c\udffd", "\u03a9", "\u00a0",
  "\x1b[31mred\x1b[39m", "\x1b[1mbold\x1b[22m", "\x1b[4munder\x1b[24m", "\x1b[7minv", "\x1b[0m", "https://example.com/a/b/c?d=e", "a-b_c.d", "!?,;"];
for (let i = 0; i < 40; i++) {
  let text = "";
  for (let k = 1 + rand(14); k > 0; k--) text += pieces[rand(pieces.length)];
  cases.push({ tool: "web_search", phase: "call", args: { query: text } });
  cases.push({ tool: "web_fetch", phase: "result", options: { expanded: true }, result: { content: [{ type: "text", text }], details: { url: "https://x" } } });
}

// Text itself, for the inputs no renderer produces: blank text, tabs, empty lines.
const plain = ["", "   ", "\t\n ", " x ", "tab\there", "a\n\nb", "  leading and trailing  ", "one\r\ntwo"];

const out = [...cases.map((c) => {
  const tool = tools.get(c.tool);
  const component = c.phase === "call"
    ? tool.renderCall(c.args, theme, undefined)
    : tool.renderResult(c.result, c.options, theme, undefined);
  return { ...c, rendered: widths.map((w) => ({ width: w, lines: component.render(w) })) };
}), ...plain.map((text) => ({ tool: "text", text, rendered: widths.map((w) => ({ width: w, lines: new Text(text, 0, 0).render(w) })) }))];
writeFileSync(new URL("../extensions/rpiv-web-tools/testdata/render-differential.json", import.meta.url), JSON.stringify(out));
console.log(out.length, "cases,", widths.length, "widths");
