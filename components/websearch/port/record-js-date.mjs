// Records what the original's runtime (V8) makes of new Date(string) for the date strings the Defuddle extractors
// feed it (GitHub datetime attributes, Gmail title attributes, Discourse meta tags), so the Go parser
// (dfd_ext_date.go) is checked against V8's own answers. Run in the container with the time zone fixed:
//   TZ=UTC node port/record-js-date.mjs
// Writes extensions/websearch/testdata/jsdate.expected.json: [{ input, ms }] with ms null for an invalid date.
import { writeFile } from "node:fs/promises";

const inputs = [
	"", " ", "2025-03-02T10:00:00Z", "2025-03-02T10:00:00.123Z", "2025-03-02T10:00:00+02:00", "2025-03-02T10:00:00-0530",
	"2025-03-02T10:00:00", "2025-03-02", "2025-03", "2025", "+002025-03-02", "-000001-01-01T00:00:00Z", "-000000-01-01",
	"2025-03-02T24:00:00Z", "2025-03-02T24:00:01Z", "2025-13-02", "2025-02-30", "2025-02-30T00:00:00Z", "2025-3-2",
	"2025-03-02T10:00", "2025-03-02T10:00Z", "2025-03-02T10:00:00.1Z", "2025-03-02T10:00:00.123456789Z", "2025-03-02 10:00:00",
	"2025-03-02 10:00:00Z", "2025-03-02T1:00:00Z", "not a date", "Tue, Mar 4, 2025, 10:15 AM", "Tue, Mar 4, 2025, 10:15\u202fAM",
	"Wed, Mar 5, 2025, 9:05 PM", "Mar 4, 2025", "March 4, 2025 10:15:30", "4 Mar 2025", "4 March 2025 10:15", "Jan 5",
	"1/2/2025", "13/2/2025", "2025/03/04", "12/31/99", "1/1/49", "1/1/50", "Tue Mar 04 2025 10:15:00 GMT+0100 (CET)",
	"Tue, 04 Mar 2025 10:15:00 GMT", "Tue, 04 Mar 2025 10:15:00 +0200", "Mar 4 2025 12:00 PM", "Mar 4 2025 12:00 AM",
	"Mar 4 2025 13:00 PM", "Mar 4 2025 10:15 EST", "Mar 4 2025 10:15 PDT", "Mar 4 2025 10:15 UTC+3", "Mar 4 2025 10:15 GMT-8",
	"10:15", "10:15 AM", "5 pm", "mar", "2025-03-02T10:00:00Z extra", "2025-03-02T10:00:00 (comment)", "(x) Mar 4, 2025",
	"Mar 4, 2025 ) x", "Monday", "Mon 5", "foo 5", "foo5", "5foo", "1.5", "10:15:30.5", "10:15:30.123456", "Mar 4 2025 10:15:30.25",
	"Mar-4-2025", "4-Mar-2025", "2025-Mar-04", "275760-09-13", "+275760-09-13T00:00:00.000Z", "+275760-09-13T00:00:00.001Z",
	"Sep 13 275760", "Dec 31 99999", "Jan 1 1970 00:00:00 UTC", "Jan 1 1970 00:00:00 +0000", "Jan 1 1970 00:00:00 +00:00",
	"Jan 1 1970 00:00:00 -5", "Jan 1 1970 00:00:00 +123456", "June 31, 2025", "Feb 29, 2024", "Feb 29, 2025", "0", "12345", "123456789",
	"1234567890123", "Thu, 01 Jan 1970 00:00:00 GMT", "2025-03-04T10:15:30.000+05:30", "2025-03-04T10:15:30z", "2025-03-04t10:15:30Z",
	"  2025-03-02T10:00:00Z", "2025-03-02T10:00:00Z  ", "2025-03-02T10:00:00 Z", "Tue,Mar,4,2025", "Tuesday, March 4, 2025 at 10:15 AM",
	"Mar 4, 2025, 10:15 AM PST", "3/4/2025, 10:15:30 AM", "4.3.2025", "2025.03.04", "ÄÖ 5 Mar 2025", "Mär 4, 2025", "4 mars 2025",
];
const out = inputs.map((input) => {
	const t = new Date(input).getTime();
	return { input, ms: Number.isNaN(t) ? null : t };
});
await writeFile(new URL("../extensions/websearch/testdata/jsdate.expected.json", import.meta.url), JSON.stringify(out, null, 1) + "\n");
console.log(out.length, "inputs");
