# Parchment Technical Guide

This guide describes Parchment's file format and implementation boundaries.
User-facing commands and workflows are in the [User Guide](User_Guide.md);
complete working samples are in [`examples/`](../examples/).

## Design and file ownership

Parchment is local-first. The artifact file is authoritative, ordinary
filesystem tools can inspect and edit it, and normal operations work offline.
Each artifact is one standalone, user-named file in the location selected by
its owner. Parchment does not own artifact directories, require a workspace,
or create per-artifact sidecars.

The file path is the user-facing identity. Runtime IDs and titles are derived
from the path and are not persisted in the artifact envelope. Moving or
renaming a file moves or renames the artifact; opening it by its new path
continues to work. A standalone PNG/JPEG is a binary image file, not a
Markdown artifact.

`~/.parchment/` is reserved for Parchment application state, such as its
configuration, write lock, recovery drafts, logs, and persistent undo/redo
history. Recovery and history data are exceptions to the one-file-per-artifact
rule; neither stores the artifact itself as the authoritative copy.

## Markdown artifact envelope

A Parchment note, document, spreadsheet, or presentation is UTF-8 Markdown
with an embedded envelope. The format is deliberately readable and
line-oriented:

````markdown
```parchment-meta
{
  "parchment_format": "parchment-single-file-v2",
  "kind": "note",
  "format_version": 2,
  "created_at": "2026-01-15T09:00:00Z",
  "modified_at": "2026-01-15T09:00:00Z"
}
```

<!-- parchment-body -->

The artifact's canonical Markdown body is here.
```
````

The metadata fence is the first Markdown block (leading blank lines are
allowed). It is JSON and currently contains exactly:

| Field | Meaning |
| --- | --- |
| `parchment_format` | Envelope discriminator; currently `parchment-single-file-v2`. |
| `kind` | One of `note`, `document`, `spreadsheet`, or `presentation`. |
| `format_version` | Shared artifact format version; currently `2`. |
| `created_at` | Creation timestamp in UTC RFC 3339 form. |
| `modified_at` | Last-modified timestamp in UTC RFC 3339 form. |

Do not add an ID, title, path, tags, or links to shared metadata. The file
name is the user-visible name; runtime identity is derived from the path.
Parchment has not had a first release and does not promise compatibility with
older envelope formats.

The exact `<!-- parchment-body -->` line separates the envelope from the
canonical body. The body is retained as Markdown source; renderers must not
replace it with rendered output or otherwise mutate it. The explicit separator
allows the body itself to begin with a fence whose info string starts
`parchment-`, without mistaking that body fence for structured artifact data.
Plain Markdown without a metadata envelope can be opened as a note and remains
plain Markdown when saved.

## Structured blocks and code fences

Kind-specific data is stored after the body, preceded by the exact
`<!-- parchment-blocks -->` line:

````markdown
<!-- parchment-body -->

Human-authored Markdown remains here.

<!-- parchment-blocks -->

```parchment-document
{
  "layout": { "version": 1 },
  "images": []
}
```
````

Each structured payload is a fenced code block with a `parchment-*` info
string and valid JSON content. Several structured blocks may follow the marker.
The marker is significant: the parser only interprets blocks in this trailing
region, never a similarly named fence among body paragraphs. This preserves
literal examples and lets authors discuss Parchment syntax in ordinary
Markdown. A reserved-looking fence in the body is source text, not artifact
data.

Parchment's renderer hides fenced blocks whose info string begins with
`parchment-`, wherever they occur, but leaves ordinary fenced code blocks and
user prose visible. Rendering behavior is not a parsing rule: a reserved
fence can be hidden from a view without being interpreted as structured data.
Code fences use Markdown fence delimiters; the parser recognizes backtick or
tilde fences and requires a matching closing fence. Preserve the delimiter and
payload when editing an artifact, and do not use reserved `parchment-*` info
strings for ordinary code samples unless the goal is to hide that sample in
Parchment's rendered views.

Structured data is JSON, normally pretty-printed. The shared writer sorts
block names when encoding for deterministic output. A trailing block sequence
may contain only reserved `parchment-*` fences; duplicate block names,
duplicate metadata, malformed JSON, and invalid metadata are errors. Unknown
reserved blocks can be preserved by artifact readers that do not own them.
The current implementation caps a complete artifact at 64 MiB.

### Kind-specific data

The body and trailing data serve distinct purposes: body text is the
human-authored canonical content, while blocks hold typed data needed by the
application.

| Kind | Body | Structured blocks |
| --- | --- | --- |
| Note | Canonical Markdown note text. | No kind-specific block is required. |
| Document | Canonical Markdown including anchors, links, page/section break comments, and image references. | `parchment-document`: layout, embedded image bytes, and versioned pending tracked changes. |
| Spreadsheet | Optional human-readable workbook description. | `parchment-spreadsheet`: versioned sheets, cell arrays, explicit literal/formula cells, and range-bound chart definitions. |
| Presentation | Go `present`-style Markdown source: title, slides, notes, image/code directives. | `parchment-presentation`: versioned presentation settings and embedded image bytes. |

The [`document.md`](../examples/document.md) example shows `parchment-document`
with layout and embedded image content. [`budget.md`](../examples/budget.md)
shows multiple sheets and formula cells in `parchment-spreadsheet`.
[`presentation.md`](../examples/presentation.md) shows slide source, speaker
notes, an image directive, and embedded presentation image data.

In JSON, binary image data is encoded as base64 strings. The image name used
by a body reference or presentation directive must match the name stored in
the block. Document image references use ordinary Markdown links. Presentation
slides use Go `present` syntax: `#` for the deck title, `##` for slide
headings, `###` for subsections, `//` for comments, `: ` for speaker notes,
and `.image` / `.code` directives for media and source excerpts.

Spreadsheet cell objects distinguish literal content from formulas. A literal
uses `value`; a formula uses `formula`. A string beginning with `=` is not
automatically treated as a formula. Formula parsing, evaluation, and reference
adjustment belong to the spreadsheet domain layer, not Markdown rendering.

Document tracking uses version 1 metadata in the `parchment-document` block.
Each pending replacement stores a stable ID, UTC timestamp, UTF-8 byte offset,
deleted text and inserted text; inserted text is part of the current body.
Anchors are validated against that body on load, transformed during edits, and
coalesced when edits overlap. Review removes metadata on acceptance or reverses
the replacement on rejection. Image-reference moves can link multiple
replacements for atomic review; embedded bytes are retained while any change
is pending. Layout changes are not tracked.

## ODT and DOCX interchange

Document import and export use pure-Go adapters over ZIP archives and XML;
they require no office suite or external conversion process. Import accepts
ODT and DOCX archives up to 64 MiB, with at most 1000 entries and 128 MiB of
total uncompressed data. XML external entities and document types are not
expanded. Each imported embedded image is limited to 10 MiB; PNG, JPEG, and
GIF are supported.

Conversion maps a defined subset rather than promising lossless office-format
round-trips: headings, paragraphs, inline emphasis/code, links, lists,
blockquotes, code blocks, GFM tables, page and section breaks, common page
geometry, columns, headers/footers, page numbers, document-wide fonts, and
supported embedded images. Page dimensions are mapped to supported sizes;
margins and column counts are clamped or rounded. Per-run and per-section
fonts collapse to one document font. Unsupported content and approximations
are reported as warnings. External images and unsupported image formats are
skipped; their fallback text is retained where available. Export uses the
current Document body and omits pending review metadata with a warning.

## Persistence and external edits

Services in `internal/note`, `internal/document`, `internal/spreadsheet`, and
`internal/presentation` own domain behavior. The filesystem repository in
`internal/filerepo` owns file I/O, locking, encoding, and atomic writes.
`internal/artifactfile` reads and writes the shared envelope and
`internal/artifact` defines common metadata. The CLI and TUI orchestrate these
layers rather than duplicating domain rules.

Writes use a temporary file in the artifact's directory, sync it, then rename
it into place. Existing permissions are preserved, new artifact files are
owner-only, and symbolic links are preserved by updating their targets.
Parchment compares the current file with the version loaded before writing and
rejects an external edit instead of silently overwriting it. The global write
lock and Parchment-owned state stay under `~/.parchment/`; no other files are
placed beside artifacts.

Recovery drafts protect unsaved editor work and are keyed to the artifact
path. Persistent undo/redo records successful saved service operations in
application state. These mechanisms do not change which file is canonical:
the ordinary artifact at its user-selected path remains authoritative.

### Persistent history storage

History is stored under `~/.parchment/history/`, outside artifact directories.
Each file's history directory is keyed by the SHA-256 hash of its resolved
canonical path, so opening a symlink and its target uses the same history.
History reads and writes coordinate through Parchment's global write lock.

Each per-file `index.json` manifest records a bounded sequence of snapshots,
the current head, and the configured undo limit. A new artifact starts at a
non-undoable initial baseline. Snapshot data is stored in immutable,
content-addressed SHA-256 blobs: envelope metadata, the Markdown body,
structured blocks, and embedded image bytes can be shared across snapshots.
When a snapshot cannot be re-encoded to the exact original bytes, history
stores the full raw file instead. Garbage collection removes blobs no longer
referenced by any manifest.

Artifact and history changes use a journaled transition. The repository first
stages `index.pending.json` and a `journal.json` containing source and target
file digests and the staged manifest digest. It then atomically writes the
artifact, commits the manifest, and removes the journal and pending manifest.
On the next history operation, recovery compares the on-disk artifact digest:
the target digest commits the staged manifest, the source digest leaves the
old manifest in place, and any other digest is reported as an external-change
conflict. Cleanup and garbage-collection failures after a successful commit
are warnings rather than failed saves.

Undo and redo refuse to replace a file that differs from the history head.
When a user saves a new Parchment edit over externally modified content, that
content is checkpointed as an `"External edit"` entry. Restore also preserves
the external checkpoint and requested restore target while applying bounded
retention.

## Related documentation

- [User Guide](User_Guide.md) — commands, editors, printing, recovery, and examples.
- [TUI Design Contract](TUI_Design.md) — interaction and rendering conventions.
