# Parchment User Guide

Parchment is a local-first terminal application for working with notes,
documents, spreadsheets, presentations, and images. Work with files at the
paths you choose; no account, workspace setup, or artifact library is needed.
This guide covers the workflows shown in the [example artifacts](../examples/).

## Install and open files

Parchment requires Go 1.24 or newer. From a clone of this repository:

```sh
go install ./cmd/parchment
```

Make sure `$(go env GOPATH)/bin` is on `PATH`, then check the command:

```sh
parchment --help
```

Open an existing file directly, or use `tui` to choose a kind when creating a
new file:

```sh
parchment ~/Documents/ideas.md
parchment tui ~/Documents/brief.md --kind document
```

If a file does not exist and no kind was specified, Parchment asks which kind
to create. Existing plain Markdown opens as a note and remains plain Markdown
when saved. Existing Parchment files open in the matching editor or reader;
PNG and JPEG images open in the image viewer/editor. Every operation addresses
the file by path. Parchment does not list, search, or delete files; use your
usual file manager or command-line tools for those tasks.

Use the corresponding `create` command to create an artifact without opening
the editor:

```sh
parchment note create ~/Documents/ideas.md --body "A thought to keep."
parchment document create ~/Documents/brief.md
parchment spreadsheet create ~/Documents/plan.md
parchment presentation create ~/Documents/tour.md
```

Create commands refuse to overwrite existing files. Run
`parchment <command> --help` for command-specific options.

## Notes

Notes are flat Markdown for quick capture. They do not have a document outline,
page layout, or document-only link navigation. The [note example](../examples/note.md)
demonstrates preview, search, and saved-file history.

```sh
parchment note show ~/Documents/ideas.md
parchment note edit ~/Documents/ideas.md --body "Updated Markdown."
parchment note print ~/Documents/ideas.md
parchment note print ~/Documents/ideas.md --format pdf -o ideas.pdf
```

In the note editor, `Ctrl+S` saves, `F5` toggles a rendered Markdown preview,
`Ctrl+F` finds text, and `Ctrl+Z` / `Ctrl+R` undo and redo. Preview is a view
of the source; it does not replace the Markdown saved in the file. `Ctrl+P`
exports the current text, including unsaved edits, to PDF.

## Documents

Documents add a reader, heading outline, clickable internal links, print
layout, embedded images, and review proposals to Markdown. These features are
Document-only: notes remain flat and presentations use slides instead. The
[document example](../examples/document.md) demonstrates Markdown, anchors,
layout, printing, and embedded media.

### Navigate and edit

An existing document opens in its reader, with an outline beside the rendered
pages. Use `↑` / `↓` to scroll across pages, `←` / `→` or `[` / `]` to change
pages, `{` / `}` to move between sections, and click an outline entry to jump.
Press `e` to edit, `v` to list proposals, `u` or `Ctrl+R` to undo or redo, and
`Esc` or `q` to leave the reader.

In the editor, `Ctrl+S` saves and `F5` previews the rendered pages. `Ctrl+O`
toggles the outline; the toolbar provides text formatting, page setup, and
save/proposal actions. `F2` and arrow keys navigate toolbar controls, and `F6`
focuses the outline. `F3` records the unsaved changes as a proposal; `F4`
opens proposal review. In the proposal list, `Enter` reviews a proposal and
`a` or `r` accepts or rejects it. A proposal does not change the live document
until accepted; only one can be pending per document, and accepting is refused
if the live document changed after the proposal was recorded.

### Layout, images, and interchange

Page and section break comments control pagination. Layout settings include
page size, orientation, margins, columns, headers, footers, page numbering, and
one document-wide print font (`courier`, `helvetica`, or `times`). Font choice
affects printer-oriented output, not the terminal display. Embedded images are
stored in the document file and referenced by ordinary Markdown links.

Use `<!-- parchment:page-break -->` for a page break and
`<!-- parchment:section-break columns=2 -->` for a two-column section; add
`continuous` to a section break to continue on the same page. Header and footer
text accepts `left|center|right` fields and the `{title}`, `{page}`, and
`{pages}` tokens.

```sh
parchment document print report.md
parchment document print report.md --format pdf -o report.pdf
parchment document layout report.md --page-size letter --orientation portrait --columns 2
parchment document export report.md -o report.docx
parchment document import draft.docx report.md
```

PDF output renders Markdown styling, links, layout, and embedded images.
Document import and export support the documented ODT and DOCX subsets and
warn when a conversion is lossy. `Ctrl+P` in the reader or editor exports a
PDF; the destination defaults to the last successful PDF destination, and
overwriting an existing file requires confirmation. In non-interactive CLI
use, pass `--force` to replace an existing PDF.

Proposal commands are also available through `parchment document propose`,
`review`, `accept`, and `reject`. A proposal can include Markdown or layout
changes. Add new image data with the regular document image command rather
than a proposal. `parchment document images` lists embedded assets;
`image-extract` and `image-remove` extract or remove them.

## Spreadsheets

A spreadsheet is a workbook with named sheets, literal cells, and explicit
formula cells. Text that looks like a formula is not evaluated unless saved as
a formula. The [budget example](../examples/budget.md) demonstrates multiple
sheets, ranges, aggregate functions, and cross-sheet formulas.

```sh
parchment spreadsheet create budget.md --csv-file budget.csv
parchment spreadsheet add-sheet budget.md "Work Plan"
parchment spreadsheet cell budget.md B2 12
parchment spreadsheet cell budget.md C2 '=B2*2' --formula
parchment spreadsheet show budget.md
parchment spreadsheet export budget.md --csv --sheet "Work Plan"
parchment spreadsheet print budget.md --format pdf -o budget.pdf
```

Formulas support numeric constants, same-sheet and cross-sheet A1 references,
rectangular ranges, parentheses, unary signs, `+`, `-`, `*`, `/`, nested
function calls, and `SUM`, `AVERAGE`, `COUNT`, `MIN`, and `MAX`. Empty
referenced cells evaluate to zero; formula errors can be stored and displayed.
Rows, columns, and sheets can be inserted or deleted; affected references
shift or become `#REF!` as appropriate.

In the grid editor, arrow keys select cells and `Enter` edits a cell. Changes
are buffered until `Ctrl+S`. `Shift+Enter` stages the current cell and starts
editing the cell below; if the terminal or multiplexer does not report that
key, use `Alt+Enter`. Insert a row or column with `i` then `r` or `c`; delete
a row, column, or sheet with `d` then `r`, `c`, or `s`. `u` / `Ctrl+Z` and
`Ctrl+R` undo and redo buffered edits before committed changes.

CSV import initializes literal text cells; CSV export writes one selected
sheet (the first sheet by default). The Markdown file is the canonical
workbook format, not a CSV file directly openable by spreadsheet applications.
Printing outputs all sheets as text or PDF, paging horizontally when needed;
PDF output includes cell gridlines. `Ctrl+P` in the editor exports the current
workbook, including unsaved edits.

## Presentations

Presentations use Markdown source with slides, speaker notes, and optional
embedded images. The [presentation example](../examples/presentation.md) is a
slideshow walkthrough of Parchment. It demonstrates file-first storage,
artifact types, editing and recovery, images, exports, and configuration.

The source follows Go `present` conventions: `#` introduces the deck title,
`##` begins a slide, `###` adds a subsection, `//` starts an ignored comment,
and `: ` begins a speaker note. Notes and comments remain in the source but
are omitted from the plain-text slide preview.

```sh
parchment presentation show talk.md
parchment presentation preview talk.md
parchment presentation print talk.md
parchment presentation print talk.md --format pdf -o talk.pdf
```

Printing defaults to a title page followed by one landscape page per slide,
with notes below the corresponding slide. `--slides-per-page 2` uses portrait
pages with two boxed slides; slides that cannot fit return an error rather than
being clipped. In the viewer or source editor, `Ctrl+P` exports to PDF;
`Tab` at the output-path prompt switches layouts. Presentations support
embedded-image management and Go `present` `.image` and `.code` directives.
The `presentation image`, `images`, `image-extract`, and `image-remove`
commands manage embedded assets.
The presentation-wide print font can be `courier`, `helvetica`, or `times`;
it does not affect terminal output.

## Images

Standalone PNG and JPEG files open in a native viewer/editor. Parchment can
also crop or resize them from the CLI:

```sh
parchment image crop photo.jpg --x 100 --y 50 --width 640 --height 480 -o cropped.jpg
parchment image resize photo.jpg --width 800 -o smaller.png
parchment image resize photo.jpg --width 800 --in-place --yes
```

Choose either `-o/--output` or `--in-place`. New files are created with
owner-only permissions. Replacing a different output requires `--force`;
in-place CLI edits ask for confirmation on a terminal and require `--yes` in
scripts. Crop coordinates are zero-based after JPEG orientation is normalized.
All crop dimensions are required; rectangles outside the image are rejected.
Resizing with one dimension preserves proportions, while two dimensions set
the exact size. Input format is detected from file contents, and output format
is selected by the `.png`, `.jpg`, or `.jpeg` extension. PNG transparency is
preserved; transparent JPEG pixels are composited over white.

The TUI viewer fits the image to the terminal using Kitty, Sixel, iTerm2, or an
ANSI half-block fallback. Use `c` for crop, `r` for resize, Tab to move between
fields, Enter to apply, and Esc to cancel. `u` undoes and `Ctrl+R` redoes
in-session edits. `Ctrl+S` saves in place; `s` saves to another image path.
Images do not have recovery drafts or persistent saved-file history. Inputs
are limited to 128 MiB and dimensions to 16,384 pixels per axis and 100
megapixels total. EXIF orientation is normalized before cropping; malformed
orientation metadata is rejected. Re-encoding drops metadata. JPEG output
quality is configurable from 1 to 100 (default 90); it does not affect PNG.

## Recovery, history, and configuration

Note, document, spreadsheet, and presentation editors save recovery drafts
under Parchment's application state, not beside the artifact. After an
unexpected exit, reopening the same path offers to recover or discard the
draft. Saving or closing normally removes it. Recovered saves detect external
changes rather than overwriting them.

Saved-file undo and redo persist across restarts. Inspect and manage history
with:

```sh
parchment history log ~/Documents/ideas.md
parchment history undo ~/Documents/ideas.md
parchment history redo ~/Documents/ideas.md
parchment history restore ~/Documents/ideas.md <step-id>
parchment history clean ~/Documents/ideas.md
parchment history clean --all
parchment history gc
```

Parchment creates `~/.parchment/parchment.toml` on first use. Set
`PARCHMENT_CONFIG` to use a different existing configuration file. Settings
use TOML with `version = 1`, then optional values such as:

```toml
version = 1
undo_limit = 100

[logging]
level = "warn"
format = "text"

[theme]
mode = "auto"

[backup.local]
destination = ""
```

Environment overrides include `PARCHMENT_UNDO_LIMIT`, `PARCHMENT_THEME`,
`PARCHMENT_LOG_LEVEL`, `PARCHMENT_LOG_FORMAT`, and
`PARCHMENT_BACKUP_DESTINATION`. Theme mode accepts `auto`, `light`, or
`dark`; optional six-digit hex overrides include `primary`, `border`, `error`,
`selection`, and `match`. Logging level accepts `debug`, `info`, `warn`,
`error`, or `off`, and format accepts `text` or `json`. Local backup snapshots
are disabled unless a destination is configured. See the
[Technical Guide](Technical_Guide.md) for the file format and application-state
boundaries.
