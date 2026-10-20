// Records what the original's Markdown stage makes of HTML snippets: Defuddle 0.19.3 dist/markdown.js
// createMarkdownContent(html, url), the last step of the pipeline. A page run through the whole pipeline reaches only
// the markup that standardization leaves (it strips every class but a handful, collapses code blocks, and so on); the
// snippets here reach what the pages cannot, such as the task list classes. The Go twin
// (TestDefuddleMarkdownStageEqualsTheOriginal) runs dfdCreateMarkdownContent on the same snippets and compares byte
// for byte.
//
// Run in the container, from components/websearch:
//   node port/record-defuddle-markdown.mjs      # rewrites extensions/websearch/testdata/defuddle-markdown.json
import { createRequire } from "node:module";
import { writeFile } from "node:fs/promises";

const require = createRequire(new URL("./oracle/package.json", import.meta.url));
const { createMarkdownContent } = require(new URL("./oracle/node_modules/defuddle/dist/markdown.js", import.meta.url).pathname);

const url = "https://example.com/post";
const cases = {
	"task-list-checked-and-open": `<ul><li class="task-list-item"><input type="checkbox" checked="checked" disabled> Done</li><li class="task-list-item"><input type="checkbox" disabled> Todo</li><li class="task-list-item"><input type="checkbox" checked> Boolean checked attribute</li><li class="task-list-item"><input type="checkbox" checked="true"> Checked is true</li></ul>`,
	"task-list-nested": `<ul class="contains-task-list"><li class="task-list-item"><input type="checkbox" checked="checked"> Parent<ul><li class="task-list-item"><input type="checkbox"> Child</li></ul></li><li>Plain item</li></ul>`,
	"task-list-checkbox-without-class": `<ul><li><input type="checkbox" checked> No class</li></ul>`,
	"code-block-blank-line-run": `<pre><code>first\n\n\n\nsecond\n\n\nthird</code></pre>`,
	"code-block-blank-lines-between-paragraphs": `<p>One</p><pre><code>a\n\n\n\n\nb</code></pre><p>Two</p>`,
	"inline-code-newline-run": `<p>Text <code>a\n\n\n\nb</code> after.</p>`,
	"paragraph-with-empty-paragraphs": `<p>One</p><p></p><p></p><p></p><p>Two</p>`,
	"leading-no-break-space": `<p>&nbsp;Starts with a no-break space</p><p>Second</p>`,
	"leading-em-space": `<p>\u2003Starts with an em space</p>`,
	"leading-bom": `<p>\ufeffStarts with a byte order mark</p>`,
	"leading-spaces-then-text": `<p>   Starts with spaces</p>`,
	"trailing-no-break-space": `<p>Ends with a no-break space&nbsp;</p>`,
	"trailing-break": `<p>Ends with a break<br></p>`,
	"leading-h1-removed": `<h1>Title</h1><p>Body</p>`,
	"block-math-ampersand": `<math display="block" data-latex="x &amp;= y"><mi>x</mi></math>`,
	"block-math-ampersand-in-text": `<p>Before.</p><math display="block" data-latex="a &amp;= b + c"></math><p>After.</p>`,
	"block-math-line-break": `<math display="block" data-latex="a \\\\ b"><mi>a</mi></math>`,
	"block-math-plain": `<math display="block" data-latex="x + y"><mi>x</mi></math>`,
	"block-math-environment": `<math display="block" data-latex="\\begin{matrix} a &amp; b \\end{matrix}"><mi>a</mi></math>`,
	"inline-math-ampersand": `<p>Text <math data-latex="a &amp;= b">x</math> more text.</p>`,
};

const out = [];
for (const [name, html] of Object.entries(cases)) {
	let markdown;
	let error = null;
	try {
		markdown = createMarkdownContent(html, url);
	} catch (err) {
		markdown = null;
		error = err instanceof Error ? err.message : String(err);
	}
	out.push({ name, html, url, markdown, error });
}
await writeFile(new URL("../extensions/websearch/testdata/defuddle-markdown.json", import.meta.url), JSON.stringify(out, null, 1) + "\n");
console.log(`${out.length} snippets`);
