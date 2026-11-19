# TUI Design Contract

This document defines the visual and interaction contract for Parchment's
Bubble Tea screens. Apply it to Note, Document, Spreadsheet, and Presentation
views, including readers, editors, previews, and dialogs.

## Semantic styling

Use semantic styles from the shared TUI theme rather than defining colors in
individual views. The theme must provide roles for interface chrome, content
accent, muted text, borders, selection, errors, search matches, headings, links,
images, and code. Map those roles to legible light and dark palettes, and
provide useful limited-color and monochrome fallbacks. Markdown rendering and
the native editor should use the same role mapping without creating package
import cycles.

Group roles by what they represent. Content titles and headings share the
gold/ochre family; links use violet, image labels use pink, inline code uses
orange, and list markers use green. Ordinary prose remains neutral. Fenced
code uses syntax highlighting (Monokai in dark mode). Parchment-owned interface
chrome—including screen titles, find prompts, borders, and active-cell
selection—uses a distinct family. Errors, search matches, and code retain
their semantic colors. Image styling applies to labels, not image pixels.
Default content headings are warm gold/ochre, while interface chrome is cool
teal; these must remain visibly different hues, including in 16-color
terminals, rather than neighboring shades of blue and cyan. The `primary`
configuration override affects content headings, not links or image labels.

Do not remove color, borders, help, focus indicators, or other useful
affordances unless the change is intentional and visually reviewed. Keep
terminal rendering independent of artifact printer-font settings.

## Layout and interaction

- Keep the top header and contextual shortcut help, bordered content area, and
  bottom status/undo/redo area visually distinct and consistently sized.
- Fit the content frame to the space left after fixed chrome and terminal
  borders. Avoid overflow, clipped controls, and unused rows.
- Make the focused pane and current selection apparent. Keep shortcuts
  contextual and visible in the help area when practical.
- At small terminal sizes, preserve the current task and essential controls;
  simplify or scroll content rather than overlapping panes or hiding errors.
- Preserve cursor, selection, focus, active cell/slide, and viewport position
  across saves, save results, status changes, and background updates. Clamp or
  remap positions only when resizing or content changes require it, and keep
  intentional reader/editor transitions explicit.
- Keep `View()` free of I/O and domain mutation. Represent blocking work through
  Bubble Tea commands and typed result messages.

## Markdown and media

Keep canonical Markdown unchanged during rendering. Hide reserved
`parchment-*` blocks while preserving ordinary code fences and user prose.
Document outline and in-document links apply only to Documents.

In text editors, show embedded images as canonical Markdown links or
placeholders. Reader and preview views may render images graphically, with
terminal-protocol detection and the documented ANSI fallback behavior.

## Visual verification

TUI behavior and appearance should be protected by message-driven regression
tests and deterministic, ANSI-aware rendering tests at representative terminal
sizes, including a small-terminal case. Tests should control theme, color
profile, cursor blink, time, and fixture paths; assert screen dimensions and
important styled regions; and avoid shared mutable renderer state in parallel
tests.

For TUI changes, provide before/after screenshots or reproducible recordings
using the same fixture, terminal dimensions, and theme. Interaction fixes need
evidence of the interaction, not only a static screen. Review golden-file
changes intentionally; never update baselines only to make tests pass. Approved
reference screenshots belong alongside the rendering tests once reviewed.
