# Future Work

This document lists open or undecided work only. Completed implementation
phases and merged PR checklists are intentionally omitted. See the
[User Guide](docs/User_Guide.md) for current behavior and the
[Technical Guide](docs/Technical_Guide.md) for architecture and format details.

## TUI quality and regression prevention

The design contract (`docs/TUI_Design.md`), shared semantic theme, rendering
goldens, and PR visual-evidence checklist are already in place. Keep adding
message-driven regression tests for newly discovered interaction defects and
update rendering baselines only for intentional, reviewed changes.

- **Make the styling guard syntax-aware.** Replace the existing regular
  expression scan in `internal/tui/style_guard_test.go` with a Go AST-based
  check rejecting hard-coded UI colors and direct
  `lipgloss.Color`/`lipgloss.AdaptiveColor` palette declarations outside the
  approved theme code. Exempt tests and fixtures, and allow runtime-generated
  image pixel colors. Existing theme tests cover supported color profiles,
  including monochrome.
- **Decide on images in Note previews.** Document reader/preview and
  Presentation views already render embedded images with terminal graphics
  protocols and ANSI half-block fallback. Decide whether Note `F5` should
  render explicitly referenced local images; if so, add that support without
  scanning folders or fetching remote content, and keep the note's Markdown
  source unchanged.

## Additional functionality

- **Revisit CLI file-opening convenience.** Review the existing
  `parchment <file>` entry point rather than introducing duplicate routing.
  Decide whether opening a missing path should keep prompting for kind or
  default to creating a Document. Preserve explicit kind selection, plain
  Markdown notes, and the rule that existing files are never overwritten.
  Update CLI help, tests, documentation, and agent instructions together if
  the behavior changes.
- **Replace proposals with Document change tracking.** Decide whether to retire
  the current proposal workflow and its UI/commands. If replacing it, specify
  tracked insertions/deletions, review presentation, accept/reject operations,
  and interactions with save, undo/redo, and external edits. Keep tracking data
  in the artifact's single Markdown file; do not relabel proposals or undo
  history as change tracking.
- **Expand spreadsheet built-in formulas.** The current formula engine
  supports `SUM`, `AVERAGE`, `COUNT`, `MIN`, and `MAX`. Specify and implement
  additional functions such as `COUNTA`, `ROUND`, `IF`, `IFERROR`, `SUMIFS`,
  `COUNTIFS`, `XLOOKUP`, `INDEX`, `MATCH`, `XMATCH`, `TEXTJOIN`, date/time
  functions, `FILTER`, `SORT`, `UNIQUE`, and `SEQUENCE`. Define argument,
  empty-cell, error, date/time, and array-result semantics and test successful
  and failing calculations.
- **Add spreadsheet copy/paste with reference adjustment.** Update relative
  formula references when copying cells or ranges, while preserving absolute
  and mixed references (`$A$1`, `$A1`, `A$1`). Define clipboard behavior and
  boundary/error handling, and test copying in both row and column directions.
- **Add spreadsheet charts.** Support pie, bar, and line charts sourced from
  workbook ranges. Define data binding, TUI rendering, persistence, and export
  behavior; keep chart definitions in the spreadsheet artifact rather than
  per-artifact sidecars.

## Large-artifact performance and responsiveness

Extend the Go benchmarks with reproducibly generated inputs rather than
committing huge fixtures. Record baseline commands and results, and agree on
latency and memory targets before optimization. Measure opening, editing,
rendering/scrolling, saving, and peak memory at fixed terminal sizes; verify
that improvements preserve content, layout, and view-state stability.

- **Documents:** Hundreds of pages and several hundred megabytes of embedded
  images; measure page navigation, Markdown layout/rendering, image decoding,
  and memory retained while scrolling.
- **Presentations:** Hundreds of slides and several hundred megabytes of media;
  measure slide switching, source editing, preview rendering, and image/cache
  memory without sacrificing responsive keyboard input.
- **Spreadsheets:** Hundreds of rows and columns with dense data, formulas, and
  dependencies; measure bulk edits/paste, dependent recalculation, grid
  scrolling, and saving, including large update cascades and cycle errors.
