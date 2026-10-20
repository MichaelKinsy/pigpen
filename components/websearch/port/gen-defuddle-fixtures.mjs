// Writes the hand-made fixtures of the Defuddle port into extensions/websearch/testdata/defuddle (prefix port--):
// pages that exercise one stage of the pipeline each (removals, scoring, standardization, footnotes, code blocks,
// callouts, malformed markup, large pages, invalid bytes, empty pages). The expected output is not written here: the
// original's Defuddle makes it (node port/record-defuddle.mjs).
//
//   node port/gen-defuddle-fixtures.mjs
import { writeFileSync } from "node:fs";

const out = new URL("../extensions/websearch/testdata/defuddle/", import.meta.url);
const put = (name, body) => writeFileSync(new URL(`port--${name}.html`, out), body);
const words = "the quick brown fox jumps over a lazy dog while seven wizards quietly box jumpers near river banks and old mills grind fine grain under steady rain".split(" ");
function lorem(n, seed = 0) {
	const w = [];
	for (let i = 0; i < n; i++) w.push(words[(i * 7 + seed * 3 + i * i) % words.length]);
	const s = w.join(" ");
	return s[0].toUpperCase() + s.slice(1) + ".";
}
const page = (title, body, head = "") =>
	`<!DOCTYPE html>\n<html lang="en"><head><meta charset="utf-8"><title>${title}</title>${head}</head><body>${body}</body></html>\n`;
const article = (inner, title = "Sample article") =>
	page(title, `<header><nav><a href="/">Home</a> <a href="/about">About</a></nav></header><main><article><h1>${title}</h1>${inner}</article></main><footer><p>Copyright 2026 Example Corp</p></footer>`);
const body = (k = 4) => Array.from({ length: k }, (_, i) => `<p>${lorem(45, i)} ${lorem(30, i + 5)}</p>`).join("\n");

// removals
put("removals-hidden", article(`${body(2)}<div style="display:none"><p>Display none text that must go.</p></div><div hidden><p>Hidden attribute text.</p></div><p aria-hidden="true">Aria hidden text.</p><div style="visibility: hidden"><p>Visibility hidden text.</p></div><p style="opacity:0">Opacity zero text.</p><div class="hidden"><p>Class hidden text.</p></div><div class="sr-only">Screen reader only</div>${body(2)}`));
put("removals-cookie-ads-newsletter", page("Removals", `<div id="cookie-banner" class="cookie-consent"><p>We use cookies. <button>Accept</button></p></div><div class="advertisement"><p>Buy now!</p></div><aside class="sidebar"><ul><li><a href="/a">Popular A</a></li><li><a href="/b">Popular B</a></li></ul></aside><main><article><h1>Removals</h1>${body(4)}<div class="newsletter-signup"><h3>Subscribe</h3><form><input type="email" placeholder="you@example.com"><button>Sign up</button></form></div><div class="share-buttons"><a href="https://twitter.com/share">Tweet</a><a href="https://facebook.com/sharer">Share</a></div>${body(2)}<div class="related-posts"><h3>Related posts</h3><ul><li><a href="/r1">One</a></li><li><a href="/r2">Two</a></li></ul></div></article></main><footer>Footer text</footer>`));
put("removals-nav-header-footer", page("Nav", `<header class="site-header"><a class="logo" href="/">Site</a><nav class="main-nav"><ul><li><a href="/1">One</a></li><li><a href="/2">Two</a></li><li><a href="/3">Three</a></li></ul></nav></header><div class="content"><h1>Nav page</h1>${body(5)}</div><nav class="breadcrumbs"><a href="/">Home</a> / <a href="/x">X</a></nav><footer class="site-footer"><ul><li><a href="/privacy">Privacy</a></li><li><a href="/terms">Terms</a></li></ul><p>All rights reserved.</p></footer>`));
put("removals-comments-section", article(`${body(3)}<section id="comments" class="comments"><h2>Comments</h2><div class="comment"><p>First!</p></div><div class="comment"><p>Nice post.</p></div></section><div id="disqus_thread"></div>`));
put("removals-scripts-styles-forms", article(`${body(2)}<script>var x = "script text";</script><style>.a{color:red}</style><noscript><p>Enable JavaScript</p></noscript><form action="/s"><label>Search <input name="q"></label><select><option>One</option></select><textarea>text</textarea><button>Go</button></form><iframe src="https://ads.example.com/frame"></iframe><iframe src="https://www.youtube.com/embed/dQw4w9WgXcQ"></iframe><link rel="stylesheet" href="/x.css"><meta name="x" content="y">${body(2)}`));
put("removals-class-patterns-in-prose", article(`<p>${lorem(40)}</p><div class="social-share-box"><span>12 shares</span></div><div class="widget-promo"><p>Promo text of a widget.</p></div><div class="post-meta"><span>By Jane Doe</span> <time datetime="2026-01-02">Jan 2, 2026</time></div><div class="tags"><a href="/t/a">tag-a</a> <a href="/t/b">tag-b</a></div>${body(3)}`));
put("removals-small-images-icons", article(`${body(2)}<p><img src="/icons/logo.svg" width="16" height="16" alt="icon"><img src="/pixel.gif" width="1" height="1" alt=""><img src="/photo.jpg" width="800" height="600" alt="Photo"></p>${body(2)}`));

// scoring
put("scoring-two-candidates", page("Scoring", `<div id="sidebar-like"><p>${lorem(20)}</p><p>${lorem(22, 2)}</p></div><div id="story"><h1>The story</h1>${body(6)}</div><div id="other"><p>${lorem(30, 3)}</p></div>`));
put("scoring-link-density", page("Link density", `<div class="links"><a href="/1">${lorem(5)}</a> <a href="/2">${lorem(6)}</a> <a href="/3">${lorem(7)}</a> <a href="/4">${lorem(8)}</a></div><div class="text"><h1>Text</h1>${body(4)}</div>`));
put("scoring-nested-divs", page("Nested", `<div><div><div><div><div class="inner"><h1>Deep</h1>${body(5)}</div></div></div></div></div><div><p>${lorem(12)}</p></div>`));
put("scoring-main-vs-article", page("Main", `<main><p>${lorem(15)}</p><article><h1>Inside main</h1>${body(4)}</article><p>${lorem(15, 4)}</p></main>`));
put("scoring-role-main", page("Role", `<div role="main"><h1>Role main</h1>${body(4)}</div><div role="complementary"><p>${lorem(40, 2)}</p></div>`));
put("scoring-table-layout", page("Table layout", `<table width="100%"><tr><td width="20%"><a href="/a">Menu A</a><br><a href="/b">Menu B</a></td><td><h1>Table layout page</h1>${body(5)}</td></tr></table>`));
put("scoring-many-short-blocks", page("Short blocks", `<div class="stream">${Array.from({ length: 30 }, (_, i) => `<div class="item"><p>${lorem(6, i)}</p></div>`).join("")}</div>`));
put("scoring-multiple-articles", page("Multiple", `<article><h2>First</h2>${body(2)}</article><article><h2>Second</h2>${body(3)}</article><article><h2>Third</h2>${body(1)}</article>`));
put("scoring-itemprop-articlebody", page("Schema", `<div itemscope itemtype="https://schema.org/Article"><h1 itemprop="headline">Schema headline</h1><div itemprop="articleBody">${body(4)}</div><div class="aside"><p>${lorem(30, 6)}</p></div></div>`));
put("scoring-post-content-class", page("Post", `<div class="wrap"><div class="entry-content">${body(4)}</div><div class="sidebar-widget"><p>${lorem(25, 3)}</p></div></div>`));

// standardize
put("standardize-headings", article(`<h1>Sample article</h1><p>${lorem(30)}</p><h3>Skipped level</h3><p>${lorem(30, 1)}</p><h2><a href="#anchor" class="anchor">#</a>Heading with permalink</h2><p>${lorem(30, 2)}</p><h2>Heading <span class="x">with</span> <em>inline</em></h2><p>${lorem(30, 3)}</p><h4>Deep</h4><p>${lorem(30, 4)}</p><h6>Six</h6><p>${lorem(20)}</p>`));
put("standardize-images", article(`${body(1)}<figure><img src="/a.jpg" alt="A"><figcaption>Caption <em>A</em></figcaption></figure><picture><source srcset="/b.webp" type="image/webp"><img src="/b.jpg" alt="B"></picture><img data-src="/lazy.jpg" src="data:image/gif;base64,R0lGODlhAQABAAAAACw=" alt="Lazy"><img srcset="/s1.jpg 400w, /s2.jpg 800w, /s3.jpg 1600w" alt="Responsive"><img src="//cdn.example.com/p.png" alt="Protocol relative"><img src="rel/c.png" alt="Relative">${body(2)}`));
put("standardize-lists", article(`<ul><li>One</li><li>Two<ul><li>Two A</li><li>Two B<ol><li>Deep</li></ol></li></ul></li></ul><ol start="5"><li><p>Paragraph item</p><p>Second paragraph</p></li><li>Plain</li></ol><dl><dt>Term</dt><dd>Definition</dd></dl>${body(2)}<ul><li><a href="/x">Linked</a></li><li><strong>Bold</strong> item</li></ul>`));
put("standardize-tables", article(`${body(1)}<table><caption>Cap</caption><thead><tr><th>A</th><th>B</th></tr></thead><tbody><tr><td>1</td><td>2</td></tr><tr><td colspan="2">span</td></tr></tbody></table><table><tr><td>layout only</td></tr></table><table><tr><th>H</th></tr><tr><td><p>cell para</p><ul><li>cell li</li></ul></td></tr></table>${body(1)}`));
put("standardize-inline-formatting", article(`<p>${lorem(15)} <b>bold</b> <i>italic</i> <u>under</u> <s>strike</s> <del>deleted</del> <ins>inserted</ins> <mark>marked</mark> <kbd>Ctrl</kbd> <sub>2</sub><sup>3</sup> <abbr title="Abbr">AB</abbr> <q>quoted</q> <small>small</small> <code>code</code> <var>v</var> <cite>cite</cite> <time datetime="2026-02-03">Feb 3</time>.</p>${body(3)}`));
put("standardize-links", article(`<p><a href="/rel">relative</a> <a href="../up">up</a> <a href="#frag">fragment</a> <a href="mailto:a@b.c">mail</a> <a href="tel:+123">tel</a> <a href="javascript:void(0)">js</a> <a>no href</a> <a href="">empty</a> <a href="https://example.com/a b">space</a> <a href="https://example.com/?a=1&amp;b=2" title="T">query</a> <a href="/img"><img src="/i.png" alt="img link"></a></p>${body(3)}`));
put("standardize-attributes", article(`<p class="lead" id="p1" style="color:red" data-x="1" onclick="alert(1)">${lorem(30)}</p><div class="a b c" data-reactid="5"><p lang="fr" dir="rtl" title="t">${lorem(20, 2)}</p></div>${body(3)}`));
put("standardize-spans-divs", article(`<div><div><p>${lorem(20)}</p></div></div><div><span>${lorem(10)}</span><span> more </span><span>text</span></div><span><span><span>nested spans ${lorem(8)}</span></span></span>${body(3)}`));
put("standardize-br-paragraphs", article(`<div>${lorem(15)}<br><br>${lorem(14, 1)}<br>${lorem(13, 2)}<br><br><br>${lorem(12, 3)}</div>${body(3)}`));
put("standardize-blockquote-cite", article(`<blockquote cite="https://example.com/q"><p>${lorem(20)}</p><footer>Someone <cite>Book</cite></footer></blockquote><blockquote><blockquote><p>Nested ${lorem(8)}</p></blockquote><p>Outer</p></blockquote>${body(3)}`));
put("standardize-entities", article(`<p>&amp; &lt; &gt; &quot; &#39; &nbsp; &copy; &eacute; &#x1F600; &mdash; &hellip; &unknown; &amp;amp;</p><p>Less than 1 &lt; 2 and a &amp; b: ${lorem(20)}</p>${body(3)}`));
put("standardize-iframes-video-audio", article(`${body(1)}<video src="/v.mp4" controls poster="/p.jpg"></video><audio src="/a.mp3" controls></audio><iframe src="https://player.vimeo.com/video/123"></iframe><iframe src="https://www.youtube-nocookie.com/embed/abc"></iframe><embed src="/x.swf"><object data="/o"></object>${body(2)}`));
put("standardize-pre-text", article(`${body(1)}<pre>plain preformatted
  text   with   spaces
	and a tab</pre>${body(2)}`));
put("standardize-details-summary", article(`${body(1)}<details open><summary>More</summary><p>${lorem(20)}</p></details><details><summary>Closed</summary><p>${lorem(10)}</p></details>${body(2)}`));
put("standardize-figure-without-caption", article(`${body(1)}<figure><div><img src="/a.jpg" alt=""></div></figure><figure class="wp-block-image"><img src="/b.jpg" alt="B"><figcaption></figcaption></figure>${body(2)}`));

// footnotes
put("footnotes-wikipedia-like", article(`<p>${lorem(20)}<sup id="cite_ref-1" class="reference"><a href="#cite_note-1">[1]</a></sup> ${lorem(20, 1)}<sup id="cite_ref-2" class="reference"><a href="#cite_note-2">[2]</a></sup>.</p>${body(2)}<h2>References</h2><ol class="references"><li id="cite_note-1"><span class="mw-cite-backlink"><a href="#cite_ref-1">^</a></span> <span class="reference-text">First reference text.</span></li><li id="cite_note-2"><span class="mw-cite-backlink"><a href="#cite_ref-2">^</a></span> <span class="reference-text">Second reference with <a href="https://example.com/ref">a link</a>.</span></li></ol>`));
put("footnotes-pandoc", article(`<p>${lorem(20)}<a href="#fn1" class="footnote-ref" id="fnref1" role="doc-noteref"><sup>1</sup></a> ${lorem(10, 2)}<a href="#fn2" class="footnote-ref" id="fnref2" role="doc-noteref"><sup>2</sup></a></p>${body(2)}<section class="footnotes" role="doc-endnotes"><hr><ol><li id="fn1"><p>First note.<a href="#fnref1" class="footnote-back" role="doc-backlink">↩︎</a></p></li><li id="fn2"><p>Second note.<a href="#fnref2" class="footnote-back" role="doc-backlink">↩︎</a></p></li></ol></section>`));
put("footnotes-same-note-twice", article(`<p>${lorem(12)}<sup><a href="#n1" id="r1">1</a></sup> and again${lorem(5)}<sup><a href="#n1" id="r1b">1</a></sup>.</p>${body(2)}<div class="footnotes"><ol><li id="n1">Shared note <a href="#r1">back</a> <a href="#r1b">back2</a></li></ol></div>`));
put("footnotes-non-sequential", article(`<p>${lorem(12)}<sup><a href="#n3" id="r3">3</a></sup> ${lorem(5)}<sup><a href="#n1" id="r1">1</a></sup></p>${body(2)}<ol class="footnotes"><li id="n1">One <a href="#r1">↩</a></li><li id="n3">Three <a href="#r3">↩</a></li></ol>`));
put("footnotes-missing-definition", article(`<p>${lorem(12)}<sup><a href="#nowhere" id="r9">9</a></sup> ${lorem(8)}</p>${body(3)}<ol class="footnotes"><li id="n1">Lonely <a href="#r1">↩</a></li></ol>`));
put("footnotes-bracket-numbers-not-notes", article(`<p>See [1] and [2] for details: ${lorem(20)} The array a[3] is used.</p>${body(3)}`));
put("footnotes-ghost-style", article(`<p>${lorem(15)}<sup id="fnref:1"><a href="#fn:1" rel="footnote">1</a></sup></p>${body(2)}<div class="footnotes"><hr><ol><li class="footnote" id="fn:1"><p>Ghost note. <a href="#fnref:1" class="reversefootnote" rev="footnote">&#8617;</a></p></li></ol></div>`));
put("footnotes-sidenotes", article(`<p>${lorem(15)}<label for="sn1" class="margin-toggle sidenote-number"></label><input type="checkbox" id="sn1" class="margin-toggle"><span class="sidenote">A sidenote text.</span> ${lorem(10, 3)}</p>${body(3)}`));
put("footnotes-multiple-paragraph-note", article(`<p>${lorem(12)}<sup><a href="#fn1" id="fr1">[1]</a></sup></p>${body(2)}<div class="footnotes"><ol><li id="fn1"><p>First paragraph of note.</p><p>Second paragraph of note.</p><ul><li>list in note</li></ul></li></ol></div>`));
put("footnotes-endnotes-definition-list", article(`<p>${lorem(12)}<sup><a href="#e1" id="b1">1</a></sup></p>${body(2)}<h2>Notes</h2><dl><dt id="e1"><a href="#b1">1</a></dt><dd>Definition list note.</dd></dl>`));

// code blocks
const code = (lang, text, cls) => `<pre class="${cls ?? ""}"><code class="${lang ? `language-${lang}` : ""}">${text}</code></pre>`;
put("code-languages", article(`${body(1)}${code("js", "const a = 1;\nconsole.log(a &lt; 2);")}${code("python", "def f(x):\n    return x * 2\n")}<pre data-lang="rust"><code>fn main() {}</code></pre><div class="highlight highlight-source-go"><pre>package main</pre></div><pre><code class="lang-sh">$ echo hi</code></pre><pre class="brush: java">class A {}</pre><pre><code class="hljs language-json">{"a": [1, 2]}</code></pre>${body(1)}`));
put("code-inline-and-block", article(`<p>Use <code>foo()</code> and <code>a &lt; b</code> inline; run <code>x`+"`"+`y</code> too. ${lorem(15)}</p>${code("c", "int main(void) { return 0; }")}${body(2)}`));
put("code-whitespace-preserved", article(`${body(1)}<pre><code>line1\n\n\n    indented\n\ttabbed\ntrailing   \n</code></pre>${body(1)}`));
put("code-line-numbers-table", article(`${body(1)}<table class="highlighttable"><tr><td class="linenos"><pre>1\n2\n3</pre></td><td class="code"><pre>a = 1\nb = 2\nc = 3</pre></td></tr></table>${body(2)}`));
put("code-pre-without-code", article(`${body(1)}<pre>no code element here\n  second line</pre><pre><span class="k">def</span> <span class="n">f</span>():\n    <span class="k">pass</span></pre>${body(1)}`));
put("code-backticks-in-code", article(`${body(1)}<pre><code>echo \`date\`\n\`\`\`fenced\`\`\`</code></pre><p>Inline <code>\`\`</code> and <code>a\`b</code>.</p>${body(1)}`));
put("code-nested-in-list", article(`<ol><li>Step one:<pre><code>npm install</code></pre></li><li>Step two: <code>npm test</code></li></ol>${body(3)}`));
put("code-html-entities-and-tags", article(`${body(1)}<pre><code class="language-html">&lt;div class="a"&gt;\n  &lt;p&gt;Hi &amp;amp; bye&lt;/p&gt;\n&lt;/div&gt;</code></pre>${body(1)}`));
put("code-copy-button-and-filename", article(`${body(1)}<div class="code-block"><div class="code-header"><span class="filename">app.js</span><button class="copy-button">Copy</button></div><pre><code class="language-js">let x = 1;</code></pre></div>${body(2)}`));
put("code-prism-tokens", article(`${body(1)}<pre class="language-javascript"><code class="language-javascript"><span class="token keyword">const</span> <span class="token function">f</span> <span class="token operator">=</span> <span class="token punctuation">(</span><span class="token punctuation">)</span> <span class="token operator">=&gt;</span> <span class="token number">1</span><span class="token punctuation">;</span></code></pre>${body(1)}`));
put("code-empty-and-blank", article(`${body(1)}<pre><code></code></pre><pre><code>   </code></pre><pre></pre>${body(2)}`));

// callouts
put("callouts-github-alerts", article(`${body(1)}<div class="markdown-alert markdown-alert-note"><p class="markdown-alert-title">Note</p><p>${lorem(12)}</p></div><blockquote><p>[!WARNING]<br>${lorem(8)}</p></blockquote><blockquote><p>[!TIP] Short tip</p></blockquote>${body(2)}`));
put("callouts-docusaurus", article(`${body(1)}<div class="theme-admonition theme-admonition-tip admonition_xJq3 alert alert--success"><div class="admonitionHeading_Gvgb"><span class="admonitionIcon_Rf37"><svg viewBox="0 0 12 16"><path d="M6.5"/></svg></span>tip</div><div class="admonitionContent_BuS1"><p>${lorem(14)}</p></div></div>${body(2)}`));
put("callouts-mkdocs", article(`${body(1)}<div class="admonition warning"><p class="admonition-title">Careful</p><p>${lorem(14)}</p></div><details class="note"><summary>Collapsible</summary><p>${lorem(9)}</p></details>${body(2)}`));
put("callouts-aside-and-role-note", article(`${body(1)}<aside class="callout"><p><strong>Heads up:</strong> ${lorem(10)}</p></aside><div role="note"><p>${lorem(10, 3)}</p></div>${body(2)}`));
put("callouts-obsidian-nested-and-folded", article(`${body(1)}<div data-callout="info" class="callout"><div class="callout-title"><div class="callout-title-inner">Info</div></div><div class="callout-content"><p>${lorem(10)}</p><div data-callout="warning" class="callout"><div class="callout-title"><div class="callout-title-inner">Inner</div></div><div class="callout-content"><p>Nested.</p></div></div></div></div><div data-callout="faq" data-callout-fold="-" class="callout is-collapsible is-collapsed"><div class="callout-title"><div class="callout-title-inner">FAQ</div></div><div class="callout-content"><p>${lorem(7)}</p></div></div>${body(2)}`));
put("callouts-bootstrap-and-notice", article(`${body(1)}<div class="alert alert-danger" role="alert"><strong>Error!</strong> ${lorem(8)}</div><p class="notice warning">${lorem(10)}</p><div class="notice-box"><span class="notice-title">Notice</span><p>${lorem(6)}</p></div>${body(2)}`));

// malformed
put("malformed-unclosed-tags", page("Unclosed", `<div><p>${lorem(20)}<p>${lorem(20, 1)}<ul><li>one<li>two<li>three</ul><b>bold<i>both</b>italic</i><div><span>x`));
put("malformed-stray-end-tags", page("Stray", `</div></p></span><div><p>${lorem(30)}</p></div></table></ul><p>${lorem(30, 2)}</p></body></html></html>`));
put("malformed-bad-entities", article(`<p>&amp &lt &#; &#x; &#99999999; &#xD800; &#0; &#128; &#x80; ${lorem(20)} &notit; &copy2 &AMP;</p>${body(3)}`));
put("malformed-bad-attributes", article(`<p class="a" class="b" id=unquoted data-x='single "double"' =noname "quoted-only" a=b=c>${lorem(30)}</p><img src=x.png alt=A&B width=>${body(3)}`));
put("malformed-control-characters", article(`<p>a\u0000b\u0001c\u007fd\u0085e\u2028f\u2029g\ufeffh ${lorem(30)}</p>${body(3)}`));
put("malformed-deep-nesting", page("Deep", `${"<div>".repeat(400)}<p>${lorem(40)}</p>${"</div>".repeat(400)}`));
put("malformed-table-fragments", page("Table", `<td>cell without table</td><tr><td>row</td></tr><table><td>a<td>b<tr><td>c</table><p>${lorem(40)}</p>${body(2)}`));
put("malformed-unterminated-comment", page("Comment", `<p>${lorem(30)}</p><!-- unterminated comment <p>${lorem(30, 1)}</p>`));
put("malformed-unterminated-script", page("Script", `<p>${lorem(30)}</p><script>var a = "<p>not content</p>";`));
put("malformed-no-html-body", `<p>${lorem(40)}</p><p>${lorem(40, 1)}</p><h2>Heading</h2><p>${lorem(30, 2)}</p>`);
put("malformed-multiple-body-html", `<html lang="en"><body><p>${lorem(30)}</p></body><body class="second"><p>${lorem(30, 1)}</p></body></html><html><body><p>${lorem(30, 2)}</p></body></html>`);
put("malformed-cdata-and-pi", article(`<![CDATA[ not really ]]><?xml version="1.0"?><p>${lorem(30)}</p><!DOCTYPE junk>${body(2)}`));
put("malformed-nested-forms-and-anchors", article(`<a href="/a"><a href="/b">nested anchors ${lorem(6)}</a></a><form><form><input></form></form><p><div>div in p ${lorem(10)}</div></p>${body(3)}`));
put("malformed-svg-math-foreign", article(`<p>${lorem(10)}</p><svg><p>html in svg</p><foreignObject><p>x</p></foreignObject></svg><math><mi>x</mi><p>html in math</p></math>${body(3)}`));
put("malformed-huge-attribute", article(`<p data-blob="${"x".repeat(100000)}">${lorem(30)}</p>${body(3)}`));

// large pages
const big = (n, f) => Array.from({ length: n }, (_, i) => f(i)).join("\n");
put("large-many-paragraphs", article(big(1500, (i) => `<p>${lorem(40, i)} ${lorem(20, i + 1)}</p>`)));
put("large-many-sections", article(big(200, (i) => `<h2>Section ${i}</h2><p>${lorem(50, i)}</p><ul><li>${lorem(6, i)}</li><li>${lorem(7, i)}</li></ul>`)));
put("large-table-1000-rows", article(`<table><thead><tr><th>N</th><th>Text</th></tr></thead><tbody>${big(1000, (i) => `<tr><td>${i}</td><td>${lorem(6, i)}</td></tr>`)}</tbody></table>${body(2)}`));
put("large-many-links", article(`${body(1)}<p>${big(3000, (i) => `<a href="/p/${i}">link ${i}</a>`)}</p>${body(2)}`));
put("large-deep-lists", article(`${"<ul><li>level".repeat(60)}${"</li></ul>".repeat(60)}${body(3)}`));
put("large-many-footnotes", article(`<p>${big(150, (i) => `${lorem(8, i)}<sup id="r${i}"><a href="#n${i}">${i + 1}</a></sup>`)}</p><ol class="footnotes">${big(150, (i) => `<li id="n${i}">Note ${i} <a href="#r${i}">↩</a></li>`)}</ol>`));
put("large-many-code-blocks", article(big(300, (i) => `<p>${lorem(12, i)}</p><pre><code class="language-js">let v${i} = ${i};\nconsole.log(v${i});</code></pre>`)));

// invalid bytes, encodings, BOM
const bytes = (parts) => Buffer.concat(parts.map((p) => (typeof p === "string" ? Buffer.from(p, "utf8") : Buffer.from(p))));
const nonUtf8 = (name, parts) => writeFileSync(new URL(`port--${name}.html`, out), bytes(parts));
nonUtf8("nonutf8-invalid-bytes", [`<!DOCTYPE html><html><head><meta charset="utf-8"><title>Bytes `, [0xff, 0xfe], `</title></head><body><article><h1>Invalid bytes</h1><p>`, lorem(20), " caf", [0xc3], " and ", [0xc3, 0x28], " and ", [0xe2, 0x82], " and ", [0xf0, 0x9f, 0x98], " end. ", lorem(30, 1), "</p>", body(3), "</article></body></html>"]);
nonUtf8("nonutf8-latin1-declared", [`<!DOCTYPE html><html><head><meta http-equiv="Content-Type" content="text/html; charset=iso-8859-1"><title>Latin `, [0xe9], `</title></head><body><article><h1>Latin-1 page</h1><p>caf`, [0xe9], " na", [0xef], "ve ", [0x93], "quoted", [0x94], " ", lorem(30), "</p>", body(3), "</article></body></html>"]);
nonUtf8("nonutf8-bom", [[0xef, 0xbb, 0xbf], article(body(4), "BOM page")]);
nonUtf8("nonutf8-utf16-bytes", [Buffer.from("\ufeff" + article(body(2), "UTF-16 page"), "utf16le")]);
nonUtf8("nonutf8-surrogate-encoded", [`<!DOCTYPE html><html><body><article><h1>Surrogates</h1><p>`, [0xed, 0xa0, 0x80], " ", [0xed, 0xb0, 0x80], " ", lorem(30), "</p>", body(3), "</article></body></html>"]);
put("unicode-astral-and-combining", article(`<p>😀 𝒳 e\u0301 \u200d\u200b العربية 日本語のテキスト ${lorem(30)}</p>${body(3)}`, "Unicode 😀"));

// empty and near-empty pages
put("empty-file", "");
put("empty-whitespace", " \n\t \n");
put("empty-doctype-only", "<!DOCTYPE html>");
put("empty-head-only", `<!DOCTYPE html><html><head><title>Only a title</title></head></html>`);
put("empty-body", `<!DOCTYPE html><html><head><title>Empty body</title></head><body></body></html>`);
put("empty-comment-only", "<!-- nothing here -->");
put("empty-article-no-text", page("No text", `<article><div><span></span></div><img src="/x.png" alt=""></article>`));
put("empty-plain-text-no-tags", "Just some plain text without any markup at all, long enough to be a sentence. " + lorem(30));
put("empty-only-scripts", page("Scripts only", `<script>window.app = {}</script><script src="/app.js"></script><noscript>Please enable JavaScript</noscript><div id="root"></div>`));
put("empty-title-with-meta", `<!DOCTYPE html><html><head><title></title><meta property="og:title" content="OG title"><meta name="description" content="A description"><meta name="author" content="Author Name"></head><body><p>${lorem(30)}</p></body></html>`);
put("short-single-line", page("Short", `<p>Just one short line.</p>`));
put("url-with-query-and-port", `<!-- {"url": "https://example.com:8443/path/page?x=1&y=2#frag"} -->\n` + article(`<p><a href="rel">relative</a> <img src="img.png" alt="i"></p>${body(3)}`));
put("url-base-element", `<!-- {"url": "https://example.com/dir/page"} -->\n` + page("Base", `<p><a href="x">x</a><img src="y.png" alt="y"></p>${body(3)}`, `<base href="https://cdn.example.org/base/">`));

// the pages of the four test/rsc-fallback.test.mjs cases that reach Defuddle
const rep = (s, n) => s.repeat(n);
put("rsc-fallback-null-readability", `<!doctype html><html><head><title>Defuddle article</title></head><body><aside><main>${rep("Useful local Defuddle fallback content with enough detail. ", 20)}</main></aside></body></html>`);
put("rsc-fallback-relative-canonical", `<!doctype html><html><head><title>Relative canonical</title><link rel="canonical" href="/relative-canonical"></head><body><aside><main>${rep("Defuddle content with a relative canonical URL stays quiet. ", 20)}</main></aside></body></html>`);
put("rsc-fallback-short-readability", `<!doctype html><html><head><title>Short article fallback</title></head><body><article>Loading...</article><aside><main>${rep("Useful Defuddle content after the short Readability article. ", 20)}</main></aside></body></html>`);
put("rsc-fallback-selector-failure", `<!doctype html><html><head><title>Defuddle selector failure</title></head><body><nav><a href="/">Home</a></nav><template id="B:0"></template><div hidden id="S:a"><h1>Defuddle selector failure</h1><p>${rep("This is substantial article text that should be extracted as the page's main content. ", 20)}</p></div><template id="P:a"></template><footer><p>Copyright 2026 Example Inc. All rights reserved.</p></footer></body></html>`);
