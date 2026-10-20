// Generates the PDF fixtures of the PDF text extraction twins (extensions/websearch/testdata/pdf/).
// Every file is written by hand with no PDF library, so the corpus is reproducible and small:
//
//   node port/make-pdf-fixtures.mjs
//
// Run it from components/websearch. The output is deterministic (fixed IVs, no timestamps). The
// original's extraction of every fixture is recorded by port/record-pdf-extract.mjs.
import { createHash, createCipheriv } from "node:crypto";
import { mkdirSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { deflateSync } from "node:zlib";

const out = join(dirname(fileURLToPath(import.meta.url)), "..", "extensions", "websearch", "testdata", "pdf");
mkdirSync(out, { recursive: true });

const md5 = (...parts) => createHash("md5").update(Buffer.concat(parts.map((p) => Buffer.from(p)))).digest();
const PAD = Buffer.from("28BF4E5E4E758A4164004E56FFFA01082E2E00B6D0683E802F0CA9FE6453697A", "hex");

function rc4(key, data) {
	const s = Array.from({ length: 256 }, (_, i) => i);
	for (let i = 0, j = 0; i < 256; i++) {
		j = (j + s[i] + key[i % key.length]) & 255;
		[s[i], s[j]] = [s[j], s[i]];
	}
	const res = Buffer.alloc(data.length);
	for (let k = 0, i = 0, j = 0; k < data.length; k++) {
		i = (i + 1) & 255;
		j = (j + s[i]) & 255;
		[s[i], s[j]] = [s[j], s[i]];
		res[k] = data[k] ^ s[(s[i] + s[j]) & 255];
	}
	return res;
}

const padPassword = (pw) => Buffer.concat([Buffer.from(pw, "latin1"), PAD]).subarray(0, 32);
const hexOf = (buf) => `<${Buffer.from(buf).toString("hex")}>`;
const ID0 = Buffer.from("0123456789abcdef0123456789abcdef", "hex");

// Standard security handler. kind "rc4-40" is V1/R2, kind "aes-128" is V4/R4 (AESV2).
function makeEncryption(kind, userPassword = "") {
	const P = -4;
	const pBytes = Buffer.alloc(4);
	pBytes.writeInt32LE(P);
	const ownerPad = padPassword("owner-secret");
	const userPad = padPassword(userPassword);
	if (kind === "rc4-40") {
		const O = rc4(md5(ownerPad).subarray(0, 5), userPad);
		const key = md5(userPad, O, pBytes, ID0).subarray(0, 5);
		const U = rc4(key, PAD);
		return {
			dict: `<< /Filter /Standard /V 1 /R 2 /O ${hexOf(O)} /U ${hexOf(U)} /P ${P} >>`,
			encrypt(n, g, data) {
				const k = md5(key, [n & 255, (n >> 8) & 255, (n >> 16) & 255, g & 255, (g >> 8) & 255]).subarray(0, Math.min(key.length + 5, 16));
				return rc4(k, data);
			},
		};
	}
	let h = md5(ownerPad);
	for (let i = 0; i < 50; i++) h = md5(h);
	const ownerKey = h.subarray(0, 16);
	let O = rc4(ownerKey, userPad);
	for (let i = 1; i <= 19; i++) O = rc4(Buffer.from(ownerKey.map((b) => b ^ i)), O);
	let key = md5(userPad, O, pBytes, ID0);
	for (let i = 0; i < 50; i++) key = md5(key.subarray(0, 16));
	key = key.subarray(0, 16);
	let U = rc4(key, md5(PAD, ID0));
	for (let i = 1; i <= 19; i++) U = rc4(Buffer.from(key.map((b) => b ^ i)), U);
	U = Buffer.concat([U, Buffer.alloc(16)]);
	return {
		dict: `<< /Filter /Standard /V 4 /R 4 /Length 128 /CF << /StdCF << /CFM /AESV2 /AuthEvent /DocOpen /Length 16 >> >> /StmF /StdCF /StrF /StdCF /O ${hexOf(O)} /U ${hexOf(U)} /P ${P} >>`,
		encrypt(n, g, data) {
			const k = md5(key, [n & 255, (n >> 8) & 255, (n >> 16) & 255, g & 255, (g >> 8) & 255], "sAlT");
			const iv = Buffer.alloc(16, n & 255);
			const c = createCipheriv("aes-128-cbc", k, iv);
			return Buffer.concat([iv, c.update(data), c.final()]);
		},
	};
}

// A PDF is a list of object builders. Each receives {n, str, stream} and returns a Buffer or a
// string; str() writes a literal text string (hex, UTF-16BE when it is not Latin-1) that is
// encrypted when the document is.
function buildPdf(builders, { root = 1, info = 0, encrypt = null, header = "%PDF-1.7\n", trailer = true } = {}) {
	const parts = [Buffer.from(header, "latin1")];
	let size = parts[0].length;
	const offsets = [];
	const total = builders.length + (encrypt ? 1 : 0);
	const all = [...builders];
	if (encrypt) all.push(() => encrypt.dict);
	all.forEach((builder, index) => {
		const n = index + 1;
		const isEncryptDict = encrypt && n === total;
		const ctx = {
			n,
			str(text) {
				const bytes = /^[\x00-\xFF]*$/.test(text) ? Buffer.from(text, "latin1") : Buffer.concat([Buffer.from([0xfe, 0xff]), Buffer.from(text, "utf16le").swap16()]);
				return hexOf(encrypt && !isEncryptDict ? encrypt.encrypt(n, 0, bytes) : bytes);
			},
			stream(dict, data, { flate = false } = {}) {
				let payload = Buffer.from(data);
				if (flate) payload = deflateSync(payload, { level: 9 });
				if (encrypt && !isEncryptDict) payload = encrypt.encrypt(n, 0, payload);
				return Buffer.concat([Buffer.from(`<< ${dict} ${flate ? "/Filter /FlateDecode " : ""}/Length ${payload.length} >>\nstream\n`, "latin1"), payload, Buffer.from("\nendstream")]);
			},
		};
		const body = builder(ctx);
		offsets.push(size);
		const chunk = Buffer.concat([Buffer.from(`${n} 0 obj\n`), Buffer.isBuffer(body) ? body : Buffer.from(body, "latin1"), Buffer.from("\nendobj\n")]);
		parts.push(chunk);
		size += chunk.length;
	});
	if (!trailer) return Buffer.concat(parts);
	const xref = size;
	let tail = `xref\n0 ${all.length + 1}\n0000000000 65535 f \n`;
	for (const o of offsets) tail += `${String(o).padStart(10, "0")} 00000 n \n`;
	tail += `trailer\n<< /Size ${all.length + 1} /Root ${root} 0 R${info ? ` /Info ${info} 0 R` : ""}${encrypt ? ` /Encrypt ${total} 0 R` : ""} /ID [${hexOf(ID0)} ${hexOf(ID0)}] >>\nstartxref\n${xref}\n%%EOF\n`;
	parts.push(Buffer.from(tail));
	return Buffer.concat(parts);
}

const esc = (s) => s.replace(/[\\()]/g, "\\$&");
const winAnsi = (s) =>
	[...s]
		.map((ch) => {
			const code = ch.codePointAt(0);
			const special = { "\u20ac": 0x80, "\u2014": 0x97, "\u2013": 0x96, "\u201c": 0x93, "\u201d": 0x94, "\u2018": 0x91, "\u2019": 0x92, "\u2022": 0x95 }[ch];
			const byte = special ?? code;
			return byte < 32 || byte > 126 ? `\\${byte.toString(8).padStart(3, "0")}` : esc(ch);
		})
		.join("");

const helvetica = "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica /Encoding /WinAnsiEncoding >>";

// Document layouts. pages: array of content-stream strings; the font is /F1 unless fonts is given.
function simpleDoc(pages, { title, author, encrypt, flate = false, fonts, extra = "" } = {}) {
	const first = 3;
	const fontNum = first + pages.length * 2;
	const fontEntries = fonts ? Object.keys(fonts) : ["F1"];
	const fontObjs = fonts ? Object.values(fonts) : [() => helvetica];
	const builders = [
		() => "<< /Type /Catalog /Pages 2 0 R >>",
		() => `<< /Type /Pages /Kids [${pages.map((_, i) => `${first + i * 2} 0 R`).join(" ")}] /Count ${pages.length} >>`,
	];
	pages.forEach((content, i) => {
		builders.push(() => `<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << ${fontEntries.map((f, k) => `/${f} ${fontNum + k} 0 R`).join(" ")} >> >> /Contents ${first + i * 2 + 1} 0 R ${extra}>>`);
		builders.push((c) => c.stream("", content, { flate }));
	});
	for (const f of fontObjs) builders.push(f);
	if (title !== undefined || author !== undefined) {
		builders.push((c) => `<< ${title !== undefined ? `/Title ${c.str(title)} ` : ""}${author !== undefined ? `/Author ${c.str(author)} ` : ""}>>`);
	}
	const hasInfo = title !== undefined || author !== undefined;
	return buildPdf(builders, { info: hasInfo ? builders.length : 0, encrypt: encrypt ? makeEncryption(...encrypt) : null });
}

const textPage = (lines, { x = 72, y = 720, size = 12, lead = 14 } = {}) =>
	`BT /F1 ${size} Tf ${lead} TL ${x} ${y} Td ${lines.map((l) => `(${winAnsi(l)}) Tj T*`).join(" ")} ET`;

const write = (name, buf) => writeFileSync(join(out, name), buf);

// 1. The fixture of the original's own tests (pdf-fixture.mjs).
write("text.pdf", simpleDoc([textPage(["Hello PDF", "Second (line)"])]));

// 2. Several pages, metadata, one empty page between two text pages.
write(
	"multipage.pdf",
	simpleDoc([textPage(["First page", "with two lines"]), "q 0.9 g 72 72 100 100 re f Q", textPage(["Third page text"])], { title: "  Quarterly Report  ", author: "A. Writer" }),
);

// 3. Latin text beyond ASCII through WinAnsiEncoding, plus a UTF-16 metadata title.
write("unicode-winansi.pdf", simpleDoc([textPage(["Caf\u00e9 na\u00efve \u00c5ngstr\u00f6m \u20ac10 \u2014 \u201cquoted\u201d", "Tab\there and  double  spaces"])], { title: "Caf\u00e9 \u4e2d\u6587 title", author: "Zo\u00eb" }));

// 4. A Type0 Identity-H font with a ToUnicode CMap and no embedded program: Cyrillic and CJK.
{
	const text = ["\u041f\u0440\u0438\u0432\u0435\u0442 \u043c\u0438\u0440", "\u4f60\u597d\u4e16\u754c", "Greek \u03b1\u03b2\u03b3"];
	const chars = [...new Set(text.join("").split(""))];
	const gid = new Map(chars.map((ch, i) => [ch, i + 1]));
	const hex4 = (n) => n.toString(16).toUpperCase().padStart(4, "0");
	const cmap = `/CIDInit /ProcSet findresource begin 12 dict begin begincmap /CMapName /Adobe-Identity-UCS def /CMapType 2 def 1 begincodespacerange <0000> <FFFF> endcodespacerange ${chars.length} beginbfchar ${chars.map((ch) => `<${hex4(gid.get(ch))}> <${hex4(ch.codePointAt(0))}>`).join(" ")} endbfchar endcmap CMapName currentdict /CMap defineresource pop end end`;
	const shown = (line) => `<${[...line].map((ch) => hex4(gid.get(ch))).join("")}> Tj T*`;
	const content = `BT /F1 12 Tf 14 TL 72 720 Td ${text.map(shown).join(" ")} ET`;
	const font = "<< /Type /Font /Subtype /Type0 /BaseFont /ArialMT /Encoding /Identity-H /DescendantFonts [<< /Type /Font /Subtype /CIDFontType2 /BaseFont /ArialMT /CIDSystemInfo << /Registry (Adobe) /Ordering (Identity) /Supplement 0 >> /DW 1000 >>] /ToUnicode 99 0 R >>";
	const builders = [
		() => "<< /Type /Catalog /Pages 2 0 R >>",
		() => "<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		() => "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 5 0 R >> >> /Contents 4 0 R >>",
		(c) => c.stream("", content),
		() => font,
	];
	while (builders.length < 98) builders.push(() => "null");
	builders.push((c) => c.stream("", cmap));
	write("unicode-cid.pdf", buildPdf(builders));
}

// 5. Run splitting and spacing: adjacent Tj runs, TJ arrays with small and large kerning, a
// second text object on the same baseline, and separate lines.
write(
	"runs-and-spacing.pdf",
	simpleDoc([
		"BT /F1 12 Tf 72 720 Td (He) Tj (llo) Tj ET " +
			"BT /F1 12 Tf 72 700 Td [(Kern) -30 (ing) -600 (gap) 20 (ped)] TJ ET " +
			"BT /F1 12 Tf 72 680 Td (left) Tj 100 0 Td (right) Tj ET " +
			"BT /F1 12 Tf 72 660 Td (line one) Tj 0 -14 Td (line two) Tj ET " +
			"BT /F1 12 Tf 72 620 Td (   padded   ) Tj ET",
	]),
);

// 6. Flate-compressed content streams.
write("flate.pdf", simpleDoc([textPage(["Compressed page one"]), textPage(["Compressed page two"])], { flate: true, title: "Flate", author: "Me" }));

// 7. Encrypted with the empty user password (RC4 40 bit, AES 128) and with a user password.
write("encrypted-rc4-empty.pdf", simpleDoc([textPage(["Encrypted with RC4"])], { title: "RC4 doc", encrypt: ["rc4-40", ""] }));
write("encrypted-aes-empty.pdf", simpleDoc([textPage(["Encrypted with AES"])], { title: "AES doc", encrypt: ["aes-128", ""], flate: true }));
write("encrypted-password.pdf", simpleDoc([textPage(["Needs a password"])], { encrypt: ["aes-128", "secret"] }));

// 8. Corrupt files: truncated in the middle of the page tree, damaged cross reference table
// (objects intact), a damaged compressed stream, and bytes that are not a PDF at all.
{
	const good = simpleDoc([textPage(["Recoverable text"]), textPage(["Second recoverable"])]);
	write("corrupt-truncated.pdf", good.subarray(0, Math.floor(good.length * 0.45)));
	write("corrupt-xref.pdf", Buffer.from(good.toString("latin1").replace(/startxref\n\d+/, "startxref\n999999").replace(/xref\n0 \d+/, "xref\n0 99"), "latin1"));
	const flate = simpleDoc([textPage(["Damaged stream"])], { flate: true });
	const text = flate.toString("latin1");
	const at = text.indexOf("stream\n") + 7 + 6;
	write("corrupt-stream.pdf", Buffer.concat([flate.subarray(0, at), Buffer.from("\xff\xff\xff\xff", "latin1"), flate.subarray(at + 4)]));
	write("not-a-pdf.pdf", Buffer.from("This is plain text, not a PDF document.\n"));
	write("empty.pdf", Buffer.alloc(0));
}

// 9. Scanned/empty: pages that draw but show no text.
write("scanned-empty.pdf", simpleDoc(["q 612 0 0 792 0 0 cm 0.8 g 0 0 1 1 re f Q", "q 0.5 g 100 100 200 200 re f Q"], { title: "Scan" }));

// 10. Many pages: 120 pages, more than the default pdf.maxPages of 100.
write("many-pages.pdf", simpleDoc(Array.from({ length: 120 }, (_, i) => textPage([`Page ${i + 1} body text`])), { flate: true }));

// 11. A representative report: 30 pages of justified-looking paragraphs, compressed.
{
	const words = "the quarterly results show steady growth across all regions while operating costs remained flat and customer retention improved because support response times fell below target".split(" ");
	const para = (seed) => Array.from({ length: 12 }, (_, line) => Array.from({ length: 11 }, (_, w) => words[(seed * 7 + line * 5 + w * 3) % words.length]).join(" "));
	write("report-30p.pdf", simpleDoc(Array.from({ length: 30 }, (_, i) => textPage([`Section ${i + 1}: Results`, "", ...para(i), "", ...para(i + 3)], { lead: 14 })), { flate: true, title: "Annual Report", author: "Finance Team" }));
}
