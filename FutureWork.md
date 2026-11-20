# Future Work

This document lists open or undecided work only. Completed implementation
phases and merged PR checklists are intentionally omitted. See the
[User Guide](docs/User_Guide.md) for current behavior and the
[Technical Guide](docs/Technical_Guide.md) for architecture and format details.

## TUI quality improvements

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
- **Decide on images in Note previews.** Decision - Notes won't support embedded images.
  More generally, Notes won't support any features that result in code blocks after
  the closing parchment fence (like images).
- **Assess extending animated view transitions to Documents and Spreadsheets.**
  Presentation slide transitions now have a Harmonica prototype. Preserve
  reader/editor and sheet-tab state, keep transitions interruptible, and ensure
  animation ticks stop on cancellation or view exit. Add deterministic
  message-driven tests and visual evidence before extending the behavior.
- **Evaluate terminal image libraries separately from charting.** Compare
  `go-termimg` with `internal/tui/termimg` for Kitty, iTerm2, Sixel, sizing,
  placement, and ANSI fallback. Its Go 1.24-compatible release supports these
  protocols, but its normal render-options path queries terminal capabilities;
  Parchment's environment-only detection and no-terminal-query behavior must
  remain intact. Use a library only if rendering can be explicitly configured
  without terminal queries or direct stdout writes, and retain Parchment's
  image-dimension limits and document-image placement behavior. The newer
  ntcharts image widget is not a drop-in alternative: its current v2 branch
  requires Go 1.26 and Bubble Tea v2 and has different protocol coverage.
- **Consider BubbleZone only for discrete hit regions.** Prototype it for
  rectangular toolbar or outline controls if those controls grow. Keep custom
  coordinate mapping for editor cursor/selection and spreadsheet cells, where
  pointer positions map to runes or cells rather than simple clickable boxes.

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
