// Records how the original turns a response body into text (extract.ts readTextResponseWithLimit: the charset of the
// content type, else UTF-8, through TextDecoder; an unknown label falls back to UTF-8) for byte samples that are
// not valid UTF-8, a BOM, and the legacy encodings. The Go twin (decodetext_test.go) compares decodeText.
//
//   node port/record-decode-text.mjs      # rewrites extensions/websearch/testdata/decode-text.json
import { writeFileSync } from "node:fs";

const samples = {
	ascii: [0x68, 0x69],
	"invalid ff fe": [0x61, 0xff, 0xfe, 0x62],
	"truncated 2-byte": [0x61, 0xc3],
	"bad continuation": [0xc3, 0x28],
	"truncated 3-byte": [0x61, 0xe2, 0x82],
	"truncated 4-byte": [0xf0, 0x9f, 0x98],
	"surrogate d800": [0xed, 0xa0, 0x80, 0x78],
	"above 10ffff": [0xf4, 0x90, 0x80, 0x80],
	overlong: [0xc0, 0x80, 0xe0, 0x80, 0x80],
	"utf-8 bom": [0xef, 0xbb, 0xbf, 0x68, 0x69],
	"utf-16le bom": [0xff, 0xfe, 0x68, 0x00, 0x69, 0x00],
	"utf-16be bom": [0xfe, 0xff, 0x00, 0x68, 0x00, 0x69],
	"utf-16le odd": [0x68, 0x00, 0x69],
	"utf-16 lone surrogate": [0x00, 0xd8, 0x61, 0x00],
	"high bytes": Array.from({ length: 128 }, (_, i) => 0x80 + i),
	"latin1 text": [0x63, 0x61, 0x66, 0xe9, 0x20, 0x93, 0x71, 0x94, 0x80, 0x9f],
	"shift_jis": [0x93, 0xfa, 0x96, 0x7b, 0x8c, 0xea, 0xb1, 0xb2],
	gbk: [0xc4, 0xe3, 0xba, 0xc3, 0x81, 0x40],
	"euc-kr": [0xbe, 0xc8, 0xb3, 0xe7],
	big5: [0xa7, 0x41, 0xa6, 0x6e],
	"euc-jp": [0xc6, 0xfc, 0xcb, 0xdc, 0x8e, 0xb1],
	"koi8-r": [0xf0, 0xd2, 0xc9, 0xd7, 0xc5, 0xd4],
	"truncated multibyte": [0x93],
	"nul and controls": [0x00, 0x01, 0x7f, 0x85],
	"emoji utf-8": [0xf0, 0x9f, 0x98, 0x80, 0xe2, 0x82, 0xac],
	"empty": [],
};
const types = [
	"", "text/html", "text/html; charset=utf-8", "text/html; charset=UTF-8", 'text/html; charset="utf-8"', "text/html;charset=utf8",
	"text/html; charset=iso-8859-1", "text/html; charset=latin1", "text/html; charset=windows-1252", "text/html; charset=us-ascii",
	"text/html; charset=iso-8859-2", "text/html; charset=windows-1251", "text/html; charset=koi8-r", "text/html; charset=shift_jis",
	"text/html; charset=euc-jp", "text/html; charset=iso-2022-jp", "text/html; charset=gbk", "text/html; charset=gb2312", "text/html; charset=gb18030",
	"text/html; charset=big5", "text/html; charset=euc-kr", "text/html; charset=utf-16", "text/html; charset=utf-16le", "text/html; charset=utf-16be",
	"text/html; charset=macintosh", "text/html; charset=x-user-defined", "text/html; charset=bogus", "text/html; charset=", "text/plain; CHARSET = 'utf-8' ; x=y",
	"text/html; charset=replacement", "text/html; charset=iso-8859-15",
];
const out = [];
for (const [name, bytes] of Object.entries(samples)) {
	for (const contentType of types) {
		const buf = Buffer.from(bytes);
		const charset = contentType.match(/charset\s*=\s*["']?([^;"'\s]+)/i)?.[1];
		let text;
		try {
			text = new TextDecoder(charset || "utf-8").decode(buf);
		} catch {
			text = new TextDecoder("utf-8").decode(buf);
		}
		out.push({ sample: name, hex: buf.toString("hex"), contentType, text });
	}
}
writeFileSync(new URL("../extensions/websearch/testdata/decode-text.json", import.meta.url), JSON.stringify(out, null, 1) + "\n");
console.log(out.length + " cases");
