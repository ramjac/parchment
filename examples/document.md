```parchment-meta
{
  "parchment_format": "parchment-single-file-v2",
  "kind": "document",
  "format_version": 2,
  "created_at": "2026-01-15T09:00:00Z",
  "modified_at": "2026-11-20T00:41:00.989507972Z"
}
```

<!-- parchment-body -->

# Parchment: a file-first guide

Parchment is a local-first terminal workspace for notes, documents,
spreadsheets, and presentations. Each artifact remains an ordinary file at the
path you choose. This document demonstrates Markdown and document features;
the complete
[artifact overview](#four-kinds-of-artifact) is also available as a
[presentation](presentation.md).

<a id="file-first"></a>
## Files, not a vault

Open or create one file by path. Parchment does not require `init`, a workspace,
or a Parchment-owned library:

```sh
parchment ~/Documents/launch-brief.md
parchment tui ~/Documents/launch-brief.md --kind document
```

The hidden metadata and structured data stay in that same Markdown file. The
body below the `parchment-body` marker remains canonical Markdown that other
editors can inspect and change.

## Four kinds of artifact

### Notes stay lightweight

Notes are flat, unstructured text. Use them for quick capture, then search or
preview Markdown in the native editor. Document outline navigation and
in-document link jumps are deliberately **Document-only**.

### Documents add structure

Documents add a page layout, a heading outline, and clickable internal links
in the reader and preview. Markdown links remain ordinary editable source:
[jump to printing](#print-and-share).

> **Try it:** open this file in Parchment, press `e` to edit, `Ctrl+O` to
> toggle the outline, and `F5` to preview. `Ctrl+S` saves; saved changes can
> be undone and redone.

### Spreadsheets keep formulas explicit

Workbooks store named sheets and formula cells in a structured block; plain
text that looks like a formula is not evaluated unless it is marked as one.
Open [`budget.md`](budget.md) to inspect a multi-sheet planning example.

### Presentations turn Markdown into slides

Presentations use `#` for the deck title, `##` for slides, and `: ` for
speaker notes. Open [`presentation.md`](presentation.md) for a walkthrough
of Parchment itself.

## Markdown remains portable

Common Markdown stays legible in any editor: **bold**, *emphasis*,
***both together***, `inline code`, nested lists, block quotes, and links.
This document also uses an explicit anchor and a heading-generated link target.

- Page breaks and section breaks control document pagination.
- Headers, footers, page numbers, margins, columns, and a document-wide print
  font are stored in the document layout.
- Reserved `parchment-*` data blocks are hidden by Parchment renderers; ordinary
  code fences and the source body are preserved.

<!-- parchment:section-break columns=2 continuous -->

## Print and share

Documents can print as plain text or PDF. PDF output supports Markdown styling,
embedded images, page geometry, columns, and running headers and footers.
Document import and export support the documented ODT and DOCX subset, with
warnings when conversion is lossy.

```sh
parchment document print examples/document.md
parchment document print examples/document.md --format pdf -o /tmp/guide.pdf
parchment document export examples/document.md --format docx -o /tmp/guide.docx
```

### Embedded media

Images can live in the document's own structured payload and be referenced
with ordinary Markdown. This illustration is an example of an embedded asset;
its bytes travel with the Markdown file.

![Example image embedded in the document](image-04509606a67711eb.png "Embedded media")

The same payload can be referenced by name:

![The embedded image can be reused][embedded-example]

## Review and recover

`F3` records unsaved document edits as a proposal; `F4` reviews proposals, and
accepting one updates the live document. Saving or editing in another program
does not silently overwrite external changes. Recovery drafts protect unsaved
note and document edits, while persistent service undo and redo survive
restarts.

For the configuration and filesystem model, see the
[Technical Guide](../docs/Technical_Guide.md). All examples are ordinary files:
copy one before experimenting if you want to keep the checked-in version
unchanged.

[embedded-example]: image-04509606a67711eb.png "Example embedded asset"

<!-- parchment-blocks -->

```parchment-document
{
  "layout": {
    "version": 1,
    "page_size": "letter",
    "orientation": "portrait",
    "margins_mm": {
      "top": 25,
      "right": 25,
      "bottom": 25,
      "left": 25
    },
    "columns": 1,
    "header": "Parchment|{title}|{page}",
    "footer": "Local-first files||Page {page}/{pages}",
    "page_numbers": "right",
    "font": "times"
  },
  "images": [
    {
      "name": "image-04509606a67711eb.png",
      "data": "iVBORw0KGgoAAAANSUhEUgAAAUAAAAC0CAIAAABqhmJGAAAC9klEQVR42u3dTUrDUBSA0a6mKxAKnbkLcaLTzgTFTXUxD1yFU6eO7LAEqSY37y/vwJmmDfflI6SkyS59fgGd2hkBCBgQMCBgEDAgYEDAgIBBwICAAQGDgAEBAwIGBAwCBgQMCBgQMAgYGCzg74/n2ywJNBfwn90qGRoNeEG9Gob6AS9OV8ZQOeBV6tUwVAh4xXo1DEUDXr1eDYOAQcCV6tUwZA84a70aBgGDgAUMGwu4QL0aBgGDgAUMAhYwCBgELGAQsIBBwAIGAYOABQwCFjC4FxoQMAhYwLCVgJMnckDXASfPxAIBCxjqBJw8Fxq6Djh5MwN0HXDybiToOuBgxpYH6gecvB8Yug74/yVbEmg3YEDAgIBBwICAAQGDgAEBAwIGBAwCBgQMCBgEbAogYEDAgIBBwICAAQEDAgYBAwIGBAwCBgQMCBgQMAgYEDAgYEDAIGBAwICAQcCAgAEBAwIGAQMCBgQMCBgEDAgYEDAIGBAwIGDg94Dfz09ApwQMAgYEDAgYBAwIGOL2r8drBtJKwKf7PReTA3TWtiPMPzKf3ASMgAUsYAELWMACFnCDA7lNwK0E/Ph2iJisa/DTspp1gE62zRfww8tdmyYDqbszAhZwaM8FLGABC1jAAhawgIvUJeBtBhy5khSwgAXsDCxgAQtYwAIWsIAFXCbgWVcuAhawgJ2BBSxgAQtYwALO8UUC7oiABRwiYAELWMDjBhzcYQELWMACFnDBrpq65UvAAt5mwG3+aCxgAQtYwAJerdjgnScCFrCAnYEFLOBh/g8sYAELWMACFrCABSxgAQtYwAIWsIAFLGABC1jAAhawgAV8QMACFrCABSxgAQsYAQtYwAIWMAIWsIAFLGABC1jAAhawgAUsYAELWMACFrCAxSlgAQtYwAJGwAIWsIAFLOByAbOA+fc7fwEjYAELWMAI2AEkYPMfK2BAwCBgQMCAgAEBg4ABAQMCBgQMAgYEDAgYBAwIGBAwIGAQMCBgQMCAgEHAQBU/mxL2k2O7ASEAAAAASUVORK5CYII="
    }
  ]
}
```
