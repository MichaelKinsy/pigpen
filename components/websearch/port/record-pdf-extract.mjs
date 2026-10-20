// Records what the ORIGINAL pdf-extract.ts (unpdf, PDF.js) makes of every fixture in
// extensions/websearch/testdata/pdf/, for the Go twins and the recorded differences
// (extensions/websearch/pdf_extract_test.go, docs in port/PORT.md "Deliberate differences").
//
//   node port/record-pdf-extract.mjs > extensions/websearch/testdata/pdf-unpdf.json
//
// Run it from components/websearch with the oracle's dependencies installed (Node 24, which strips
// the TypeScript types itself). The provider is pinned to unpdf and no API key is set, so the
// run is the deterministic local path.
import { mkdtempSync, readdirSync, readFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { basename, dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const fixtures = join(here, "..", "extensions", "websearch", "testdata", "pdf");
const agentDir = mkdtempSync(join(tmpdir(), "pdf-record-"));
process.env.PI_CODING_AGENT_DIR = agentDir;
for (const k of ["GEMINI_API_KEY", "DATALAB_API_KEY", "GOOGLE_GEMINI_BASE_URL", "CLOUDFLARE_API_KEY", "DATALAB_MODE"]) delete process.env[k];
const { writeFileSync } = await import("node:fs");
writeFileSync(join(agentDir, "web-search.json"), JSON.stringify({ pdf: { provider: "unpdf" } }));
const { extractPDFToMarkdown } = await import(new URL(`file://${join(here, "oracle", "pdf-extract.ts")}`).href);

const result = { pdfs: {}, urls: {} };
for (const file of readdirSync(fixtures).filter((f) => f.endsWith(".pdf")).sort()) {
	const bytes = readFileSync(join(fixtures, file));
	const buffer = bytes.buffer.slice(bytes.byteOffset, bytes.byteOffset + bytes.byteLength);
	const outputDir = mkdtempSync(join(tmpdir(), "pdf-record-out-"));
	const name = basename(file, ".pdf");
	try {
		const r = await extractPDFToMarkdown(buffer, `https://example.test/docs/${file}`, { outputDir });
		result.pdfs[file] = { title: r.title, pages: r.pages, chars: r.chars, file: basename(r.outputPath), content: r.content };
	} catch (err) {
		result.pdfs[file] = { error: err instanceof Error ? err.message : String(err), name: err?.name };
	}
}
// The title and file name the original derives from a URL when the document has no title.
const plain = readFileSync(join(fixtures, "text.pdf"));
const plainBuffer = plain.buffer.slice(plain.byteOffset, plain.byteOffset + plain.byteLength);
for (const url of [
	"https://example.com/My_Report-final.pdf",
	"https://example.com/",
	"https://example.com/.pdf",
	"https://example.com/a/b/",
	"https://example.com/docs/Q3%20Results.PDF",
	"https://arxiv.org/pdf/2401.12345v2",
	"https://arxiv.org/abs/2401.12345",
	"https://www.arxiv.org/pdf/hep-th/9901001",
	"https://example.com/___.pdf",
	"https://example.com/%E4%B8%AD%E6%96%87.pdf",
	"https://example.com/download?id=7",
	"https://example.com/report.pdf?x=1#frag",
	"not a url",
]) {
	const outputDir = mkdtempSync(join(tmpdir(), "pdf-record-out-"));
	const r = await extractPDFToMarkdown(plainBuffer.slice(0), url, { outputDir });
	result.urls[url] = { title: r.title, file: basename(r.outputPath) };
}
console.log(JSON.stringify(result, null, "\t"));
