# Repository guidance

## Build, test, and format

Use Go 1.24 or newer.

- Build: `go build ./cmd/parchment`
- Run all tests: `go test ./...`
- Run one test: `go test ./internal/note -run '^TestNoteOperationsUndoAndRedo$'`
- Format Go code: `gofmt -w cmd internal`

There is no repository-specific lint target or linter configuration.

## Architecture

`cmd/parchment` is the executable entry point; `internal/cli` accepts a file
path and resolves per-user settings. The CLI and the Bubble Tea interface
share `internal/note.Service` for note operations.

The note service depends on a small repository interface. File-backed
repositories in `internal/workspace` implement note, document, spreadsheet,
and presentation persistence for individual files; `internal/tui` receives
the appropriate service and repository. This keeps behavior independent of
the CLI, TUI, and storage implementation. `internal/history` provides bounded,
in-memory undo/redo for successful service changes.

Parchment is a file-oriented editor, like a word processor. A shared
workspace means the user's ordinary filesystem (for example, their home
directory): Parchment, basic text editors, and other applications can all
open and modify the same files. It does not mean an application-owned
directory, project, or artifact store. Users open and edit existing files
wherever they live without initialization, workspace discovery, or
app-specific directory markers.
The optional `parchment.toml` is per-user configuration in the OS config
directory, not a marker alongside edited files. A Parchment file is a
Markdown document with an embedded, typed data envelope: its first fenced code
block is `parchment-meta`, containing kind, format version, and creation and
modification timestamps as JSON. Filenames, not stored IDs or titles, identify
files to users; do not persist an ID, title, location, tags, or links in the
envelope. Runtime identities may be derived from file paths for existing
service interfaces. Kind-specific data is stored in `parchment-<thing>` fenced blocks, also JSON
unless a versioned block format explicitly specifies otherwise; the remaining
content is the human-authored Markdown body, separated from the envelope by a
required `<!-- parchment-body -->` comment. Structured `parchment-*` blocks
follow the body after an explicit `<!-- parchment-blocks -->` comment, keeping
large payloads such as base64-encoded images after the reader-friendly content.
This keeps one inspectable, editable file per artifact while allowing
structured data such as workbook cells, document layout, and change history.
`internal/artifactfile` reads and writes the common envelope, and
`internal/artifact` defines the shared metadata. Parchment renderers hide
reserved `parchment-*` fences, but preserve ordinary code fences. `internal/search`
searches notes directly through the repository, without an index or background
process.

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
- Open note files directly in their editor rather than a read-only preview.
  Documents, spreadsheets, and presentations may open in their reader views.
- Keep note behavior in the service layer and persistence in file-backed
  repositories; the CLI and TUI should orchestrate these rather than duplicate
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
- Do not require an artifact ID or title to open, edit, or save a file. Never
  use stored location metadata to constrain where an existing file can be
  opened or saved.
- Generally, persist each artifact as one human-readable file. Keep its shared
  metadata, content, comments or annotations, and change-tracking data together
  so a text editor can inspect the complete artifact without opening sidecars.
  Store metadata in the first `parchment-meta` fenced JSON block. Store
  structured data in kind-specific `parchment-<thing>` fenced blocks, normally
  JSON; encode binary content such as images as base64 in these blocks. Keep
  ordinary prose and formatting in the Markdown body. The expected exception
  is separate autosave/recovery data used to restore unsaved work. Do not add
  other per-artifact sidecar files.
- File writes use a temporary file followed by sync and rename; preserve the
  existing file permissions and reject concurrent external edits.
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
- Artifact timestamps are UTC. The optional per-user config is versioned TOML
  (`version = 1`) at `parchment/parchment.toml` under the platform's standard
  user config directory. Environment overrides are applied afterward. There
  is no project-local config or workspace selection in the file-opening CLI.
- If multi-file search is exposed later, do not invent a Parchment-specific
  workspace or index to support it. Existing search logic scans a supplied
  repository directly, without a database or background process.
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
  interactive terminal. Keep tests independent of a real home directory,
  network, timezone, terminal, and machine-specific configuration.
