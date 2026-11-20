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
- **Evaluate Charmbracelet libraries before adding bespoke TUI components.**
  Outside the dedicated large-text editors, check whether Bubbles already
  provides the needed component. PDF path fields and chart-form fields already
  use Bubbles `textinput`, while their prompt, validation, and overwrite flows
  remain application state. Audit the chart list and key/help handling for
  `list`, `key`, or `help` components; do not replace the spreadsheet editor
  with a generic table unless formula editing, range selection, clipboard
  operations, and sheet navigation remain intact. Do not use a file picker
  that scans directories: users explicitly name files. Keep Lip Gloss as the
  shared semantic styling and layout layer.
- **Prototype animated view transitions, starting with Presentations.** Try
  Harmonica for slide-to-slide transitions first, then assess the Document
  reader/editor transition and spreadsheet sheet-tab changes. Preserve the
  active slide, page, cursor, selection, focus, and viewport state. Keep
  transitions interruptible and ensure animation ticks stop on cancellation or
  view exit; add deterministic message-driven tests and visual evidence before
  extending the behavior to other screens.
- **Evaluate ntcharts for spreadsheet chart previews.** Compare its terminal
  bar/line rendering with `internal/chartrender`, using the existing
  `spreadsheet.ChartData` as input. It may be that we use one method of
  displaying charts in the TUI such as with ntcharts and another method of 
  rendering them for SVG/PDF export. Consider making a prereq step of upgrading
  to Bubbletea v2 to adopt ntcharts v2.
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
