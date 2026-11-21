# Reading the capture with REA

Two tools do all of it: `inspect_web_network_capture` reads the file, `inspect_analysis_view` shows parts of what was
read. Neither fetches anything.

## 1. Read once, keep the result

Call `inspect_web_network_capture` with the absolute `capture_path`, `format` (`har`, or `saz` for Fiddler) and
`detail: "summary"`. A whole capture rarely fits a reply; the summary keeps the complete result in the session and
returns its `parent_evidence_id` and the record count.

Read the reply for what it did not cover before you use it:

- `total_records` against the number the capture tool reported. Every record is decoded before any is returned; a
  malformed record fails the whole read with its position, and nothing partial comes back.
- the `limitations` list.
- A refusal that names `maximum_capture_bytes` means the file is over REA's 33,554,432-byte input budget. Nothing was
  read. Split the capture ([capture.md](capture.md)); do not retry.

## 2. The destinations

Call `inspect_analysis_view` with `source: {kind: "retained-evidence", evidence_id: <parent_evidence_id>}` and
`view: {kind: "api-surface", include_query_parameter_names: true}`.

Each group is one method, origin and path template, with the `record_ordinals` behind it, the statuses and content
types seen, and the names of its query parameters. Path segments that look like a number, a UUID, a long hex string or
a date are replaced by a placeholder and marked `basis: "inferred"`. A group flagged `single-sample` was seen once: its
template is a guess from one URL.

This is the table of the report. It lists what was observed, grouped. It is not the app's API: a request the run did
not trigger is not in it.

## 3. The records of a group

For the groups that matter (anything that sends a body, anything that goes to a host the user would not expect), read
the records: `inspect_analysis_view` with `view: {kind: "capture", detail: "preview", offset, limit}` for request
lines and sizes, and `detail: "complete"` for one page of full records.

From each record take what was sent, not what came back: the request's method and URL, its header names, and the
structure of its body (field names, not values). Note where a body is absent though the request declares a size
(not stored, see the capture's threshold) and where it is present but unreadable (compressed, encrypted or a binary
format). Protobuf and gRPC bodies can be decoded for selected records; the REA skill says how.

## 4. Where the user's values are

Ask the user which values count as their data for this audit, with a label for each: an email address, a name, a user
or account id, the computer's name, the home directory path, a serial number, a license key. Offer to read the ones
the machine knows (`whoami`, `hostname`, `scutil --get ComputerName` on macOS) and ask before you do.

Then read the capture again, once per value, with that value in `sensitive_values`:

- REA reports every field in which the value occurs as a redaction: `{pointer, reason: "explicit-sensitive-value"}`
  under the record, where `pointer` names the field (`/request/url`, `/request/queryString/0/value`,
  `/request/postData/text`, `/request/headers/3/value`). The field's content comes back as `null`.
- The value itself is never written into REA's result; only how many values were declared.

So the list of redactions of one such read is the answer to "which requests carry this value, and in which field",
without the value appearing anywhere. Keep to one value per read: a redaction does not say which of several declared
values matched.

A match is by spelling, so declare the spellings the value can travel in, in the same read:

| Spelling | Finds it in |
|---|---|
| as typed | JSON and form bodies, decoded query values, headers |
| percent-encoded (`@` as `%40`, `/` as `%2F`, space as `%20` or `+`) | the raw URL, form-encoded bodies |
| base64 of the value | fields that carry an encoded blob |
| lower case, upper case | identifiers an app normalizes |
| a hash of it (hex SHA-256, SHA-1, MD5) | "anonymous" identifiers derived from it |

Report, per value: the records and fields where each spelling was found, and the spellings tried and not found. When a
read with several spellings has redactions, read again with one spelling at a time to learn which one matched. Do not
open the capture file yourself to look: that puts the value, and everything beside it, into the conversation.

**Not found is weaker than found.** A value inside a compressed or encrypted body, inside a larger base64 blob at an
offset that changes its encoding, salted before hashing, or split across fields is not found by any of these. Say
"not found in the readable fields, in these spellings" and list the bodies that could not be read.

Only requests matter for what was sent. A redaction under `/response/` means the server returned the value; report it
separately.

## What the capture does not say

- Which process or code sent a request: [attribute.md](attribute.md).
- That a destination is the company it appears to be. A host name is what was contacted, not who operates it.
- What the server does with what it received.
