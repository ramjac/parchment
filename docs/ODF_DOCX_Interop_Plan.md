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
