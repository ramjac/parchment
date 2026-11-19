package tui

import (
	"context"
	"errors"
	"io"
	"testing"
)

func TestCancelledPDFExportDoesNotStartRendering(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	rendered := false
	result := writeRenderedPDF(ctx, nil, "", "", false, func(context.Context, io.Writer) error {
		rendered = true
		return nil
	})
	if !errors.Is(result.err, context.Canceled) {
		t.Fatalf("cancelled export error = %v, want context.Canceled", result.err)
	}
	if rendered {
		t.Fatal("rendering started after cancellation")
	}
}
