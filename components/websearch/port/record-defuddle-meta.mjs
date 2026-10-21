// Records the metadata the original's Defuddle 0.19.3 makes of the HTML fixtures of the extractors
// (testdata/defuddle/ext--*.html): what the extractor puts in the variables that port/record-defuddle.mjs does not
// keep (author, published, description, site, language, image, extractorType, word count). The Go twin
// (TestDefuddleExtractorMetadataEqualsTheOriginal) runs the port on the same files and compares each field.
//
// Run in the container, from components/websearch, under TZ=UTC:
//   TZ=UTC node port/record-defuddle-meta.mjs name ...   # writes testdata/defuddle/<name>.meta.json
import { createRequire } from "node:module";
import { readFile, writeFile } from "node:fs/promises";

const require = createRequire(new URL("./oracle/package.json", import.meta.url));
const root = new URL("../extensions/websearch/testdata/defuddle/", import.meta.url);
const { Defuddle } = await import(new URL("./oracle/node_modules/defuddle/dist/node.js", import.meta.url).href);
const { parseHTML } = require("linkedom");

console.warn = () => {};
const originalConsoleError = console.error;
for (const name of process.argv.slice(2)) {
	const html = await readFile(new URL(`${name}.html`, root), "utf8");
	const url = JSON.parse(html.match(/<!--\s*(\{"url":.*?\})\s*-->/)[1]).url;
	const { document } = parseHTML(html);
	Object.defineProperty(document, "location", { value: new URL(url), configurable: true });
	console.error = (...args) => {
		if (typeof args[0] === "string" && /^Defuddle(?:\s|:)|^Defuddle$/.test(args[0])) return;
		originalConsoleError(...args);
	};
	let r;
	try {
		r = await Defuddle(document, url, { markdown: true, useAsync: false });
	} finally {
		console.error = originalConsoleError;
	}
	const meta = {};
	for (const k of ["author", "published", "description", "site", "language", "image", "extractorType", "wordCount"]) {
		meta[k] = r[k] ?? null;
	}
	await writeFile(new URL(`${name}.meta.json`, root), JSON.stringify(meta, null, 1) + "\n");
}
