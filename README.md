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
environment overrides. `PARCHMENT_UNDO_LIMIT` overrides the configured undo limit. Theme,
logging, and backup settings are not yet connected to runtime
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
presentations. Detailed PR-by-PR implementation plans are in
[`FutureWork.md`](FutureWork.md), [`docs/ODF_DOCX_Interop_Plan.md`](docs/ODF_DOCX_Interop_Plan.md),
and [`docs/Persistent_Undo_Redo_Plan.md`](docs/Persistent_Undo_Redo_Plan.md).
Planned work is organized into five sequential implementation phases broken down
into **38 reviewable Pull Requests (`PR 1`–`PR 38`)**:

### Phase 1: Foundations & Bug Fixes (PRs 1–6)
- **PR 1:** Fix bordered content sizing in the note editor (and Lip Gloss outer border dimensions `width-2` / `height-chrome-2` so borders never overflow terminal bounds)
- **PRs 2–4:** Connect configuration settings (`version = 1`) to runtime behavior, rejecting invalid configuration values on load
  - **PR 2:** Implement `log/slog` file logging for `logging.level` (`debug`, `info`, `warn`, `error`, `off`) and `logging.format` (`text`, `json`) via `PARCHMENT_LOG_LEVEL` and `PARCHMENT_LOG_FORMAT` at `~/.parchment/parchment.log` (`0600`, capped at `5 MiB` with single-file rotation to `parchment.log.1`)
  - **PR 3:** Respect configured `[theme]` (`mode = "auto"|"light"|"dark"` overridden by `PARCHMENT_THEME`, plus optional hex overrides `primary`, `border`, `error`, `selection`, `match`) across all TUI screens instead of hardcoding colors
  - **PR 4:** Implement local backup snapshots (`backup.local.destination`, defaulting to `""` disabled, overridden by `PARCHMENT_BACKUP_DESTINATION`, rejecting paths inside `~/.parchment/`), naming snapshots `<stem>-<8char-path-hash>-<YYYYMMDDTHHMMSSZ><ext>`, keeping the last 10 per source file, and logging/warning without failing a successful save if backup creation fails
- **PRs 5–6:** Complete the native text editor widget (`internal/tui/editor`) with literal `\t` preservation (4-space visual tab stops while `Tab` inserts 2 spaces, removing `\t` rejection from `isUnpreservableText`), `UndoLimit`-bounded keystroke history, keystroke-undo reset on `Ctrl+S` save (**PR 5**), and `go-runewidth` soft line wrapping with CJK/emoji-aware visual row navigation and mouse/selection coordinate mapping (**PR 6**)

### Phase 2: State Safety & CLI Core (PRs 7–13)
- **PRs 7–9:** Buffered `Ctrl+S` saving and autosave recovery drafts for spreadsheets (switching the spreadsheet TUI from immediate per-cell disk writes to buffered workbook editing with dirty-close confirmation in **PR 7**, autosaving the dirty workbook + active cell input in **PR 8**) and presentations (opening recovered drafts directly into the Markdown source editor in **PR 9**)
- **PR 10:** Explicit CLI-only conversion of plain Markdown files (`parchment convert <file> [--kind document|note|presentation] [--yes]`, defaulting `--kind` to `document`, prompting `[y/N]` on a TTY and requiring `--yes` when non-interactive, setting `created_at` from file `mtime` UTC and `modified_at` to current UTC)
- **PRs 11–12:** Spreadsheet deletion & CSV export: 1-based `delete-row`, `delete-column`, and `delete-sheet` (CLI + TUI shortcuts) with formula reference shifting, `#REF!` token support, and relaxed save-time validation allowing formula error values to persist while rejecting invalid syntax (**PR 11**); `spreadsheet export <file> --csv [--sheet <name>] [-o <output>] [--force]` defaulting to the first sheet and `stdout` when omitted and refusing overwrite without `--force` (**PR 12**)
- **PR 13:** Document embedded image management: `document images <file>` (`<name>\t<format>\t<WxH>\t<bytes>\t<referenced|unreferenced>`), `document image-extract <file> <image-name> -o <output> [--force]` (`0600`), and `document image-remove <file> <image-name> [--strip-references]` (refusing removal when referenced in `Body` without `--strip-references` or when referenced by a pending proposal)

### Phase 3: Rendering & TUI Features (PRs 14–23)
- **PRs 14–16:** Overall visual improvements
  - **PRs 14–15:** Render Note `F5` previews, Document reader/preview pages, and Presentation slides via [Glamour](https://github.com/charmbracelet/glamour) (**PR 14**, keeping CLI `document print` plain text and editor images as literal `![alt](image-...)` Markdown). Render embedded images graphically in TUI previews/readers via env-detected Kitty/Sixel/iTerm2 protocols with automatic 24-bit ANSI half-block fallback in multi-column sections or partially clipped viewports (**PR 15**)
  - **PR 16:** Unify bordered content panes and visually separated top header/help and bottom status/undo/redo chrome across Note, Document, Spreadsheet, and Presentation screens
- **PRs 17–19:** Document navigation pane and in-document link navigation (strictly for Documents; does not apply to Notes or Presentations)
  - **PRs 17–18:** Heading slugging (`-1`/`-2` suffixes) and `#id` anchor parsing (**PR 17**), plus a left-side outline pane in the Document editor and reader (`Ctrl+O` toggles visibility; `F6` focuses the editor outline pane, freeing `Tab` in the body to insert 2 spaces while `F2` focuses the toolbar), displaying H1–H6 headings, untitled section breaks (`── Section (N cols)`), and `#id` anchors (**PR 18**)
  - **PR 19:** Pre-render rewritten and coordinate-mapped clickable internal links in Document preview and reader views (suppressing raw destination URLs in Glamour and tracking ordered post-render ANSI line spans)
- **PRs 20–21:** In-editor search and find across all artifact viewers and editors (`Ctrl+F` everywhere plus `/` in read-only/grid browse views; modal bottom find bar + text-editor match highlighting in **PR 20**, extended with `n`/`N`, smart-case matching, ANSI stripping, and full multi-sheet + auto-revealed presentation speaker-note search in **PR 21**)
- **PRs 22–23:** Document and presentation printer font selection (`"courier"` default, `"helvetica"`, `"times"` in `document.Layout` at `LayoutVersion = 1` and a new `font` field in the existing `parchment-presentation` v1 block in **PR 22**), adding a pure-Go core-font PDF renderer (`internal/pdfrender`) and `document print --format text|pdf` (default `text`) without affecting terminal fonts (**PR 23**)

### Phase 4: Extended Domain Engines (PRs 24–30)
- **PRs 24–25:** Zero-dependency recursive-descent spreadsheet formula Lexer, AST, span rewriter, and cross-sheet references (`Sheet2!A1`) in `internal/spreadsheet/formula` (**PR 24**), followed by rectangular ranges (`A1:B10`), `""`-escaped `StringLiteral`s with Excel/Calc coercion, `SUM`, `AVERAGE`, `COUNT`, `MIN`, `MAX`, nested calls, and persisted runtime error propagation (`#REF!`, `#DIV/0!`, `#NAME?`, `#VALUE!`, `#CIRCULAR!`) (**PR 25**)
- **PRs 26–28:** Presentation media, directives, and export
  - **PR 26:** Embedded images in `parchment-presentation` with CLI parity (`presentation image`, `images`, `image-extract`, `image-remove`) and TUI slide rendering
  - **PR 27:** Go `present` `.image <embedded-name> [height] [width]` and directory-confined `.code <relpath> [bounds]` directives (`<= 1 MiB`, no `..`/symlink escape, line numbers and `/regex/` bounds)
  - **PR 28:** Pure-Go standalone `presentation export <file> --format html|pdf -o <output> [--notes] [--force]` (single-file HTML with inline CSS/JS and base64 `data:` URIs; shared pure-Go PDF writer; speaker notes omitted unless `--notes`)
- **PRs 29–30:** Standalone PNG/JPEG image editing (`parchment image crop` and `parchment image resize` with `-o [--force]` or confirmed `--in-place [--yes]`) using `golang.org/x/image/draw` (`CatmullRom`, JPEG quality `90`, `<= 16384px` cap) and removing Markdown-envelope `artifact.ImageKind` (**PR 29**), followed by PNG/JPEG magic-byte detection in `resolveTUITarget` and a basic TUI image viewer with crop/resize prompts (**PR 30**)

### Phase 5: External Interop & Persistent History (PRs 31–38)
- **PRs 31–35:** Import and export ODF (`.odt`) and DOCX (`.docx`) documents via pure-Go stdlib `archive/zip` + `encoding/xml` adapters (`parchment document import` / `export`) with lossy approximation warnings on `stderr` ([`docs/ODF_DOCX_Interop_Plan.md`](docs/ODF_DOCX_Interop_Plan.md))
- **PRs 36–38:** Persistent, chunk-deduplicated undo/redo storage under `~/.parchment/history/` (`history.Store` + `parchment history undo|redo|log|restore|clean|gc`) replacing in-memory service stacks across CLI and TUI ([`docs/Persistent_Undo_Redo_Plan.md`](docs/Persistent_Undo_Redo_Plan.md))

## Architecture & Planning

The detailed PR-by-PR implementation plans can be found in [`FutureWork.md`](FutureWork.md) and the `docs/` directory:
- [Future Work Implementation Plan (Phases 1–5, PRs 1–38)](FutureWork.md)
- [ODF / DOCX Import & Export Plan (Phase 5, PRs 31–35)](docs/ODF_DOCX_Interop_Plan.md)
- [Persistent Undo/Redo Storage Plan (Phase 5, PRs 36–38)](docs/Persistent_Undo_Redo_Plan.md)

## Development

Parchment requires Go 1.24 or newer (ensure the Go binary is on your `PATH`, e.g. `export PATH=$PATH:/usr/local/go/bin:~/go/bin`).

```sh
go build ./cmd/parchment
go test ./...
```

The module path `example.com/parchment` is a placeholder until the project
selects a canonical module path.
