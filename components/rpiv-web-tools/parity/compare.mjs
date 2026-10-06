// SPDX-License-Identifier: MIT

// The differential parity generator: it runs the oracle's own TypeScript and writes the results the Go side reads.
//
//	pigeq parity html   (or: node parity/compare.mjs > parity/html.json)
//
// Fixtures live in parity/fixtures.json so both sides see the same bytes: the oracle's output is the expectation,
// exactly the way pigeq twins check treats the upstream test titles.
import { readFileSync, writeFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';
import { htmlToText, extractTitle, isHtmlContentType, assertTextContentType } from '../port/oracle/providers/fetch-helpers.ts';

const here = dirname(fileURLToPath(import.meta.url));
const fixtures = JSON.parse(readFileSync(join(here, 'fixtures.json'), 'utf8'));

const textCases = [];
for (const f of fixtures.html) {
	textCases.push({ name: f.name, got: htmlToText(f.html) });
}

const titleCases = [];
for (const f of fixtures.html) {
	titleCases.push({ name: f.name, got: extractTitle(f.html) ?? null });
}

const typeCases = [];
for (const f of fixtures.contentTypes) {
	let threw = null;
	try {
		assertTextContentType(f);
	} catch (err) {
		threw = err.message;
	}
	typeCases.push({ name: f, got: threw, isHtml: isHtmlContentType(f) });
}

const out = { htmlToText: textCases, extractTitle: titleCases, contentTypes: typeCases };
writeFileSync(join(here, 'expectations.json'), JSON.stringify(out, null, 1) + '\n');
process.stdout.write(`wrote expectations.json: ${textCases.length} text, ${titleCases.length} title, ${typeCases.length} type\n`);
