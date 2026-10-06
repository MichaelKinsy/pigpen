// Writes extensions/rpiv-web-tools/testdata/differential.json: inputs run through the ORIGINAL's htmlToText and extractTitle
// (port/oracle/providers/fetch-helpers.ts) and Pi's truncateHead and formatSize, for the Go port's differential test.
// Run with Node 24 from the Package root: node port/gen-differential.mjs <pi-coding-agent dist dir>
import { writeFileSync } from "node:fs";
import { htmlToText, extractTitle } from "./oracle/providers/fetch-helpers.ts";
const piDist = process.argv[2];
const { truncateHead, formatSize } = await import(`${piDist}/core/tools/truncate.js`);

const html = [
  "<html><head><title>  A &amp; B </title></head><body><p>Hello</p><p>World</p></body></html>",
  "<script>var x = '<p>no</p>';</script><p>kept</p><STYLE>p{color:red}</STYLE><noscript>nope</noscript>text",
  "<div>a</div><div>b</div><br><br/>c<BR />d<li>one</li><li>two</li><tr><td>x</td></tr>",
  "&amp;lt; &lt;b&gt; &quot;q&quot; &#39;s&#39; &nbsp;gap &#65;&#8364;&#128512; &#x41; &unknown;",
  "line one\n\n\n\n\nline   two\t\t tabbed\n   indented",
  "<h1>Title</h1>\n<p>para</p>\n\n\n<pre>code\n  block</pre>",
  "<title>First</title><title>Second</title>",
  "<title></title><p>empty title</p>",
  "<a href=\"x\">link</a> and <img src=\"y\"> and <!-- comment --> done",
  "   \n  leading and trailing  \n   ",
  "",
  "plain text only",
  "<p>unclosed <b>bold",
  "<TITLE>Upper</TITLE><P>Upper case</P>",
  "<section><article><header>H</header><footer>F</footer><nav>N</nav><details>D</details><summary>S</summary></article></section>",
  "caf\u00e9 \u00fc\u00f1\u00ee \u4e2d\u6587 \ud83d\ude00",
];
const bodies = [
  "", "one", "a\nb\nc", "a\nb\nc\n", "x".repeat(60_000), "line\n".repeat(2500), "l".repeat(51_300) + "\nsecond",
  "\u00e9".repeat(30_000), ("word ".repeat(10) + "\n").repeat(1200), "a\n".repeat(2000), "a\n".repeat(2001),
];
const out = {
  html: html.map((h) => ({ in: h, text: htmlToText(h), title: extractTitle(h) ?? null })),
  truncate: bodies.map((b) => {
    const t = truncateHead(b, { maxLines: 2000, maxBytes: 50 * 1024 });
    return { in: b, t };
  }),
  sizes: [0, 1, 1023, 1024, 1536, 10240, 1048575, 1048576, 5242880, 123456789].map((n) => ({ n, s: formatSize(n) })),
};
writeFileSync(new URL("../extensions/rpiv-web-tools/testdata/differential.json", import.meta.url), JSON.stringify(out));
console.log(out.html.length, "html cases,", out.truncate.length, "truncation cases,", out.sizes.length, "sizes");
