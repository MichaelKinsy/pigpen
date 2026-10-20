// scratch: records linkedom's parse/serialize/selector results for the Defuddle corpus
import { createRequire } from "node:module";
import { readdirSync, readFileSync, writeFileSync } from "node:fs";
const require = createRequire(new URL("./oracle/package.json", import.meta.url));
const { parseHTML } = require("linkedom");
const c = require(new URL("./oracle/node_modules/defuddle/dist/constants.js", import.meta.url).pathname);
const dir = new URL("../extensions/websearch/testdata/defuddle/", import.meta.url);
const sels = [...c.EXACT_SELECTORS, ...c.ENTRY_POINT_ELEMENTS, ...c.FOOTNOTE_INLINE_REFERENCES.split(","), ...c.FOOTNOTE_LIST_SELECTORS.split(","), c.HIDDEN_EXACT_SELECTOR, c.TEST_ATTRIBUTES_SELECTOR, c.CONTENT_ELEMENT_SELECTOR,
 "span:has(img)", "video:not([src]):not(:has(source))", "header:not(:has(p + p)):not(:has(img))", "p:last-of-type", "table > tbody > tr", "table > tr", "ul > li:first-child", "li:nth-child(2n+1)", "img[srcset]", "img[srcSet]", "[class~=post]", "[lang|=en]", "a[href^='http' i]", "a:not([href])", "p:empty", ":root", "div > p + p", "h1 ~ p", "*"];
let out = {};
for (const f of readdirSync(dir).filter(f => f.endsWith(".html")).sort()) {
  const html = readFileSync(new URL(f, dir), "utf8");
  const { document } = parseHTML(html);
  const all = [...document.querySelectorAll("*")];
  const idx = new Map(all.map((e, i) => [e, i]));
  const r = { html: document.documentElement ? document.documentElement.outerHTML : null, sel: {} };
  for (const s of sels) {
    try { r.sel[s] = [...document.querySelectorAll(s)].map(e => idx.get(e)); } catch (e) { r.sel[s] = "ERR " + e.message; }
  }
  out[f] = r;
}
writeFileSync(new URL("../extensions/websearch/testdata/ldom-scratch.json", import.meta.url), JSON.stringify(out));
console.log("recorded", Object.keys(out).length);
