# Artifact examples

These four complete Markdown artifacts use Parchment itself as their subject:

- `note.md` demonstrates quick, flat note-taking, Markdown preview, search,
  and saved-file history.
- `document.md` demonstrates Markdown, document-only outline and link
  navigation, page layout, printing, embedded media, and a line chart
  sourced from an embedded sheet.
- `budget.md` is a multi-sheet planning workbook with ranges, aggregate
  formulas, and cross-sheet references.
- `presentation.md` is a slide walkthrough of Parchment's file-first model,
  artifact types, editing, recovery, history, image workflows, and exports.
  Its final slide displays a bar chart whose sheet is in the speaker notes.

Each file can be opened directly, without setup:

```sh
parchment examples/note.md
parchment examples/document.md
parchment examples/budget.md
parchment examples/presentation.md
```

Inspect or export the samples with the matching CLI commands:

```sh
parchment document print examples/document.md --format pdf -o /tmp/guide.pdf
parchment spreadsheet show examples/budget.md
parchment spreadsheet export examples/budget.md --csv --sheet "Work Plan"
parchment presentation preview examples/presentation.md
parchment presentation print examples/presentation.md --format pdf -o /tmp/tour.pdf
```

A leading `parchment-meta` block contains shared metadata. The editable
Markdown body follows `<!-- parchment-body -->`; structured blocks such as
document layout, workbook cells, and embedded image bytes follow
`<!-- parchment-blocks -->`. The document and presentation images are embedded
in their respective hidden JSON payloads, keeping each artifact in one file.
Their sheets and charts live in each artifact's `parchment-workbook` block.
In the document or presentation source editor, place the cursor on a sheet
reference and press `Ctrl+Shift+E` to open the sheet. The examples identify
the chart's anchor cell; changing source cells and saving updates the chart.
See the [User Guide](../docs/User_Guide.md) for workflows and the
[Technical Guide](../docs/Technical_Guide.md) for the complete file format.

Edits save to the opened file. Copy an example to another path before
experimenting if you want to keep the checked-in sample unchanged.
