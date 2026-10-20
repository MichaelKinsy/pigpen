// Records what the original's Defuddle fallback makes of each HTML fixture of extensions/websearch/testdata/defuddle:
// the call of oracle/extract.ts extractWithDefuddle (a linkedom document, Defuddle 0.19.3 with
// { markdown: true, useAsync: false }, the console.error capture that turns "Error processing document" into a
// thrown error), so the expected title, Markdown and error are the original's own. The Go twin (defuddle_corpus_test.go)
// runs the port on the same files and compares byte for byte. The files of testdata/html (the Readability fixtures) are
// recorded as <name>.defuddle.json.
//
// extractWithDefuddle is not exported by extract.ts, so its body is repeated below, unchanged except for the
// import paths (resolved from the vendored original's node_modules).
//
// Run in the container, from components/websearch:
//   node port/record-defuddle.mjs            # rewrites every testdata/defuddle/*.expected.json
//   node port/record-defuddle.mjs name ...   # only these fixtures
//
// A fixture <name>.html takes its URL from a leading comment <!-- {"url": "..."} --> and otherwise from its name,
// the way the corpus of the Defuddle project itself does: https:// plus the name without its "kind--" prefix.
import { createRequire } from "node:module";
import { readdir, readFile, writeFile } from "node:fs/promises";
import { basename } from "node:path";

const require = createRequire(new URL("./oracle/package.json", import.meta.url));
const root = new URL("../extensions/websearch/testdata/defuddle/", import.meta.url);

function errorMessage(err) {
	return err instanceof Error ? err.message : String(err);
}

function isDefuddleConsoleError(args) {
	const prefix = args[0];
	return prefix === "Defuddle" || (typeof prefix === "string" && /^Defuddle(?:\s|:)/.test(prefix));
}

async function extractWithDefuddle(text, url) {
	const { Defuddle } = await import(new URL("./oracle/node_modules/defuddle/dist/node.js", import.meta.url).href);
	const { parseHTML } = require("linkedom");
	const { document } = parseHTML(text);
	Object.defineProperty(document, "location", {
		value: new URL(url),
		configurable: true,
	});
	let processingError;
	const originalConsoleError = console.error;
	console.error = (...args) => {
		if (isDefuddleConsoleError(args)) {
			if (args[0] === "Defuddle" && args[1] === "Error processing document:") {
				processingError = args[2];
			}
			return;
		}
		originalConsoleError(...args);
	};

	let resultPromise;
	try {
		resultPromise = Defuddle(document, url, { markdown: true, useAsync: false });
	} finally {
		console.error = originalConsoleError;
	}

	const result = await resultPromise;
	if (processingError !== undefined) {
		throw new Error(`Defuddle failed to process document: ${errorMessage(processingError)}`);
	}
	return typeof result.content === "string" ? { title: result.title, content: result.content } : null;
}

// The two corpora: the Defuddle fixtures (<name>.expected.json) and the HTML fixtures of the Readability pipeline
// (testdata/html, <name>.defuddle.json, served from http://fixtures.test/<name>). The text is decoded as
// readTextResponseWithLimit does for a response without a charset: TextDecoder("utf-8"), which drops a BOM and
// replaces each maximal invalid subpart with U+FFFD.
const corpora = [
	{ root, suffix: ".expected.json", urlOf: (name, html) => {
		const m = html.match(/<!--\s*(\{"url":.*?\})\s*-->/);
		return (m ? JSON.parse(m[1]).url : undefined) || `https://${name.replace(/^[a-z]+--/, "")}`;
	} },
	{ root: new URL("../extensions/websearch/testdata/html/", import.meta.url), suffix: ".defuddle.json", urlOf: (name) => `http://fixtures.test/${name}` },
];
const only = process.argv.slice(2);
const warn = console.warn;
console.warn = () => {};
const decoder = new TextDecoder("utf-8");
for (const corpus of corpora) {
	const names = (await readdir(corpus.root)).filter((f) => f.endsWith(".html")).map((f) => f.slice(0, -5)).sort();
	for (const name of only.length ? names.filter((n) => only.includes(n)) : names) {
		const html = decoder.decode(await readFile(new URL(`${name}.html`, corpus.root)));
		const url = corpus.urlOf(name, html);
		let expected;
		try {
			const r = await extractWithDefuddle(html, url);
			expected = { url, title: r ? r.title : null, content: r ? r.content : null, error: null };
		} catch (err) {
			expected = { url, title: null, content: null, error: errorMessage(err) };
		}
		await writeFile(new URL(`${name}${corpus.suffix}`, corpus.root), JSON.stringify(expected, null, 1) + "\n");
		console.log(`${name}: ${expected.error ? "ERROR " + expected.error : `title=${JSON.stringify(expected.title)} content=${expected.content.length}`}`);
	}
}
console.warn = warn;
process.exit(0);
