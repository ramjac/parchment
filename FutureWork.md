# Future Work

This document turns the items in the README's **Future work** section into
implementation plans. The plans follow Parchment's existing boundaries:
services own artifact rules, repositories own file I/O, and the TUI presents
those operations. Artifacts remain ordinary user-named files; application
state, recovery drafts, and persistent history belong under `~/.parchment/`.

## 1. Navigate document links

Add in-document Markdown link navigation to the document reader.

- Parse link destinations and heading targets from the canonical Markdown
  body, without rewriting or normalizing the saved source. Resolve links to
  headings by a stable Markdown heading slug, and support explicit anchors if
  the renderer/parser can do so consistently.
- Extend the document's parsed outline or pagination results with target
  locations. This gives a link target a page to navigate to while preserving
  existing outline navigation.
- Have the reader retain the rendered link spans and their destinations so
  mouse clicks can be hit-tested against terminal coordinates. A click on an
  in-document target moves to its page and section; unresolved and external
  links remain visibly links but do not navigate within the reader.
- Keep parsing and rendering separate from document persistence. Test heading
  slugs, duplicate headings, explicit anchors, links across pages, unresolved
  links, and mouse navigation through Bubble Tea model messages.

**Acceptance:** Clicking a rendered link to a document heading opens the
correct page without changing the document file; outline navigation and
ordinary Markdown text continue to work.

## 2. Explicitly convert plain Markdown files

Add an explicit CLI conversion operation for turning a plain Markdown file
into a typed Parchment artifact. Those artifacts will be a Note, Document, or Presentation. Conversion will be in place, as requested,
but must ask for confirmation before adding Parchment metadata.

- Require the source path and an explicit artifact kind (initially note,
  document, or presentation). Do not infer the kind from a
  filename or make opening a plain note perform conversion. Instead, default
  to coverting to Document with an option to pick note or presentation instead.
- Read and validate the existing plain file, preserve its Markdown body, and
  construct the selected kind's normal metadata and structured data. Do not
  add a stored title, ID, or path.
- Use the existing filesystem safeguards: detect changes since reading,
  write atomically in the source directory, preserve permissions, and leave
  the file unchanged if the user declines or validation fails. Refuse to
  convert a file that already has Parchment metadata.
- Test each supported kind, plain-body preservation, declined confirmation,
  invalid kinds, already-converted files, external-edit conflicts, and
  filesystem write failures.

**Acceptance:** Opening and saving plain Markdown retains current behavior;
only the explicit, confirmed command changes it into a Parchment artifact.

## 3. Autosave spreadsheet and presentation recovery drafts

Extend the existing recovery flow used by notes and documents to the
spreadsheet and presentation editors. The recovery store already accepts
kind-tagged JSON snapshots keyed by artifact path, so drafts can stay in
`~/.parchment/recovery/` without adding sidecars.

- Define a versioned snapshot for each editor. A spreadsheet snapshot must
  include the workbook data and the pending cell edit; a presentation
  snapshot must include the source currently in the editor. Keep recovery
  serialization separate from artifact serialization.
- Start a cancellable autosave timer only while an editor has unsaved work.
  Save through the existing recovery store, report failures in the editor,
  and ensure cancellation prevents a late save from recreating a draft after
  save or close.
- On reopen, offer recover, discard, or quit-and-keep behavior consistent
  with notes/documents. Validate draft kind and contents before restoring;
  saving recovered work must continue to use normal external-edit conflict
  checks.
- Delete drafts after a successful save or normal close; retain them after an
  abnormal exit or a failed cleanup and surface cleanup errors.
- Test autosave ticks and cancellation with model messages and a temporary
  recovery store, including malformed drafts, declined recovery, successful
  save cleanup, and interrupted-exit recovery.

**Acceptance:** Unsaved spreadsheet and presentation edits can be recovered
after an abnormal exit, with the same conflict protection and cleanup behavior
as note/document drafts.

## 4. Common editing key combinations

Improve text editing in the note, document, and presentation source editors
without moving editing rules into the view layer or changing canonical
Markdown.

- Centralize key handling for word movement, word deletion, and range
  selection so editors behave consistently. `Ctrl+Left`/`Ctrl+Right` move by
  word; `Ctrl+Backspace`/`Ctrl+Delete` remove the preceding/following word.
- Implement `Shift+Left`/`Shift+Right` and `Shift+Up`/`Shift+Down` as
  selections, with a visible selection that can be replaced by typing or
  deleted. Preserve rune boundaries and multiline behavior.
- Make `Tab` insert two spaces by default, which avoids creating an unintended
  Markdown code block with four-space indentation. Keep normal focus and
  navigation keys available outside text-editing states.
- Reuse a small shared editor-input helper if Bubble Tea's textarea does not
  provide the required selection and movement behavior; avoid replacing the
  widget unless necessary.
- Add model-level tests for each key combination, selection replacement,
  Unicode text, line boundaries, and ensuring these keys do not intercept
  reader/grid/navigation input.

**Acceptance:** The listed combinations work predictably in text-editing
states, while existing save, close, confirmation, and navigation shortcuts
remain contextual.

## 5. Improve rendering and TUI layout

### Render Markdown in viewers and previews

- Introduce a view-layer Markdown renderer shared by document pages, note
  previews, and presentation previews where appropriate. It should render
  common Markdown structures—headings, emphasis, lists, block quotes, links,
  and fenced code—with terminal-width-aware wrapping and readable styles.
- Evaluate a Go terminal Markdown renderer compatible with the existing
  Charmbracelet stack (including the library used by Glow) before adding a
  dependency. Keep the renderer pure: it must not perform I/O, mutate
  artifacts, or replace canonical Markdown.
- Handle rending images embedded as base64 encoded strings.
- Filter reserved `parchment-*` blocks from display using the existing
  artifact rules while preserving ordinary code fences. Keep document page
  breaks, section layout, and link hit-testing integrated with rendered
  output.
- Test rendering at narrow and wide widths, ordinary versus reserved code
  fences, long lines, Unicode, and preservation of the original source.

### Use the available terminal height in the note editor

- First verify whether the README's reported height issue is still present.
  Trace terminal-size messages through editor sizing and count the actual
  header, editor, status, and help rows before changing layout.
- If the editor still leaves unused rows, correct its height calculation and
  keep it responsive to resize events and minimum terminal sizes. If already
  fixed, add a regression test rather than changing working layout.

### Separate application chrome from artifact content

- Give headers, footers, status, undo/redo indicators, and editor content
  consistent visual boundaries using the existing theme and Lip Gloss styles.
- Apply the layout consistently to note, document reader/editor, spreadsheet,
  and presentation screens. Keep content readable in both terminal themes and
  avoid clipping when the terminal is narrow or short.
- Add model/view tests for line budgets and representative narrow, wide, and
  minimum terminal sizes.

**Acceptance:** Common Markdown is visibly formatted without altering stored
text, editor layout uses available terminal space, and app chrome is
distinguishable from artifact content across screens.

## 6. Persist undo/redo history

Store bounded, per-file undo/redo snapshots under `~/.parchment/`, as
requested. Use canonical artifact file contents as snapshots rather than
serializing in-memory operation objects; this keeps recovery independent of
Go implementation details and captures structured blocks and embedded data.

- Add a history store beside the existing recovery store. Key records by a
  hash of the artifact's absolute path; keep records owner-only and outside
  artifact directories. Do not scan user directories or persist history for
  file creation.
- Capture before/after snapshots only for successful service changes. Apply
  the configured undo limit per file and clear redo after a new change, as
  the in-memory stack does today.
- Persist undo/redo transitions atomically. Record the expected current file
  digest and compare it before every restore; if an external editor has
  changed the artifact, refuse to overwrite it and retain the history for
  explicit recovery. Use a small transition journal so startup can resolve a
  crash between a history update and an artifact restore by comparing the
  current file digest with the recorded before/after digests. If neither
  matches, report a conflict and leave both file and history intact.
- Surface corrupt, unreadable, or unwritable history as an explicit error;
  never silently treat it as empty. Preserve successful artifact writes if a
  later history write fails, but report that the change was saved and its
  history could not be persisted.
- Integrate the store at the service/history boundary so CLI and TUI
  operations behave consistently. Test persistence across service instances,
  bounded history, redo clearing, external edits, corrupt records, and
  simulated interrupted transitions using temporary directories.

**Acceptance:** Undo and redo survive process restarts for successful edits,
remain bounded, and cannot overwrite edits made outside Parchment.

## 7. Import and export ODF and DOCX

Plan document conversion around Markdown text, basic document layout, and
embedded images. Conversion is intentionally lossy: Parchment does not
represent multiple fonts, exact image/object positioning, or every feature
available in office formats. Import/export must report unsupported or
approximated content rather than implying a lossless round trip.

- Define a documented mapping for headings, paragraphs, lists, page size,
  orientation, margins, columns, headers/footers, and embedded images between
  Parchment documents and ODF Text (`.odt`) / Word (`.docx`). Leave unsupported
  features such as precise object placement and multiple per-section fonts
  out of the Parchment model and include them in conversion warnings.
- Implement format-specific adapters behind a document import/export
  boundary, keeping archive/XML details out of the document domain model.
  Evaluate maintained Go libraries against the required subset before
  choosing dependencies; limit archive and XML processing to the selected
  document formats.
- Add path-based CLI import/export commands. Import creates a user-named
  Markdown artifact; export creates a user-named ODF or DOCX file. Refuse
  accidental overwrites, preserve the source file, and embed images in the
  output document rather than creating asset sidecars.
- Use small checked-in fixtures for representative ODF and DOCX files,
  plus generated export fixtures where useful. Test content/layout mappings,
  embedded images, malformed or unsupported input, warnings, and
  non-overwrite behavior.

**Acceptance:** The four operations—ODF export, DOCX export, ODF import, and
DOCX import—work for the documented subset, preserve source files, and
explain losses and unsupported features.

## 8. Basic image editing

Add crop and resize operations for image files opened directly by Parchment.
Keep images as ordinary files at user-chosen paths; do not wrap them in
Markdown artifacts or add per-image sidecars.

- Start with an explicit image service and path-based CLI operations. Support
  common static PNG and JPEG inputs first, and report unsupported formats
  rather than silently changing them.
- Accept crop bounds and resize dimensions explicitly, validate positive
  dimensions and source bounds, and preserve aspect ratio when the user asks
  for proportional resizing. Make interpolation and JPEG quality documented
  defaults if they are exposed.
- Write to an explicit output path by default, preserving the original.
  Allow same-path replacement only after explicit user choice; use atomic
  writes, preserve existing permissions, and apply owner-only permissions to
  newly created output files.
- Add a basic image view in the TUI with clear crop/resize controls only if
  direct image opening is not already surfaced by the application. Keep image
  processing outside the TUI and test it with generated fixtures in temporary
  directories.
- Test format support, invalid geometry, aspect ratio, output-path behavior,
  same-path confirmation, write errors, and image dimensions/pixel results.

**Acceptance:** Users can crop or resize a directly opened supported image
without unintended loss or modification of the original.

## Suggested implementation order

1. Verify the note-editor height issue and establish shared layout/rendering
   tests before changing TUI presentation.
2. Implement shared text-input behavior and the two new recovery-draft kinds;
   both build on existing editor states and recovery infrastructure.
3. Add document link targets/navigation and explicit plain-Markdown conversion.
   Keep both path-based and preserve canonical source content.
4. Implement ODF/DOCX format adapters after documenting and testing the
   supported mapping and loss-reporting behavior.
5. Add image crop/resize operations behind a path-based service.
6. Add persistent history after defining the snapshot and crash-recovery
   record format; integrate it across services, then verify conflict and
   restart behavior.

Each feature should include focused Go unit/model tests and use temporary
directories for filesystem behavior. Run `go test ./...` and
`go build ./cmd/parchment` as the final integration checks.
