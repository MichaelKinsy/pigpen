# Upstream Provenance: `internal/markit`

Ported from omp (`https://github.com/can1357/oh-my-pi`, MIT, tag `v19.1.1`, commit `31ffee513f`).
Original DOCX, PPTX, XLSX, and EPUB converters in omp are adapted from `markit-ai` (MIT, Copyright (c) 2026 Michael Liv; see upstream `packages/coding-agent/src/markit/NOTICE`).

## Upstream Files Ported
- `packages/coding-agent/src/markit/types.ts` (lines 1-35) -> `registry.go` (`StreamInfo`, `Result`, `Converter`)
- `packages/coding-agent/src/markit/registry.ts` (lines 1-60) -> `registry.go` (`Registry`, `NewRegistry`, `ConvertFile`, `Convert`)
- `packages/coding-agent/src/utils/markit.ts` (lines 1-185) -> `registry.go` (`ConvertibleExtensions`, `NormalizeExtension`, `IsConvertibleExtension`, `ConvertFile`, `ConvertBuffer`)
- `packages/coding-agent/src/markit/converters/docx.ts` (lines 1-51) + `packages/utils/src/docx/converter.ts` (lines 1-695) + `packages/utils/src/docx/xml.ts` (lines 1-166) -> `docx.go`, `ziputil.go`
- `packages/coding-agent/src/markit/converters/pptx.ts` (lines 1-326) -> `pptx.go`
- `packages/coding-agent/src/markit/converters/xlsx.ts` (lines 1-180) -> `xlsx.go`
- `packages/coding-agent/src/markit/converters/epub.ts` (lines 1-137) -> `epub.go`
- `packages/coding-agent/src/markit/converters/pdf/index.ts` (lines 1-39) -> `pdf.go` (CLI `pdftotext` backend; see Not Ported below)
- `packages/coding-agent/src/utils/turndown.ts` (lines 1-90) + `packages/utils/src/turndown/types.ts` (lines 1-88) + `packages/utils/src/turndown/html.ts` (lines 1-277) + `packages/utils/src/turndown/service.ts` (lines 1-376) + `packages/utils/src/turndown/gfm.ts` (lines 1-117) -> `htmlmd.go`
- `packages/utils/src/ar/limits.ts` (lines 1-91) + `packages/utils/src/ar/zip-package.ts` (lines 1-57) -> `registry.go`, `ziputil.go` (in-memory, entry count, member size, and deduplicated total inflated byte caps)

## Deliberate Deviations from Upstream
- **DOCX block-level `<w:sdt>`**: renders `<w:sdtContent>` children inside `<w:sdt>` containers (upstream `docx/converter.ts:624` recurses directly on `<w:sdt>` children without unwrapping `<w:sdtContent>`, dropping block-level SDT content).
- **PPTX text runs**: preserves run whitespace and leading-zero numeric text verbatim (e.g. `# Title with runs`, `007`), whereas upstream's XML parser trims text nodes and coerces numeric strings (`# Titlewithruns`, `7`).
- **PPTX & XLSX tables**: escapes literal `|` as `\|`, flattens embedded newlines to spaces, and pads header rows to `maxCols` so malformed cells cannot break GFM pipe tables.
- **EPUB metadata & relative links**: preserves numeric Dublin Core `dc:title`/`dc:date` strings and normalizes `../` chapter hrefs relative to the OPF directory.
- **Nesting depth cap (512)**: XML elements, HTML elements, and DOCX `w:ilvl` list nesting are capped at `MaxXMLNestingDepth = 512` (returning an explicit error instead of stack overflow / quadratic amplification; e.g. 600 unclosed `<p>` tags or 200,000 list levels fail cleanly whereas upstream attempts to convert them).
- **EPUB cumulative Markdown cap**: spine rendering enforces `MaxInMemorySize` (256 MiB) on accumulated Markdown output so repeated spine references cannot amplify memory past the cap.
- **`pdftotext` subprocess bounds**: `PdfConverter` runs `pdftotext` with a 30 s default timeout (bounded by caller `ctx`), a 256 MiB (`MaxInMemorySize`) stdout cap, and an 8 KiB stderr cap, passing `LocalPath` directly when available.

## Not Ported

| Feature | Upstream File:Lines | Reason |
|---|---|---|
| Native Rust `pdf-inspector` (`pdfToMarkdown`) and OCR page detection (`pagesNeedingOcr`) | `packages/coding-agent/src/markit/converters/pdf/index.ts:1-39`, `crates/natives/src/pdf.rs` | Requires native Rust crate / cgo; forbidden by pure-Go stdlib rule. Replaced by bounded `pdftotext` CLI dispatch with explicit `ErrPdftotextNotFound` refusal when `pdftotext` is absent from PATH. |
| Chromium-rendered PDF visual page screenshots (`doc.pdf:page<N>.png`) | `packages/coding-agent/src/tools/read-pdf.ts:1-162`, `packages/coding-agent/src/tools/read.ts:1689-1720,2439-2447,2584-2593` | Requires headless Chromium / browser rendering backend not available in pure Go stdlib. |
| Markit conversion of PDF/document members inside archives (`archive.zip:doc.pdf`) | `packages/coding-agent/src/tools/read-archive.ts:326-374` | Archive member reads in `internal/files/sources.go` decode UTF-8 text and raster images only; nested markit conversion inside archive members is not wired. |
| Web scraper, `@mention`, and CLI file-processor markit callers | `packages/coding-agent/src/web/scrapers/utils.ts:242`, `packages/coding-agent/src/utils/file-mentions.ts:318`, `packages/coding-agent/src/cli/file-processor.ts:142` | Only filesystem `read` dispatches to `internal/markit`. |
| On-disk SHA-256 conversion cache (`markit-cache`) | `packages/coding-agent/src/utils/markit-cache.ts:1-166`, `packages/coding-agent/src/utils/markit.ts:90-134` | Deferred optimization; conversions run directly in-process. |
| Vision/audio `describe` / `transcribe` callbacks on `MarkitOptions` | `packages/coding-agent/src/markit/types.ts:19-26` | Requires multimodal sub-call plumbing not wired into `read`. |
| Custom mammoth `styleMap` overrides (`ConvertToHtmlOptions.styleMap`) | `packages/utils/src/docx/converter.ts:304-322` | Not exposed by `markit`'s `DocxConverter` (only default heading/style mapping is used by `markit`). |
| Legacy binary Office formats (`.doc`, `.ppt`, `.xls`, `.rtf`) | `packages/coding-agent/src/markit/NOTICE:9-11`, `packages/coding-agent/src/utils/markit.ts:17-21` | Intentionally absent upstream as well; falls through to binary-file handling. |
