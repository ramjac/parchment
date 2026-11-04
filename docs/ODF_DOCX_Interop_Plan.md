# ODF / DOCX Import & Export Plan

Plan and implement document conversion around Markdown text, basic document
layout, printer font selection, and embedded images as part of **Phase 5**
(PRs 31–35, following Phase 4 in [`FutureWork.md`](../FutureWork.md)).
Conversion is intentionally lossy: Parchment does not represent multiple
per-section fonts, exact floating object positioning, or every feature
available in office formats. Import and export must report unsupported or
approximated content rather than implying a lossless round trip.

## Architecture & Dependencies

- Implement format-specific adapters in a dedicated `internal/docinterop`
  package behind a clean document import/export boundary, keeping ZIP archive
  and XML schema details out of `internal/document`.
- Build pure-Go adapters directly over the Go standard library (`archive/zip`
  and `encoding/xml`), avoiding unmaintained or commercial third-party office
  packages and requiring zero external binaries (`pandoc`, `soffice`) so
  operations stay completely offline and portable.
- **Markdown Parsing & Generation:**
  - **Export:** Parse the document Markdown `Body` into an AST using Goldmark
    with GFM extensions enabled (`goldmark.WithExtensions(extension.GFM)`),
    which is already a dependency of Glamour.
  - **Import:** Walk the parsed Office XML elements and generate clean, canonical
    Markdown using a dedicated streaming buffer/builder in `internal/docinterop`
    enforcing Parchment's Markdown integrity conventions (canonical code fences,
    GFM pipe tables, standardized heading slugs/anchors).
- Enforce strict archive and XML resource limits during import:
  - Maximum compressed input file size: `64 MiB`.
  - Maximum total uncompressed archive bytes: `128 MiB`, and at most `1000` ZIP
    entries (guarding against ZIP bombs).
  - Disallow XML external entity expansion (`DOCTYPE` / `ENTITY` directives).
  - Enforce `document.MaxImageBytes` (`10 MiB`) per embedded image.

## CLI Command Contract

- **Import (`ODF` / `DOCX` -> Parchment Document `.md`):**
  - `parchment document import <source.odt|source.docx> <target.md> [--format odt|docx] [--no-edit]`
  - Detect input format from ZIP `mimetype` (ODF) or `[Content_Types].xml`
    (DOCX), allowing `--format odt|docx` to override or disambiguate.
  - Refuse to overwrite `<target.md>` if it already exists, preserve `<source>`,
    write `<target.md>` atomically with `0600` owner-only permissions, and
    embed valid images inside the `parchment-document` JSON payload (never
    creating asset sidecars).
  - Match `document create` interactive behavior: open the newly imported
    document in the TUI editor when run in an interactive terminal unless
    `--no-edit` is passed; print `<target.md>` to `stdout` when non-interactive
    or when `--no-edit` is set.
- **Export (Parchment Document `.md` -> `ODF` / `DOCX`):**
  - `parchment document export <source.md> -o <target.odt|target.docx> [--format odt|docx] [--force]`
  - Determine output format from `--format` when provided, otherwise from the
    `-o` file extension (`.odt` or `.docx`).
  - Preserve `<source.md>`, write `<target>` atomically (`0600` when creating a
    new file, preserving permissions when `--force` replaces an existing file),
    and refuse to overwrite an existing `<target>` unless `--force` is passed.
  - **Pending Proposals:** Export the accepted `Body` only (ignoring pending or
    resolved proposals), and emit a warning line to `stderr`
    (`warning: N pending proposal(s) not exported`) if any proposals are pending.
- **Warning & Exit-Code Semantics:**
  - Conversion is CLI-only (no in-TUI import/export dialogs).
  - Write human-readable lossy conversion and approximation warnings to
    `stderr` (one warning per line, prefixed with `warning: `) so `stdout`
    remains clean for scripting, and exit with status `0` when conversion
    succeeds with warnings. Return a non-zero exit status only on fatal errors
    (missing source, existing target without `--force`, malformed archive/XML,
    or archive safety-limit violations).

## Documented Content, Layout & Media Mapping

- **Supported Markdown Body Subset:**
  - Headings (`H1`–`H6`).
  - Paragraphs and line breaks.
  - Inline formatting: bold, italic, strikethrough, inline code, external/internal
    links, and explicit `#anchor` targets / bookmarks (`<a id="..."></a>`).
  - Unordered and ordered lists, including nested lists.
  - Blockquotes, fenced code blocks, horizontal rules, and GitHub Flavored
    Markdown (GFM) pipe tables.
  - Explicit page breaks (`<!-- parchment:page-break -->`) and multi-column
    section breaks (`<!-- parchment:section-break columns=N [continuous] -->`).
- **Layout & Font Mapping (`document.Layout`):**
  - **Page size & orientation:** Map `letter`, `a4`, and `legal` in `portrait`
    or `landscape`. On import, snap custom page dimensions to the closest
    supported Parchment page size + orientation and emit a warning on `stderr`.
  - **Margins:** Convert ODF (`fo:margin-*`) and DOCX (`w:pgMar` twips) margins
    to rounded integer millimeters clamped to `0..100` mm, emitting a warning
    if clamping or rounding alters geometry beyond `1 mm`.
  - **Columns:** Map document/section column counts clamped to
    `1..document.MaxColumns` (`4`), warning if clamped.
  - **Headers, footers & page numbers:** Map default running headers/footers
    and dynamic fields (`PAGE` / `<text:page-number>`, `NUMPAGES` /
    `<text:page-count>`, document title) to Parchment's `left|center|right`
    running strings (`{page}`, `{pages}`, `{title}`) and `PageNumbers`
    placement (`none`, `left`, `center`, `right`). If an input document defines
    distinct first-page or odd/even headers/footers, import the primary/default
    header/footer and emit a warning on `stderr`.
  - **Font family (`Layout.Font`):** Map Parchment's document-wide core font
    (`"courier"`, `"helvetica"`, `"times"`) to/from the default document style
    font family (`Courier New`, `Helvetica`/`Arial`, `Times New Roman`). On
    import, map proportional sans-serif defaults to `"helvetica"`, serif
    defaults to `"times"`, and monospaced defaults to `"courier"`, warning when
    multiple per-run/per-section fonts are collapsed to a single document font.
- **Embedded Images:**
  - Export embeds `document.Image` (`PNG`, `JPEG`, `GIF`) bytes inside ODF
    `Pictures/` or DOCX `word/media/` with matching relationships and alt text.
  - Import embeds supported `PNG`, `JPEG`, and `GIF` media (`<= 10 MiB`) via
    `document.NewImage` (`image-<hash>.<ext>`). Unsupported image formats
    (such as `SVG`, `EMF`, `WMF`, `TIFF`, `BMP`, `WebP`), external image URLs,
    or images exceeding `10 MiB` are skipped with a `stderr` warning while
    preserving fallback alt text in the Markdown body rather than aborting
    import.

## Step-by-Step PR Breakdown

Each step below is scoped as one self-contained Pull Request (`~200–450` LOC)
that compiles cleanly and passes `go test -race ./...`:

1. **PR 31 — `internal/docinterop` Foundation, Safety Reader & Markdown Bridge:**
   - Create `internal/docinterop` with format-agnostic `Exporter` / `Importer`
     interfaces, `Warning` collector (`warning: ...` formatting), bounded ZIP
     reader (`<= 64 MiB` input, `<= 128 MiB` total decompressed, `<= 1000`
     entries), entity-free `encoding/xml` decoder helper, Goldmark AST parser
     bridge for export, custom canonical Markdown generator for import, and
     shared unit/page-size/margin/header/footer/font mapping helpers with unit tests.
2. **PR 32a — DOCX Export Core Document Writer:**
   - Implement the `.docx` OpenXML archive writer (`[Content_Types].xml`,
     `_rels/.rels`, `word/document.xml`, `word/styles.xml`) for paragraphs,
     runs, headings, bold/italic/code, lists, blockquotes, and GFM tables.
   - Wire `parchment document export <src.md> -o <dst.docx> [--format] [--force]`
     in `internal/cli` (emitting `warning: N pending proposal(s) not exported`
     when proposals exist, and returning a clear unsupported-format error for `odt`
     until PR 34).
3. **PR 32b — DOCX Export Layout, Headers & Images:**
   - Add section layout, page dimensions (`w:pgSz`), margins (`w:pgMar`),
     columns (`w:cols`), running headers/footers with dynamic page numbers
     (`w:headerReference`, `w:footerReference`), and embedded `word/media/`
     image relationships to the `.docx` exporter.
4. **PR 33a — DOCX Import Core Reader & In-Test Fixtures:**
   - Implement the `.docx` reader + `[Content_Types].xml` format sniffer,
     mapping Word paragraphs, runs, lists, and tables into canonical Markdown.
   - Provide Go test helpers that construct minimal valid `.docx` archives in-memory
     to verify import decoding, plus round-trip tests through the PR 32 exporter.
   - Wire `parchment document import <src.docx> <dst.md> [--format] [--no-edit]`.
5. **PR 33b — DOCX Import Layout, Headers & Images:**
   - Map Word section geometry, margins, running headers/footers, and extract
     supported `PNG`/`JPEG`/`GIF` media into `document.Draft` images via
     `document.NewImage`. Test skipped unsupported/oversized media and warnings.
6. **PR 34a — ODF Text (`.odt`) Export Core Document Writer:**
   - Implement the `.odt` OpenDocument archive writer (`mimetype` stored first
     uncompressed per ODF spec, `META-INF/manifest.xml`, `styles.xml`,
     `content.xml`) for paragraphs, headings, lists, and tables. Enable `odt` in
     `document export`.
7. **PR 34b — ODF Text (`.odt`) Export Layout, Headers & Images:**
   - Add ODF page layout masters, margins, column styles, header/footer text
     fields, and embedded `Pictures/` media to the `.odt` exporter.
8. **PR 35a — ODF Text (`.odt`) Import Core Reader:**
   - Implement the `.odt` reader + `mimetype` format sniffer (`content.xml`),
     mapping paragraphs, lists, and tables to canonical Markdown with in-memory
     test archive fixtures. Enable `odt` in `document import`.
9. **PR 35b — ODF Text (`.odt`) Import Layout, Headers & Media:**
   - Map ODF page styles, headers/footers, and media extraction, plus cross-format
     round-trip (`.md` -> `.docx` -> `.md` -> `.odt` -> `.md`) regression tests.

**Acceptance:** ODF export, DOCX export, ODF import, and DOCX import ship across
focused sub-PRs, work offline via pure-Go standard-library archive/XML
adapters for the documented subset, preserve source files, refuse accidental
overwrites, and report all approximations and skipped features to `stderr`.
