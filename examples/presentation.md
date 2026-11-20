```parchment-meta
{
  "parchment_format": "parchment-single-file-v2",
  "kind": "presentation",
  "format_version": 2,
  "created_at": "2026-01-15T09:00:00Z",
  "modified_at": "2026-11-20T00:18:19.924Z"
}
```

<!-- parchment-body -->

# Parchment: work with files, not a vault

A local-first terminal workspace

## Start with a path

Open an existing Markdown file or create one where you want it:

```sh
parchment ~/Documents/ideas.md
parchment tui ~/Documents/brief.md --kind document
```

: Files stay in your folders; no library or setup is required.

## One file, many artifact types

- **Note** — quick, flat Markdown capture.
- **Document** — structured Markdown with page layout and a reader.
- **Spreadsheet** — named sheets and explicit formula cells.
- **Presentation** — Markdown slides with speaker notes.

: Open the matching examples in this directory to see each format in a real file.

## Notes: capture and find

Write in the native note editor. Use `Ctrl+S` to save, `F5` for a rendered
preview, and `Ctrl+F` to find text. Notes stay flat; the document outline is
not applied here.

: The preview is a view of canonical Markdown, not a conversion that replaces the source.

## Documents: structure and publish

Use headings, internal links, and a left-side outline to navigate a document.
Page and section breaks, columns, headers, footers, and page numbers shape the
printed output.

```sh
parchment document print examples/document.md --format pdf -o /tmp/guide.pdf
parchment document export examples/document.md --format docx -o /tmp/guide.docx
```

: DOCX and ODT import/export cover a documented subset and report lossy conversions.

## Spreadsheets: model a plan

The budget example has two sheets. Its formulas use ranges, aggregate
functions, and references between sheets. Edit cells in the TUI and `Ctrl+S`
commits the workbook.

```sh
parchment spreadsheet show examples/budget.md
parchment spreadsheet export examples/budget.md --csv --sheet "Work Plan"
```

: Formula cells are explicit structured data, not text guessed from the body.

## Presentations: tell the story

Slides use Markdown headings. Speaker notes begin with `: ` and stay out of the
plain slide preview; embedded images travel in the presentation's structured
payload.

.image image-7575944ea8cb624c.png 12 48

: This image is embedded in the same file as the slide source.

## Edit, recover, and undo

Parchment edits files in its native TUI and writes atomically. Note, document,
spreadsheet, and presentation editors can recover unsaved work after an
abnormal exit. After you edit and save a copy, saved-file undo and redo persist
across restarts. For example, on a copy at `~/Documents/product-tour.md`:

```sh
parchment history log ~/Documents/product-tour.md
parchment history undo ~/Documents/product-tour.md
```

: Recovery drafts and history are application state, not sidecars beside the artifact.

## Images and other files

Open a standalone PNG or JPEG to preview, crop, or resize it. Documents and
presentations can instead embed images in their own file. Neither workflow
requires moving artifacts into a Parchment folder.

: Image editing supports undo for the current session; persistent artifact history applies to saved Markdown artifacts.

## Keep files portable

Markdown bodies remain inspectable in an ordinary text editor. Print documents
or presentations to PDF, export spreadsheets to CSV, and import/export
documents as ODT or DOCX.

: The file at the chosen path remains authoritative and works offline.

## Configuration and private state

`~/.parchment/` stores Parchment's configuration, write lock, recovery drafts,
logs, and saved-file history. Theme and logging are configurable; local backup
snapshots are opt-in and go to a user-chosen destination.

: Configuration and application state never contain the artifacts themselves.

## Try the examples

Copy an example if you want to experiment without changing the checked-in
file. Open this deck in the TUI, or print it as a landscape PDF:

```sh
parchment examples/presentation.md
parchment presentation print examples/presentation.md --format pdf -o /tmp/parchment-tour.pdf
```

: Thank the audience, then invite them to open a file by path and explore.

<!-- parchment-blocks -->

```parchment-presentation
{
  "version": 1,
  "images": [
    {
      "name": "image-7575944ea8cb624c.png",
      "data": "iVBORw0KGgoAAAANSUhEUgAAAGAAAAAwCAIAAABhdOiYAAAAz0lEQVR42u3arQ3CQBgG4G6CJh0ARkBgugELsAGSHViAVIMiQaGwLNAdSEjw2DP9BHfiCk/yDnB58v1c2mtm84UEaRAAAgSodqD3cyie4XEaC6AISAVFQFosAjKDIiBDOgKyxSIgaz4Ccg+KgFwUv8zYMdrlumwAAaoWaLXZ5QRQdUC3/TWNFgMECNBPA2UO6Xhg5wC97ts0gGoFqrbFAAECVASou/RpJj+kAQHSYoAAWfM+uR6O51JRQf9XQf6LAQIECBAgQN5JAwIEaDL5AJmy43tqjq0cAAAAAElFTkSuQmCC"
    }
  ]
}
```
