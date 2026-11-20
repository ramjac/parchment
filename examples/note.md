```parchment-meta
{
  "parchment_format": "parchment-single-file-v2",
  "kind": "note",
  "format_version": 2,
  "created_at": "2026-01-15T09:00:00Z",
  "modified_at": "2026-11-20T00:18:19.924Z"
}
```

<!-- parchment-body -->

# Parchment launch notes

Parchment keeps this note as an ordinary Markdown file at the path I chose.
I can edit it in Parchment or another text editor, then reopen the same file.

Today I tried the rendered preview and the built-in editor:

- `Ctrl+S` saves without leaving the note.
- `F5` previews Markdown; the preview never replaces the source text.
- `Ctrl+Z` and `Ctrl+R` undo and redo changes.
- `Ctrl+F` finds text in the note.

The note stays intentionally lightweight: it is not a document outline or a
collection entry. For page layout, embedded images, and document-only link
navigation, use a Parchment document instead.

Next:

- [ ] Copy an example before trying edits I want to keep.
- [ ] Open `examples/document.md` and test its outline and print layout.
- [ ] Open `examples/budget.md` and inspect the formulas on each sheet.
- [ ] Present the walkthrough in `examples/presentation.md`.

Useful commands:

```sh
parchment examples/note.md
```

After saving changes to a file, inspect or undo its saved history with
`parchment history log <file>` and `parchment history undo <file>`.