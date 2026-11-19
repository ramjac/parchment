package tui

import (
	"bytes"
	"context"
	"io"
	"path/filepath"
	"strings"

	"example.com/parchment/internal/document"
	"example.com/parchment/internal/pdfrender"
	"example.com/parchment/internal/presentation"
	"example.com/parchment/internal/spreadsheet"
)

type pdfExportResult struct {
	path    string
	err     error
	warning error
}

func writeSpreadsheetPDF(ctx context.Context, store PDFExportStore, path, source string, book spreadsheet.Spreadsheet, overwrite bool) pdfExportResult {
	return writeRenderedPDF(ctx, store, path, source, overwrite, func(ctx context.Context, w io.Writer) error {
		return pdfrender.RenderSpreadsheetContext(ctx, w, book)
	})
}

func pdfDefaultPath(source, lastPath string) string {
	if lastPath != "" {
		return lastPath
	}
	extension := filepath.Ext(source)
	if extension == filepath.Base(source) {
		extension = ""
	}
	return strings.TrimSuffix(source, extension) + ".pdf"
}

func writePDF(ctx context.Context, store PDFExportStore, path, source string, d document.Document, overwrite bool) pdfExportResult {
	return writeRenderedPDF(ctx, store, path, source, overwrite, func(ctx context.Context, w io.Writer) error {
		return pdfrender.RenderContext(ctx, w, d)
	})
}

func writePresentationPDF(ctx context.Context, store PDFExportStore, path, source string, item presentation.Presentation, overwrite, twoUp bool) pdfExportResult {
	return writeRenderedPDF(ctx, store, path, source, overwrite, func(ctx context.Context, w io.Writer) error {
		if twoUp {
			return pdfrender.RenderPresentationTwoUpContext(ctx, w, item)
		}
		return pdfrender.RenderPresentationContext(ctx, w, item)
	})
}

func writeRenderedPDF(ctx context.Context, store PDFExportStore, path, source string, overwrite bool, render func(context.Context, io.Writer) error) pdfExportResult {
	if err := ctx.Err(); err != nil {
		return pdfExportResult{err: err}
	}
	var rendered bytes.Buffer
	if err := render(ctx, &rendered); err != nil {
		return pdfExportResult{err: err}
	}
	if err := ctx.Err(); err != nil {
		return pdfExportResult{err: err}
	}
	if err := store.WritePDFExport(ctx, path, source, rendered.Bytes(), overwrite); err != nil {
		return pdfExportResult{path: path, err: err}
	}
	return pdfExportResult{path: path, warning: store.SavePDFPath(ctx, path)}
}
