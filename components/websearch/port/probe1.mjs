import { createRequire } from "node:module";
const require = createRequire(new URL("./oracle/package.json", import.meta.url));
const { parseHTML } = require("linkedom");
const show = (label, v) => console.log(label + ": " + JSON.stringify(v));
let { document, window } = parseHTML(`<!doctype html><html lang="en"><head><title>T &amp; t</title><meta charset="utf-8"></head><body class="A b" id=x><p>one<p>two &nbsp; &copy; &lt; <b>bold</b><br>x<img SRC="a.png" srcSet="b 1w" alt='he said "hi"' data-x=1 hidden><table><tr><td>c</td></tr></table><ul><li>a<li>b</ul><!-- c --><textarea>&amp;x</textarea><template><i>t</i></template><svg viewBox="0 0 1 1"><path d="M0"/></svg><input checked disabled value=""><a href="/x?a=1&b=2">l</a><script type="application/ld+json">{"a":"&amp;"}</script><noscript><img src=n.png></noscript></body></html>`);
show("outer", document.documentElement.outerHTML);
show("body.className", document.body.className);
show("defaultView", [typeof document.defaultView, document.defaultView === window, typeof (document.defaultView||{}).getComputedStyle, typeof document.styleSheets]);
show("img attrs", [...document.querySelector("img").attributes].map(a => [a.name, a.value]));
show("getAttribute srcSet", [document.querySelector("img").getAttribute("srcSet"), document.querySelector("img").getAttribute("srcset"), document.querySelector("img").getAttribute("SRC")]);
show("tagName", [document.querySelector("img").tagName, document.querySelector("svg").tagName, document.querySelector("path").tagName, document.querySelector("svg").namespaceURI, document.body.namespaceURI]);
const ta = document.querySelector("textarea"); show("textarea", [ta.value, ta.textContent, ta.innerHTML]);
const tpl = document.querySelector("template"); show("template", [tpl.innerHTML, tpl.content && tpl.content.childNodes.length, tpl.childNodes.length]);
show("noscript", [document.querySelector("noscript").innerHTML, document.querySelector("noscript").childNodes.length, document.querySelector("noscript").querySelector("img") !== null]);
show("script text", document.querySelector("script").textContent);
show("title", document.title);
show("closest", [document.querySelector("path").closest("svg") !== null, document.querySelector("b").closest("p").outerHTML]);
for (const sel of [":scope > img", "p:not(.x)", "[class*='A']", "[class*='a' i]", "p, b", "img[hidden]", "table > tbody", "table > tr", "ul > li:first-child", "td:empty", "svg *", "a[href^='/x']", "[CLASS]", "BODY", "#x", ".A.b", "body > *:nth-child(2)", "[href*=\"#\"]", "div[role=\"paragraph\"]", ":is(p,b)", "p:has(b)", "ul li + li", "p ~ p"]) {
  try { show("sel " + sel, [...document.querySelectorAll(sel)].map(e => e.tagName)); } catch (e) { show("sel " + sel + " ERR", e.message); }
}
for (const sel of ["#S:a", "a:foo", "[", "p..x", ">", "", "div:hover"]) {
  try { show("sel " + sel, [...document.querySelectorAll(sel)].map(e => e.tagName)); } catch (e) { show("sel " + sel + " ERR", [e.name, e.message]); }
}
const p = document.querySelector("p");
show("p.next", [p.nextSibling && p.nextSibling.nodeName, document.body.children.length, [...document.body.childNodes].map(n=>n.nodeName)]);
show("style prop", [typeof p.style, p.getAttribute("style")]);
p.setAttribute("style", "display:none; color: red"); show("style after", [p.style && p.style.display, p.style && p.style.cssText, p.getAttribute("style")]);
const d2 = parseHTML("<div>hello</div>").document; show("frag doc", [d2.documentElement && d2.documentElement.tagName, d2.body && d2.body.tagName, d2.head && d2.head.tagName, d2.documentElement.outerHTML]);
const d3 = parseHTML("").document; show("empty doc", [!!d3.documentElement, !!d3.body, d3.documentElement && d3.documentElement.outerHTML]);
const d4 = parseHTML("<p>only p").document; show("p only", [d4.documentElement && d4.documentElement.outerHTML, !!d4.head]);
const d5 = parseHTML("<title>x</title><p>p").document; show("title p", [d5.documentElement && d5.documentElement.outerHTML]);
show("cloneNode", document.cloneNode(true).documentElement.outerHTML.length === document.documentElement.outerHTML.length);
show("textContent on el w/ comment", document.body.textContent.slice(0, 60));
show("innerHTML nbsp", [document.querySelectorAll("p")[1].innerHTML]);
show("URL", [document.URL, typeof document.location]);
show("root", [document.documentElement.nodeName, document.firstChild && document.firstChild.nodeName, [...document.childNodes].map(n=>n.nodeName)]);
