# Parchment

`parchment` is a local-first productivity application for the terminal. Each
note, document, spreadsheet, or presentation is one ordinary Markdown file that
you name and keep wherever you like, such as `~/Documents`, next to your other
files. Parchment never manages a directory of artifacts. Its files can be
opened with Parchment's command-line interface and interactive editor, or with
any text editor. Requires Go 1.24 or newer.

## Open a file

```sh
go install ./cmd/parchment
parchment note create ~/Documents/ideas.md --body "Markdown stays readable on disk."
parchment note show ~/Documents/ideas.md
parchment ~/Documents/report.md
parchment examples/budget.md
```

Every command takes the artifact's file path; there is nothing to initialize.
`create` refuses to overwrite an existing file. Run in a terminal, `create`
then opens the new file in its editor; pass `--no-edit` to only create it.
When output is piped or redirected, as in scripts, `create` prints the new
file's path instead. An artifact's title is its file
name; titles and IDs are not stored in the file. The first run creates
`~/.parchment/parchment.toml`. Use `parchment --help` and
`parchment note --help` for the complete command tree.

Note operations are `note create`, `show`, and `edit`. Plain Markdown files
without Parchment metadata can be shown, edited, and opened as notes, and stay
plain Markdown when saved. Parchment does not list, search, or delete files: use your
file manager, shell, or tools such as `ls`, `grep`, and `rm` for that, as
with any other file.

## Storage

Every artifact is one Markdown file at the path you choose; Parchment adds no
sidecar files next to it. Writes go to a temporary file in the same directory,
then sync and rename, preserving the file's permissions; edits made in another
editor since Parchment loaded the file are detected rather than overwritten.
The file begins with a `parchment-meta` fenced JSON block containing its kind,
format version, creation time, and modification time (no title, ID, location,
or tags). The editable Markdown body follows a required
`<!-- parchment-body -->` comment, so a body can safely begin with a reserved
code fence. Structured artifact data uses additional `parchment-<thing>` JSON
code blocks after the body, behind an explicit `<!-- parchment-blocks -->`
marker, so large payloads such as images come after the readable content. Parchment
renderers hide these reserved blocks while ordinary Markdown remains readable.
Metadata timestamps are UTC RFC 3339 values. Back up, copy, move, or version
artifacts with the same tools you use for any other file.

Initial artifact metadata recognizes the `note`, `document`, `spreadsheet`,
`presentation`, and `image` kinds. Notes, documents, spreadsheets, and basic
presentations have application behavior; image editing is not yet implemented.

### Documents

Documents are a separate feature from notes, with multi-page printing in mind.
The visible body is canonical Markdown. A hidden `parchment-document` JSON
block stores page layout, embedded image bytes, and proposal history in the
same artifact file. Embedded image references use ordinary
Markdown links. Page and section breaks are HTML comments so other Markdown
tools ignore them: `<!-- parchment:page-break -->` and
`<!-- parchment:section-break columns=2 [continuous] -->`. Headers and footers
are `left|center|right` text with `{title}` (the file name), `{page}`, and
`{pages}` tokens.

Printing renders monospaced pages (10 characters and 6 lines per inch) with
margins, columns, headers, footers, and page numbers, separated by form feeds:

```sh
parchment document create report.md --body-file draft.md --columns 2 --footer "{title}|{page}/{pages}"
parchment document print report.md | lpr
```

Document font selection, when implemented, applies to the whole document, not
individual sections, and only to printer-oriented rendering. It cannot change
the font used by the TUI or terminal previews, which use the font configured
in the user's terminal emulator.

Document commands (`parchment document`, alias `doc`): `create`, `show`,
`edit`, `layout`, `page-break`,
`section-break`, `image`, `print`, `propose`, `changes`, `review`, `accept`,
and `reject`. `propose` records a Markdown or layout edit without changing the live document; `review` displays the
current and proposed Markdown, and `accept` or `reject` resolves the pending
proposal. Only one proposal may be pending per document, and a proposal cannot
be accepted if the live document has changed since it was recorded. For
proposals, existing embedded images can be kept or removed, but new image data
must be added with the regular `image` command. For example:

```sh
change=$(parchment document propose report.md --body-file revised.md --description "Revise introduction")
parchment document review report.md "$change"
parchment document accept report.md "$change"
```

Proposal history is stored as inspectable JSON in each document's hidden
`parchment-document` block. In the interactive editor
(`parchment report.md`), `F3` records your unsaved edits as a proposal
instead of saving them and returns the editor to the saved document; `F4`
lists the file's proposals, `Enter` reviews one, and `a` or `r` accepts or
rejects it. Accepting is refused while the editor has unsaved edits.
The document editor always shows a toolbar above the text with buttons for
save, propose, changes, preview, close, text formatting (bold, italic,
strikethrough, code, headings, lists, quote, link, rule, image) and page setup
(page and section breaks, columns, margins, page size, orientation, header,
footer, page numbers). Click a button, press `F2` and use the arrow keys with
`Enter`, or use the `Alt` shortcuts (`B` bold, `I` italic, `C` code, `1`–`3`
headings, `L`/`N` lists, `Q` quote, `K` link, `M` image, `P` page break, `S`
section break). `F5` previews the printed pages and `Ctrl+S` saves. Document
changes, including images, support undo and redo like notes.

### Spreadsheets

Spreadsheets are stored as one Markdown file per workbook. The hidden `parchment-spreadsheet`
JSON block contains the versioned workbook data, named sheets, two-dimensional
cell arrays, and explicit literal or formula cells. This keeps the workbook
self-contained and avoids formulas being inferred from arbitrary text. CSV
files can initialize a workbook; CSV cells are imported as literal text. A new
workbook starts with `Sheet1`; additional sheets can be created with
`spreadsheet add-sheet`.

```sh
parchment spreadsheet create budget.md --csv-file budget.csv
parchment spreadsheet cell budget.md B2 12
parchment spreadsheet cell budget.md C2 '=B2*2' --formula
parchment spreadsheet cell budget.md C2
parchment spreadsheet show budget.md
```

Formula evaluation currently supports numeric constants, same-sheet A1 cell
references, parentheses, unary signs, and `+`, `-`, `*`, and `/`. Empty
referenced cells evaluate to zero. Cycles, non-numeric references, and
division by zero are rejected. Formulas are limited to 4096 bytes and 512
nested dependencies or parentheses. Rows and columns can be inserted with
`spreadsheet insert-row` and `spreadsheet insert-column`; formula references
shift with inserted rows and columns. Spreadsheet edits can be undone and
redone through the service API while its process is running; the CLI is
stateless across invocations. The Markdown file with its JSON data block is
Parchment's canonical format, not a CSV file that third-party spreadsheet
applications can open directly.

### Presentations

A presentation is stored as one Markdown text file. Its leading `parchment-meta` block
embeds the shared artifact metadata; the rest is editable Markdown. The initial
syntax follows Go present's Markdown conventions: `#` gives the deck title,
`##` begins a slide, `###` adds a subsection, `//` begins an ignored comment,
and `: ` begins a speaker-note line. Speaker notes and comments remain in the
source but are omitted from the plain-text preview.

```markdown
# Product Update

Presenter Name

## What changed

- Faster search
- Local-first storage

: Mention the upcoming release date.

## Questions

Thank you.
```

Use `presentation create talk.md --body-file slides.md` to create a deck, `presentation show talk.md` to
inspect its source, and `presentation preview talk.md` for a slide-separated
plain-text preview. `presentation edit talk.md --body-file slides.md` replaces
the Markdown source.
The parser preserves Markdown for later rendering but does not yet render it;
embedded media and Go present command directives are not implemented.
Presentation font selection, when implemented, applies to the whole
presentation, not individual sections. It applies only to rendered output,
never to terminal previews.

## Examples

The [`examples/`](examples/) directory contains complete Markdown artifact
files for notes, documents, spreadsheets, and presentations, which Parchment
can open in place; the document's image data is embedded in its hidden
payload.

## Configuration

`~/.parchment/` holds only Parchment's own files: configuration, a write lock,
and autosave recovery drafts. It never contains artifacts. Configuration
is read from `~/.parchment/parchment.toml`, which is created with owner-only
permissions on first use. Set `PARCHMENT_CONFIG` to read a different, existing
configuration file. The file is TOML and requires `version = 1`. For example:

```toml
version = 1
undo_limit = 100

[logging]
level = "warn"
format = "text"

[backup.local]
destination = ""
```

Settings are merged from built-in defaults, the configuration file, then
environment overrides. `PARCHMENT_UNDO_LIMIT` overrides the configured undo limit. Editor, theme,
logging, and backup settings are parsed but are not yet connected to runtime
behavior, and their environment variables currently have no effect.

## Interactive editor

`parchment <file>` (or `parchment tui <file>`) opens one note, document,
spreadsheet, or presentation in a full-screen editor. If the file does not
exist, Parchment creates it: pass `--kind note|document|spreadsheet|presentation`
to `parchment tui`, or choose `n`, `d`, `s`, or `p` when asked (`q` quits
without creating anything). Mouse clicks position the text cursor in the note
and document editors and select cells and slides.

In the note editor, `Ctrl+S` saves
and keeps editing, `F5` toggles a plain-text preview (`↑`/`↓` scroll), and
`Ctrl+Z`/`Ctrl+R` undo and redo edits seamlessly.
`Esc` or `Ctrl+C` closes the editor, asking for confirmation when there are
unsaved changes. `Ctrl+C` during a save cancels the save and keeps the editor
open. Very small terminals show a minimum-size message; `q` then quits only
when there are no unsaved changes. Preview is plain text rather than rendered
Markdown, so canonical Markdown is never confused with presentation.

An existing document opens in a reader: an outline of its headings sits beside
the rendered printed pages (only the pages on narrow terminals). `↑`/`↓` scroll
and continue onto the neighbouring page, `←`/`→` or `[`/`]` change pages,
`{`/`}` move between sections, clicking an outline entry jumps to it, `v`
lists proposals, `u`/`Ctrl+R` undo and redo, and `Esc` or `q` quits. Press `e` to edit;
`Esc` in the editor returns to the reader, asking first when there are unsaved
changes. New documents and restored drafts open directly in the editor.

Spreadsheets open as a grid: use arrow keys to select a cell, `Enter` to edit,
`=` to enter a formula, and `u`/`Ctrl+R` for undo/redo. Presentations open as
slides with an editable Markdown source. Press `Esc` or `q` to quit either viewer.

While editing a note or document, Parchment autosaves recovery drafts every couple of seconds to
`~/.parchment/recovery/`, never next to the artifact. Saving, proposing, or
closing the editor normally (for a document, returning to its reader) removes
the draft. If Parchment exits
unexpectedly, reopening the same file offers the draft: `r`
recovers it into the editor, `d` discards it, and `q` quits and keeps it for
later. If the file changed after the draft was written, saving the recovered
draft reports a conflict instead of overwriting those changes.

Spreadsheet and presentation edits are not yet autosaved as recovery drafts.

Undo and redo cover edits made in the running process; creating a file is not undoable. The bounded history is in memory and
does not survive a restart.

## Future work

Parchment supports notes, documents, basic spreadsheets, and Markdown
presentations. Planned work is organized into five implementation phases:

### Phase 1: Foundations & Bug Fixes
- Notes editor terminal height and width allocation (leaves an unused bottom row and 8-column right margin; fix resize calculation)
  - **Research/Decisions needed:** Should we leave a 1-column margin for visual breathing room, or strictly use 100% width? Should the bottom row be reserved for a future status bar?
- Connect configuration settings to runtime behavior
  - Respect configured theme (`theme` / `PARCHMENT_THEME`) across all TUI screens instead of hardcoding colors
    - **Research/Decisions needed:** What theme structure should we use? (e.g., predefined named themes like "light" and "dark", or custom user-defined Lip Gloss style maps?) Should we support 256 colors, true color, or dynamically adapt?
  - Implement file logging for `logging.level` and `logging.format` (`PARCHMENT_LOG_LEVEL`, `PARCHMENT_LOG_FORMAT`) under `~/.parchment/`
    - **Research/Decisions needed:** Should we use the standard `slog` package or a third-party library? What is the log rotation policy (size-based, time-based, or keep all)?
  - Implement local backup destination (`backup.local.destination`) for automatic artifact snapshots on save
    - **Research/Decisions needed:** What is the retention policy for these snapshots (e.g., keep last 10, keep by date)? Should snapshots be compressed (e.g., gzip) to save space, or kept as plain Markdown?
- Dedicated text editor widget with micro-style text selection (Shift+arrows, mouse drag, double/triple click, clipboard copy/cut/paste, replacement typing) and standard key combinations (Ctrl+arrows for word navigation, Ctrl+del/Ctrl+backspace for word deletion, Tab for spaces)
  - **Research/Decisions needed:** Should we build this from scratch, fork `bubbles/textarea`, or write a completely custom rune buffer? For clipboard integration, what library should we use (e.g., `atotto/clipboard`) to ensure cross-platform compatibility? How should wide characters (CJK) and emojis be handled in terms of visual width vs. string length?

### Phase 2: State Safety & CLI Core
- Autosave recovery drafts for the spreadsheet and presentation editors
  - **Research Task**: Should autosave be triggered periodically by a timer or by specific edit events? What format should the recovery draft use for spreadsheets and presentations? How should concurrent modifications by external tools be handled during recovery?
- Explicit, opt-in conversion of a plain Markdown file into a Parchment artifact (plain Markdown currently opens and saves as a plain note; default to Document conversion)
  - **Research Task**: What is the ideal UX for conversion (e.g., TUI prompt upon opening, or explicit CLI command)? If plain Markdown currently opens as a Note, how does a user opt-in to Document conversion? Should the system automatically back up the original plain Markdown file before conversion?
- Spreadsheet deletion & CSV export: row and column deletion (`delete-row`, `delete-column`) and sheet removal with formula reference shifting and `#REF!` error handling; CSV export command (`spreadsheet export --csv`)
  - **Research Task**: When deleting a sheet, how exactly are cross-sheet formula references invalidated (e.g., cascading `#REF!` errors)? How are `#REF!` errors represented in the `parchment-spreadsheet` JSON block? How should the `spreadsheet export --csv` command handle multi-sheet workbooks (e.g., zip archive, multiple files, or requiring a specific sheet flag)?
- Document embedded image management: commands to list embedded images, view metadata, extract image files to disk, and remove unused images
  - **Research Task**: What image metadata should be stored and displayed (e.g., original filename, dimensions, file size)? How is an "unused" image reliably detected (e.g., parsing Markdown links vs. handling commented-out links)? Should unused images be pruned automatically upon saving, or only via an explicit user command?

### Phase 3: Rendering & TUI Features
- Research and plan overall visual improvements
  - How will Glamour be integrated for rich Markdown rendering in previewers? What styles/themes will be supported? How do we map Parchment's custom styles to Glamour's stylesheet?
  - For image rendering, what library will be used to detect and support Kitty/Sixel/iTerm2 protocols? How do we handle terminal window resizing for graphical images, and what is the exact fallback behavior for ANSI half-blocks?
  - How should app components (like "Ready" status and undo/redo) be visually separated from artifact content? Options include adding borders (using lipgloss borders), distinct background colors, or specific horizontal rule separators.
- Research and plan document navigation pane and in-document link navigation (strictly for Documents; does not apply to Notes or Presentations)
  - How will the layout split be managed for the navigation pane (e.g., fixed width vs percentage)?
  - How do we handle narrow terminals where both the editor and navigation pane cannot fit? Should it be a toggleable overlay or auto-hide?
  - For clickable inline Markdown links, how do we track screen coordinates to map mouse clicks to link targets in the terminal?
- Research and plan in-editor search and find across all artifact viewers and editors (notes, documents, spreadsheets, presentations)
  - Where will the search UI be located (e.g., floating modal, bottom bar, top bar)?
  - How will matches be highlighted in the text buffer without permanently mutating the underlying text, especially across different editor components?
  - Do we need support for regex, case-insensitivity, and whole-word matching?
- Research and plan document and presentation printer font selection for printer-oriented rendering
  - What format will font selection take (e.g., CSS font-family strings, specific font files)?
  - How will the fonts be loaded or mapped during the printing/PDF generation process since they cannot be applied to terminal output?

### Phase 4: Extended Domain Engines (Research)
- Research and design the dedicated spreadsheet formula AST and evaluation engine, evaluating whether to build a custom AST or use an external library.
- Research the toolchain and approach for presentation media, directives, and exporting to standalone slide formats (HTML slides, PDF).
- Research basic image-editing features (crop, resize) and evaluate Go standard library limitations versus third-party libraries.
- Dedicated spreadsheet formula AST and evaluation engine supporting rectangular ranges (`A1:B10`), aggregate functions (`SUM`, `AVERAGE`, `COUNT`, `MIN`, `MAX`), nested function calls, cross-sheet references (`Sheet2!A1`), and standard error propagation (`#REF!`, `#DIV/0!`, `#NAME?`, `#VALUE!`, `#CIRCULAR!`)
  - *Parser implementation*: Hand-written recursive descent vs. parser generator (e.g., `goyacc` or `participle`)?
  - *Evaluation strategy*: Dependency graph for targeted recalculation vs. eager evaluation?
  - *Formula syntax details*: How to handle strings, quoting, and locale-specific argument separators?
- Presentation media, directives, and export
  - Support embedded images in slide decks
    - *Storage*: Should images be stored in a hidden `parchment-presentation` JSON block, mirroring the approach used for Documents?
    - *Syntax*: Use standard Markdown image syntax (`![alt](ref)`) mapped to the embedded data?
  - Support Go present command directives (e.g. `.image`, `.code`)
    - *Compatibility*: Full emulation of `golang.org/x/tools/present` parser or a targeted subset?
    - *Security*: How to handle `.code` local file inclusions securely, or should it only reference embedded blocks?
  - Export presentations to standalone slide formats (HTML slides, PDF)
    - *HTML Export*: Use an existing framework (e.g., `reveal.js`) or a custom, lightweight CSS grid?
    - *PDF Export*: Native Go PDF library (e.g., `gofpdf`) or headless browser dependency?
- Basic image-editing features for files opened directly by Parchment (crop, resize)
  - *Interface*: Interactive TUI (using Kitty/Sixel protocols for preview) vs. CLI flags?
  - *Backend*: Standard library (`image`, `image/draw`) vs. `golang.org/x/image/` extensions?
  - *Persistence*: Are edits destructive, or is the original image preserved as a backup?

### Phase 5: External Interop & Persistence
The planning documents for persistent undo/redo storage format and external interop formats (ODF, DOCX) can be found in the `docs/` directory:
- [ODF / DOCX Import & Export Plan](docs/ODF_DOCX_Interop_Plan.md)
- [Persistent Undo/Redo Storage Plan](docs/Persistent_Undo_Redo_Plan.md)

- Make a plan for importing and exporting to/from common document file formats (ODF, DOCX; lossy conversion reporting unsupported features)
  - **Tooling:** What library or external tool will be used for parsing and generating ODF and DOCX formats (e.g., native Go library, Pandoc integration, or LibreOffice headless)?
  - **Mapping:** How will we map between Parchment's Markdown + custom metadata structures and the rich text features of ODF/DOCX?
  - **Lossy Conversion UI:** How should unsupported features or lossy conversions be reported to the user (e.g., summary dialog, inline warnings, side-by-side comparison)?
  - **Scope:** Do we also plan to export spreadsheets (XLSX, ODS) and presentations (PPTX, ODP), or restrict this to documents?
- Make a plan for persistent undo/redo storage format under `~/.parchment/`, with explicit storage and recovery semantics (planning only)
  - **Storage Format:** What format should the undo/redo history take (e.g., JSON patches, append-only log, SQLite database)?
  - **File Identity:** Since Parchment artifacts are regular files that lack stable UUIDs and can be renamed or moved, how do we reliably link an undo/redo log in `~/.parchment/` to its corresponding file on disk (e.g., file path combined with content hashing)?
  - **Garbage Collection:** What is the lifecycle policy for these history files to prevent `~/.parchment/` from growing indefinitely?
  - **Concurrency:** How do we handle multiple Parchment processes attempting to read/write the undo/redo history for the same artifact simultaneously?
  - **Payload Size:** Should large embedded assets (like images) be duplicated in the history log when they are added or removed, or is there a reference-based approach?

## Development

```sh
go build ./cmd/parchment
go test ./...
```

The module path `example.com/parchment` is a placeholder until the project
selects a canonical module path.
