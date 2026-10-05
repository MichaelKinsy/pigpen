# Credits

`extensions/websearch` is a Go port of **pi-web-access** by **Nico Bailon**,
https://github.com/nicobailon/pi-web-access (MIT, Copyright (c) 2025 Nico Bailon).

- Original: pi-web-access 0.33.0, commit `9a734ed195da2f4cccc2fb5e7128f6774a380f47`
- The unmodified original (source and its test suite) is kept under [`port/oracle/`](port/oracle)
  as the equivalence oracle, with its license at [`port/oracle/LICENSE`](port/oracle/LICENSE).
- The upstream test titles are recorded in [`port/upstream-tests.json`](port/upstream-tests.json);
  the Go tests are twins of them (same title, same inputs, same expectations) or named skips
  with a reason.

The Go code, scenarios and Go tests were written for this Package by Michael Kinsy. Behavior,
wording, defaults and error strings follow the original. Where the port differs on purpose
(HTML-to-Markdown implementation, hardened SSRF checks, timeout classification, deferred
features) each difference is listed in [`port/PORT.md`](port/PORT.md). No file of the original
is modified; the port is a separate implementation.
