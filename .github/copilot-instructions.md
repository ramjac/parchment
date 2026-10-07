# Repository guidance

## Build, test, and format

Use Go 1.24 or newer.

- Build: `go build ./cmd/parchment`
- Run all tests: `go test ./...`
- Run one test: `go test ./internal/note -run '^TestNoteOperationsUndoAndRedo$'`
- Format Go code: `gofmt -w cmd internal`

There is no repository-specific lint target or linter configuration.

## Core principle: artifacts are ordinary files in shared folders

Parchment does not and will never manage a directory of artifacts. A
Parchment artifact is an ordinary standalone file that lives wherever the user
puts it, such as the home directory or `~/Documents`, alongside the user's
other, non-Parchment files. Those folders are shared spaces, not Parchment
territory. The same artifact can be opened by Parchment or by any text editor.

- Do not create or require a Parchment-owned artifact directory, store,
  library, vault, collection, workspace, or similar concept under any name.
  Renaming the concept (for example an "artifacts directory") does not make it
  acceptable.
- "Separating Parchment files from the user's other files" is not a goal of
  this repository; the opposite is intended. Do not justify designs with it.
- Do not give artifacts opaque storage paths such as `<id>/content.md` or
  require Parchment-specific folder layouts, marker files, lock directories,
  or bookkeeping files beside artifacts. A user names and places their files.
- Do not scan, index, or take ownership of a directory to find artifacts;
  operate on the files the user explicitly opens or names.
- `~/.parchment/` is for Parchment's own configuration and application state
  only (`parchment.toml`, the global write lock, autosave recovery drafts,
  and future persistent undo/redo history). It never contains artifacts.
- Do not reintroduce `list`, `search`, or `delete` commands that treat a
  directory as Parchment's collection. Users find, rename, move, and delete
  artifact files with ordinary tools.

## Architecture

`cmd/parchment` is the executable entry point; `internal/cli` builds the Cobra
command tree and resolves settings. Every command takes the artifact's file
path (`parchment note show ~/Documents/ideas.md`); `create` refuses to
overwrite an existing file. `parchment <file>` (or `parchment tui <file>
[--kind note|document|spreadsheet|presentation]`) opens one file in the TUI,
creating it (after asking for the kind when `--kind` is omitted) if it does
not exist. Plain Markdown files without a Parchment envelope open as notes and
stay plain when saved.

The note, document, spreadsheet, and presentation services in
`internal/<kind>` hold the domain rules and depend on small repository
interfaces keyed by file path. `internal/filerepo` implements them over
standalone, user-named files: it detects kinds from metadata, rejects files of
another kind, derives runtime identity (ID and title) from the path, detects
edits made outside Parchment by comparing the file with the version that was
loaded, writes atomically next to the file, and keeps its write lock and
recovery drafts under `~/.parchment/`. It never deletes artifact files.
`internal/tui` is a single-file editor that receives the services and a
`recovery.Store`. This keeps behavior independent of the CLI, TUI, and
storage implementation. `internal/history` provides bounded, in-memory
undo/redo for successful service changes; creating a file is not recorded.

`~/.parchment/` holds Parchment's own files only: the versioned
`parchment.toml` (created automatically), `write.lock`, and `recovery/`
autosave drafts (named by a hash of the artifact's absolute path) and, in the
future, persistent undo/redo history. Never store artifacts under
`~/.parchment/`. A Parchment file is a Markdown document with an embedded,
typed data envelope: its first fenced code block is `parchment-meta`,
containing kind, format version, and creation and modification timestamps as
JSON. Filenames, not stored IDs or titles, identify files to users; do not
persist an ID, title, location, tags, or links in the envelope. Kind-specific
data is stored in `parchment-<thing>` fenced blocks, also JSON
unless a versioned block format explicitly specifies otherwise; the remaining
content is the human-authored Markdown body, separated from the envelope by a
required `<!-- parchment-body -->` comment. Structured `parchment-*` blocks
follow the body after an explicit `<!-- parchment-blocks -->` comment, keeping
large payloads such as base64-encoded images after the reader-friendly content.
This keeps one inspectable, editable file per artifact while allowing
structured data such as workbook cells, document layout, and change history.
`internal/artifactfile` reads and writes the common envelope, and
`internal/artifact` defines the shared metadata. Parchment renderers hide
reserved `parchment-*` fences, but preserve ordinary code fences. Metadata
does not record the file's path; the path is wherever the file is.

## Repository-specific conventions

- Parchment has not had its first release and has no legacy artifacts or
  compatibility obligations. Do not add legacy-format support, migrations, or
  backwards-compatibility handling for artifact format changes. If format
  requirements change incompatibly, update the implementation and the
  checked-in files in `examples/`. Remove this instruction at the first release.
- Treat this as a local-first modular monolith: the file being edited is
  authoritative. Normal operation must work offline without accounts, hosted
  services, telemetry, a daemon, or a custom sync protocol. Keep data
  inspectable and usable with ordinary filesystem tools and other editors.
  Do not claim exclusive ownership of user files or move them into private
  application storage. Never require `init` or a workspace flag to open a file.
- Keep note behavior in the service layer and persistence in the filesystem
  repository; the CLI and TUI should orchestrate these rather than duplicate
  note rules.
- Keep architectural dependencies pointed outward: domain/application code
  must not depend on Cobra, Bubble Tea, terminal rendering, or provider-specific
  integrations. Resolve configuration at the CLI/application boundary; do not
  load environment or config files from domain packages.
- Use Cobra for the file-path CLI and typed TOML configuration; do not add Viper.
  Use the standard-library `log/slog` API if logging is implemented. Keep the
  CLI thin and make operations reusable by both CLI and TUI. Do not introduce
  ID-based commands for opening or editing files; IDs are internal metadata,
  not user-facing file addresses.
- Follow Bubble Tea's model/update/view/command structure. Keep views free of
  I/O and domain mutation; represent blocking operations as commands returning
  typed result messages. Keep key bindings contextual and ensure text editing
  and confirmation states receive input before background screens. Do not
  introduce a universal child-component interface until multiple real
  components need it.
- Do not require a stored artifact ID or title to open, edit, or save a file.
  Runtime IDs are derived from paths; they must not dictate file names or
  folder layout, and operations address artifacts by path, not ID.
- Generally, persist each artifact as one human-readable file. Keep its shared
  metadata, content, comments or annotations, and change-tracking data together
  so a text editor can inspect the complete artifact without opening sidecars.
  Store metadata in the first `parchment-meta` fenced JSON block. Store
  structured data in kind-specific `parchment-<thing>` fenced blocks, normally
  JSON; encode binary content such as images as base64 in these blocks. Keep
  ordinary prose and formatting in the Markdown body. The expected exception
  is separate autosave/recovery data used to restore unsaved work. Do not add
  other per-artifact sidecar files.
- Filesystem writes use a temporary file in the artifact's directory followed
  by sync and rename; preserve an existing file's permissions, write symlink
  targets rather than replacing links, create new files owner-only, reject
  concurrent external edits, and keep `~/.parchment/` owner-only. Never place
  other files beside artifacts.
- Keep Markdown as canonical note content; rendered Markdown belongs to the
  view layer and must never replace persisted content. Keep import/export
  formats separate from domain models.
- A document or presentation may select one font for the entire artifact, not
  per section. Font selection applies to printer-oriented or rendered output
  only. Do not apply artifact font settings to terminal output: TUI and terminal
  previews use the font configured by the user's terminal emulator.
- Hide fenced blocks whose info string begins with `parchment-` when rendering
  Markdown, while preserving ordinary code fences and all canonical source.
  The artifact parser reads structured blocks only after the body and the
  explicit `<!-- parchment-blocks -->` marker. The renderer hides
  `parchment-*` fences wherever they occur, but a block placed among body
  paragraphs is not interpreted as structured data. For example, contextual
  `parchment-footnote` blocks and Markdown-link footnote references are not
  supported; preserve such blocks as source unless implementing explicit
  parsing, validation, and rendering semantics for them.
- Artifact timestamps are UTC. Configuration is a single versioned TOML file
  (`version = 1`) at `~/.parchment/parchment.toml`, or the existing file named
  by `PARCHMENT_CONFIG`; environment overrides are applied afterward. Do not
  reintroduce workspaces, collections, or per-directory config files.
- Closing the note or document editor normally (confirmed when dirty) deletes
  the file's recovery draft; only an abnormal exit leaves one, which is
  offered when the same path is reopened. Spreadsheet and presentation editors
  do not autosave drafts yet. Document proposals are an editor action (`F3`
  proposes unsaved edits, `F4` reviews proposals), not a separate mode.
- Documents, spreadsheets, and presentations may open in their reader views;
  notes open directly in their editor. An existing document opens in its
  reader (outline pane beside the rendered pages); a newly created document or
  a restored recovery draft opens in the editor. Leaving the document editor
  returns to the reader. Do not remove reader views when changing editors.
- Keep undo operations meaningful and bounded; record only successful
  operations, clear redo after a new operation, and do not persist history
  unless persistence is implemented and tested. The current history is
  process-local.
- Add packages only for demonstrated responsibilities; do not create empty
  placeholders, use Go's `plugin` package, or introduce a TUI framework besides
  Bubble Tea.
- Tests use Go's `testing` package and temporary directories for filesystem
  behavior. CLI tests construct a command with injected output streams; TUI
  tests exercise the Bubble Tea model with messages rather than starting an
  interactive terminal, using real services over `filerepo` in temporary
  directories; CLI tests point `HOME` at a temporary directory and use
  `t.Chdir`. Keep tests independent of a real home directory,
  network, timezone, terminal, and machine-specific configuration.
