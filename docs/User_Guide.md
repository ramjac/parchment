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
layout, embedded images, and optional change tracking to Markdown. These features are
Document-only: notes remain flat and presentations use slides instead. The
[document example](../examples/document.md) demonstrates Markdown, anchors,
layout, printing, and embedded media.

### Navigate and edit

An existing document opens in its reader, with an outline beside the rendered
pages. Use `↑` / `↓` to scroll across pages, `←` / `→` or `[` / `]` to change
pages, `{` / `}` to move between sections, and click an outline entry to jump.
Press `e` to edit, `v` to list tracked changes, `u` or `Ctrl+R` to undo or redo, and
`Esc` or `q` to leave the reader.

In the editor, `Ctrl+S` saves and `F5` previews the rendered pages. `Ctrl+O`
toggles the outline; the toolbar provides text formatting, page setup, and
save and tracking actions. `F2` and arrow keys navigate toolbar controls, and
`F6` focuses the outline. `F3` toggles tracking for future edits; save or
discard unsaved edits before toggling. `Ctrl+S` saves the current Markdown,
including insertions and excluding deletions. `F4` lists saved pending changes:
press `Enter` to review, then `a` to accept or `r` to reject. In the list,
`A` accepts all and `R` rejects all. Save or discard unsaved edits before
reviewing. A stale save or review fails rather than overwriting external edits.

Tracked changes record Markdown text, including image references, not page
layout or image bytes. Overlapping edits coalesce; nearby changes and very
large replacements may be reviewed together. Image-reference moves are
reviewed together when their removal and insertion are paired. Embedded bytes
remain in the file while pending changes might need them. Turning tracking off
does not resolve pending changes; untracked edits that intersect them are
rejected until they are reviewed. At most 4096 changes and 16 MiB of tracked
text are stored per document.

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

Import and export support ODT and DOCX. The format is inferred from the file
extension; `--format odt|docx` can specify it explicitly. Import refuses to
overwrite an existing Markdown file and opens the result for editing in an
interactive terminal unless `--no-edit` is passed. Export requires `-o` and
refuses to overwrite an existing destination unless `--force` is passed.

Conversion is intentionally lossy, not a full-fidelity office document
round-trip. The supported subset includes common Markdown text and formatting,
lists, blockquotes, code blocks, tables, links, page/section breaks, basic page
layout, headers/footers, and embedded PNG, JPEG, or GIF images. Unsupported or
approximated features generate warnings; geometry is rounded or mapped to
supported page sizes, and per-section fonts collapse to one document font.
External images and unsupported image formats are skipped with a warning.
Export uses the current document body and warns when pending review metadata is
omitted. The same applies to Markdown output, printing and PDF.

PDF output renders Markdown styling, links, layout, and embedded images.
`Ctrl+P` in the reader or editor exports a PDF; the destination defaults to
the last successful PDF destination, and overwriting an existing file
requires confirmation. In non-interactive CLI use, pass `--force` to replace
an existing PDF.

Use `parchment document tracking report.md on|off` to change the persistent
setting. CLI body-changing commands accept `--track` or `--track=false`;
when tracking is enabled, the choice is required explicitly. For example:

```sh
parchment document edit report.md --body "Revised text" --track
parchment document changes report.md
parchment document review report.md CHANGE_ID
parchment document reject report.md CHANGE_ID
parchment document accept report.md --all
```

`parchment document images` lists embedded assets; `image-extract` and
`image-remove` extract or remove them.

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
function calls, comparisons (`=`, `<>`, `<`, `<=`, `>`, `>=`), and
`SUM`, `AVERAGE`, `COUNT`, `MIN`, `MAX`, `COUNTA`, `ROUND`, `IF`, and
`IFERROR`, `SUMIFS`, `COUNTIFS`, `TEXTJOIN`, `INDEX`, `MATCH`, `XMATCH`,
`XLOOKUP`, `DATE`, `YEAR`, `MONTH`, `DAY`, `TODAY`, `NOW`, `FILTER`, `SORT`,
`UNIQUE`, and `SEQUENCE`. `IF` and
`IFERROR` evaluate only the branch they need. `COUNTA`
counts nonempty cells, including formulas that return an empty string.
`ROUND(number, digits)` rounds half away from zero. Dates use serial days
from 1899-12-30, without a fictitious 1900 leap day. `DATE(year, month, day)`
normalizes overflowing months and days; years must be 1 through 9999.
`TODAY()` and `NOW()` use UTC, with one time snapshot per recalculation.
Empty
referenced cells evaluate to zero; formula errors can be stored and displayed.
`SUMIFS(sum_range, range, criterion, ...)` and
`COUNTIFS(range, criterion, ...)` require equally sized ranges. Criteria are
case-insensitive and support comparison prefixes and `*`/`?` wildcards (`~`
escapes a wildcard). `TEXTJOIN(delimiter, ignore_empty, value, ...)` joins
values and ranges in row-major order.
`INDEX(range, row, column)` uses one-based coordinates (`column` defaults to
1). `MATCH(value, range, mode)` supports exact (`0`), exact-or-smaller (`1`,
the default), and exact-or-larger (`-1`) matching. `XMATCH` and `XLOOKUP`
default to exact matching; their match modes `-1`, `1`, and `2` mean
exact-or-smaller, exact-or-larger, and wildcard. Their search mode is `1`
(forward) or `-1` (reverse); binary search is not supported. Lookup ranges
must be one-dimensional, and `XLOOKUP`'s return range must have the same
shape. Optional arguments must be supplied in order.
`SEQUENCE(rows, columns, start, step)` defaults its last three arguments to 1.
`FILTER(array, include, if_empty)` filters rows or columns using a matching
one-dimensional condition. `SORT(array, index, order, by_column)` defaults
to the first column, ascending rows. `UNIQUE(array, by_column, exactly_once)`
defaults to distinct rows. Array results spill into empty neighboring cells;
only the formula anchor is stored. A blocked or out-of-bounds result shows
`#SPILL!`, and spill cells cannot be edited directly.
Rows, columns, and sheets can be inserted or deleted; affected references
shift or become `#REF!` as appropriate. References support absolute and mixed
axes (`$A$1`, `$A1`, `A$1`) when formulas are copied.

In the grid editor, arrow keys select cells and `Enter` edits a cell. Changes
are buffered until `Ctrl+S`. `Shift+Enter` stages the current cell and starts
editing the cell below; if the terminal or multiplexer does not report that
key, use `Alt+Enter`. Insert a row or column with `i` then `r` or `c`; delete
a row, column, or sheet with `d` then `r`, `c`, or `s`. `u` / `Ctrl+Z` and
`Ctrl+R` undo and redo buffered edits before committed changes.
Press `v` to start selecting a range, move to its opposite corner, and press
`y` to copy. Press `y` without a selection to copy one cell. Move to the
destination and press `p` to paste the internal copy with relative references
adjusted. Copy also writes tab-separated text to the system clipboard when
available. Press `P` to paste system clipboard text as literal cells, including
text beginning with `=`; it never becomes a formula. `Esc` cancels a selection.

To make one cell span a rectangular area, select the range with `v` and press
`m`, or press `Enter` to span it and edit the top-left cell immediately.
Cancelling that edit leaves the span in place. The top-left cell keeps its
value or formula; the other cells must be empty and cannot overlap another
span or an array spill. Select a spanned cell
and press `M` to split it. Clicking anywhere in the span selects its top-left
cell, and arrow keys move past the span. Covered coordinates cannot be edited;
formulas referencing them evaluate them as empty. Inserting or deleting rows
or columns adjusts spans, preserving the anchor when part of a span remains.
Spans participate in undo, recovery, printing and PDF. CSV and system clipboard
text retain the anchor value and empty covered cells, but not span formatting.

The CLI can also create or split spans:

```sh
parchment spreadsheet span budget.md A1:B3
parchment spreadsheet unspan budget.md A1
```

Select an empty cell and press `g` to create a chart there. For a larger
chart, select an empty rectangle with `v` and press `g` to span it and open
the chart editor. You can also press `m` to span the rectangle first, then
press `g` on the span. Cancelling the chart editor leaves the span in place.
Press `g` or `Enter` on an existing chart to edit it;
`Delete` removes it after confirmation without splitting its span. A cell
containing data, a formula, or a spill cannot be replaced by a chart.
The form shows its type, source sheet and range, header row,
label column, and title alongside a live preview and the read-only spreadsheet.
Use `Tab` / `Shift+Tab` to move between fields, and arrows or Space to change
the type and yes/no choices. `Ctrl+S` stages the chart and `Esc` cancels.
On small terminals, the grid may be hidden and the form scrolls to the focused
field; use `PgUp` / `PgDn` to scroll a long preview or error. A chart can be
staged even if its data cannot currently be previewed. Chart changes are
buffered and saved with the workbook. In the grid, a chart that is too small
to draw displays an indicator. To enlarge an existing span, split it with `M`
and then span a larger rectangle from the same anchor.

Charts are live definitions bound to a rectangular range. Bar charts use one
numeric series; line charts support multiple numeric series. Optional headers
supply series names and an optional first column supplies category labels.
Missing or nonnumeric values are errors. Deleting a source preserves an invalid
chart for editing or deletion. A reference to a chart cell evaluates as empty.
Internal copy preserves charts and their source references; CSV and system
clipboard text do not carry charts. Text printing uses a chart placeholder;
workbook PDFs draw charts in their cells. Standalone exports are generated on
demand, refuse overwrite unless forced, and cannot overwrite the workbook.

```sh
parchment spreadsheet chart create budget.md C1 bar A1:B5 --header --labels --title "Budget"
parchment spreadsheet chart edit budget.md C1 --title "Updated budget"
parchment spreadsheet chart export budget.md C1 --format svg -o budget-chart.svg
parchment spreadsheet chart export budget.md C1 --format pdf -o budget-chart.pdf
```

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
Slide navigation animates between text-only slides; slides with embedded images
and terminals smaller than 40×10 switch immediately.

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
ANSI half-block fallback. Generic `xterm` terminal types use the ANSI fallback
because they do not reliably indicate Sixel support; explicit Sixel terminal
types, `foot`, and `mlterm` use Sixel. Use `c` for crop, `r` for resize, Tab to move between
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
boundaries, including persistent history storage.
