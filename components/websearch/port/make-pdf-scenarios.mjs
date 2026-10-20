// Writes the PDF scenarios (port/scenarios/fetch-pdf-*.json). The fake server serves the
// documents from testdata/pdf as text, so only fixtures that are pure ASCII can be used (the
// scenario server's route body is a JSON string).
//
//   node port/make-pdf-scenarios.mjs
//
// Run it from components/websearch after port/make-pdf-fixtures.mjs.
import { readFileSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const fixture = (name) => {
	const text = readFileSync(join(here, "..", "extensions", "websearch", "testdata", "pdf", name), "latin1");
	if (/[^\x00-\x7f]/.test(text)) throw new Error(`${name} is not ASCII`);
	return text;
};

const normalize = [
	{ pattern: '(responseId[\\": ]+)[0-9a-z]{9,14}\\b', replacement: "${1}<response-id>" },
	{ pattern: "^[m-o][0-9a-z]{8,13}$", replacement: "<response-id>" },
	// The directory the extracted Markdown is saved in follows the lane's TMPDIR.
	{ pattern: '(saved to: )[^"\\\\ ]+?/(pi-web-pdf/)', replacement: "${1}<tmp>/${2}" },
];

function scenario(name, description, { config, route, datalab }) {
	const web = { routes: [route] };
	const servers = { web };
	const agentConfig = { toolActivation: "eager", ssrf: { allowRanges: ["127.0.0.0/8"] }, pdf: { provider: "unpdf" }, ...config };
	const s = {
		name,
		description,
		agentFiles: { "web-search.json": JSON.stringify(agentConfig) },
		llm: [{ toolCalls: [{ name: "fetch_content", arguments: { url: "{{server:web}}/docs/report.pdf" } }] }, { text: "done" }],
		steps: [{ name: "ask", rpc: { type: "prompt", message: "research this" } }],
		servers,
		normalize,
	};
	if (datalab) {
		servers.datalab = datalab;
		s.env = { DATALAB_API_BASE: "{{server:datalab}}/api/v1" };
	}
	writeFileSync(join(here, "scenarios", `${name}.json`), JSON.stringify(s, null, " ") + "\n");
}

const pdfRoute = (body, extra = {}) => ({ path: "/docs/report.pdf", headers: { "content-type": "application/pdf" }, body, ...extra });

scenario("fetch-pdf-readable", "fetch_content extracts a three-page PDF with metadata, saves the Markdown and reports the path, page and character counts.", {
	route: pdfRoute(fixture("multipage.pdf")),
});
scenario("fetch-pdf-disabled", "pdf.enabled false stops a PDF fetch before the body is read.", {
	config: { pdf: { enabled: false, provider: "unpdf" } },
	route: pdfRoute(fixture("multipage.pdf")),
});
scenario("fetch-pdf-datalab-fallback", "With a Datalab key the conversion is tried first: the upload request is recorded, its rejection falls back to local extraction.", {
	config: { pdf: { provider: "auto" }, datalabApiKey: "datalab-test-key" },
	route: pdfRoute(fixture("multipage.pdf")),
	datalab: {
		recordHeaders: ["x-api-key"],
		routes: [{ method: "POST", path: "/api/v1/files/upload", status: 401, json: { detail: "invalid api key" } }],
	},
});
