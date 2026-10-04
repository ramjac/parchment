# Repository guidance

## Build, test, and format

Use Go 1.24 or newer.

- Build: `go build ./cmd/parchment`
- Run all tests: `go test ./...`
- Run one test: `go test ./internal/note -run '^TestNoteOperationsUndoAndRedo$'`
- Format Go code: `gofmt -w cmd internal`

There is no repository-specific lint target or linter configuration.

## Architecture

`cmd/parchment` is the executable entry point; `internal/cli` builds the Cobra
command tree and resolves settings and the active workspace. The CLI and the
Bubble Tea interface share `internal/note.Service` for note operations.

The note service depends on a small repository interface. `internal/workspace`
implements it as a filesystem-backed store, and `internal/tui` receives that
same interface along with the service. This keeps note behavior independent of
the CLI, TUI, and storage implementation. `internal/history` provides bounded,
in-memory undo/redo for successful service changes.

An initialized workspace stores versioned `parchment.toml` at its root and
artifacts under `.parchment/artifacts/<id>/content.md`. Each artifact file is
Markdown and begins with a `parchment-meta` fenced code block containing its
shared metadata as JSON. Kind-specific structured data uses `parchment-<thing>`
JSON code blocks; renderers hide these reserved blocks. `internal/artifact`
defines the metadata shared by artifact kinds. `internal/search` searches notes
directly through the repository, without an index or background process.

## Repository-specific conventions

- Treat this as a local-first modular monolith: the workspace is authoritative,
  and normal operation must work offline without accounts, hosted services,
  telemetry, a daemon, or a custom sync protocol. Keep data inspectable and
  usable with ordinary filesystem tools.
- Keep note behavior in the service layer and persistence in the workspace
  repository; the CLI and TUI should orchestrate these rather than duplicate
  note rules.
- Keep architectural dependencies pointed outward: domain/application code
  must not depend on Cobra, Bubble Tea, terminal rendering, or provider-specific
  integrations. Resolve configuration at the CLI/application boundary; do not
  load environment or config files from domain packages.
- Use Cobra for CLI commands and typed TOML configuration; do not add Viper.
  Use the standard-library `log/slog` API if logging is implemented. Keep the
  CLI thin and make operations reusable by both CLI and TUI.
- Follow Bubble Tea's model/update/view/command structure. Keep views free of
  I/O and domain mutation; represent blocking operations as commands returning
  typed result messages. Keep key bindings contextual and ensure text editing
  and confirmation states receive input before background screens. Do not
  introduce a universal child-component interface until multiple real
  components need it.
- Persist artifact IDs as 32-character lowercase hex strings. An artifact's
  `Location` must be `.parchment/artifacts/<id>/content.md`.
- Generally, persist each artifact as one human-readable file. Keep its shared
  metadata, content, comments or annotations, and change-tracking data together
  so a text editor can inspect the complete artifact without opening sidecars.
  Store metadata in the first `parchment-meta` fenced JSON block, followed by
  kind-specific `parchment-<thing>` blocks for structured data. The expected
  exception is separate autosave/recovery data used to restore unsaved work.
  Do not add other per-artifact sidecar files.
- Workspace writes use a temporary file followed by sync and rename; preserve
  the restrictive file and directory permissions used by the workspace
  package.
- Keep Markdown as canonical note content; rendered Markdown belongs to the
  view layer and must never replace persisted content. Keep import/export
  formats separate from domain models.
- Hide fenced blocks whose info string begins with `parchment-` when rendering
  Markdown, while preserving ordinary code fences and all canonical source.
- Artifact timestamps are UTC. Config files are versioned TOML (`version = 1`);
  the user config is loaded before workspace config, with environment
  overrides applied afterward. Workspace selection also supports the
  `--workspace` flag, `PARCHMENT_WORKSPACE`, a configured user workspace, and
  parent-directory discovery.
- Search is case-insensitive across title, Markdown body, and tags. Keep it a
  direct repository scan for this release: no database, index, or background
  indexer. Search results and note lists are ordered newest-modified first.
  Keep search coupled to the repository boundary, not artifact-domain APIs, so
  a rebuildable index could be added later if required.
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
