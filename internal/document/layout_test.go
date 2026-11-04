package document

import (
	"strings"
	"testing"
)

func TestDefaultLayout(t *testing.T) {
	l := DefaultLayout()
	if err := l.Validate(); err != nil {
		t.Errorf("DefaultLayout should be valid, got error: %v", err)
	}
	if l.Version != LayoutVersion {
		t.Errorf("DefaultLayout Version = %v, want %v", l.Version, LayoutVersion)
	}
	if l.PageSize != PageLetter {
		t.Errorf("DefaultLayout PageSize = %v, want %v", l.PageSize, PageLetter)
	}
	if l.Orientation != Portrait {
		t.Errorf("DefaultLayout Orientation = %v, want %v", l.Orientation, Portrait)
	}
	if l.Columns != 1 {
		t.Errorf("DefaultLayout Columns = %v, want 1", l.Columns)
	}
}

func TestColumnWidth(t *testing.T) {
	check := func(name string, textCols, cols, want int) {
		t.Run(name, func(t *testing.T) {
			if got := ColumnWidth(textCols, cols); got != want {
				t.Errorf("ColumnWidth(%d, %d) = %d, want %d", textCols, cols, got, want)
			}
		})
	}

	check("1 column", 80, 1, 80)
	check("2 columns", 80, 2, (80-3)/2) // gutter is 3
	check("3 columns", 80, 3, (80-6)/3)
	check("4 columns", 80, 4, (80-9)/4)
	check("0 columns defaults to 1", 80, 0, 80)
	check("negative columns defaults to 1", 80, -1, 80)
}

func TestGeometryBodyRows(t *testing.T) {
	g := Geometry{
		TextRows:   50,
		HeaderRows: 2,
		FooterRows: 3,
	}
	if got := g.BodyRows(); got != 45 {
		t.Errorf("BodyRows() = %d, want 45", got)
	}
}

func TestLayoutGeometry(t *testing.T) {
	t.Run("basic letter portrait", func(t *testing.T) {
		l := Layout{
			PageSize:    PageLetter,
			Orientation: Portrait,
			Margins:     Margins{Top: 25, Right: 25, Bottom: 25, Left: 25},
			Header:      "Title",
			Footer:      "Page {page}",
			PageNumbers: NumbersCenter,
		}
		g, err := l.Geometry()
		if err != nil {
			t.Fatalf("Geometry() error: %v", err)
		}
		if g.PageCols <= 0 || g.PageRows <= 0 {
			t.Errorf("invalid page cols/rows: %d x %d", g.PageCols, g.PageRows)
		}
		if g.HeaderRows != headerRows {
			t.Errorf("HeaderRows = %d, want %d", g.HeaderRows, headerRows)
		}
		if g.FooterRows != 3 { // footer line + page num line + 1 extra (if footerLines > 0) -> 2 lines + 1 = 3
			t.Errorf("FooterRows = %d, want 3", g.FooterRows)
		}
	})

	t.Run("landscape a4", func(t *testing.T) {
		l := Layout{
			PageSize:    PageA4,
			Orientation: Landscape,
			Margins:     Margins{Top: 10, Right: 10, Bottom: 10, Left: 10},
		}
		g, err := l.Geometry()
		if err != nil {
			t.Fatalf("Geometry() error: %v", err)
		}
		// In A4, portrait width is 210, height is 297.
		// Landscape width is 297, height is 210.
		// Width cols: 297 / 2.54 = 116.9... -> 116
		// Height rows: 210 / (25.4/6) = 210 / 4.2333... = 49.6... -> 49
		if g.PageCols != 116 {
			t.Errorf("PageCols = %d, want 116", g.PageCols)
		}
		if g.PageRows != 49 {
			t.Errorf("PageRows = %d, want 49", g.PageRows)
		}
	})

	t.Run("legal portrait", func(t *testing.T) {
		l := Layout{
			PageSize:    PageLegal,
			Orientation: Portrait,
			Margins:     Margins{Top: 10, Right: 10, Bottom: 10, Left: 10},
		}
		g, err := l.Geometry()
		if err != nil {
			t.Fatalf("Geometry() error: %v", err)
		}
		// Width cols: 215.9 / 2.54 = 85.0 -> 85
		// Height rows: 355.6 / (25.4/6) = 355.6 / 4.2333... = 84.0 -> 84
		if g.PageCols != 85 {
			t.Errorf("PageCols = %d, want 85", g.PageCols)
		}
		if g.PageRows != 84 {
			t.Errorf("PageRows = %d, want 84", g.PageRows)
		}
	})

	t.Run("invalid page size", func(t *testing.T) {
		l := Layout{PageSize: "unknown", Orientation: Portrait}
		if _, err := l.Geometry(); err == nil {
			t.Error("Geometry() expected error for unknown page size")
		}
	})

	t.Run("invalid orientation", func(t *testing.T) {
		l := Layout{PageSize: PageLetter, Orientation: "unknown"}
		if _, err := l.Geometry(); err == nil {
			t.Error("Geometry() expected error for unknown orientation")
		}
	})
}

func TestLayoutValidate(t *testing.T) {
	check := func(name string, modifier func(*Layout), wantErr bool, errText string) {
		t.Run(name, func(t *testing.T) {
			l := DefaultLayout()
			if modifier != nil {
				modifier(&l)
			}
			err := l.Validate()
			if wantErr {
				if err == nil {
					t.Fatalf("Validate() expected error, got nil")
				}
				if !strings.Contains(err.Error(), errText) {
					t.Errorf("Validate() error = %v, want it to contain %q", err, errText)
				}
			} else {
				if err != nil {
					t.Fatalf("Validate() unexpected error: %v", err)
				}
			}
		})
	}

	check("default is valid", nil, false, "")
	check("unsupported version", func(l *Layout) { l.Version = 99 }, true, "unsupported layout version")
	check("negative margin", func(l *Layout) { l.Margins.Top = -1 }, true, "margin must be between")
	check("too large margin", func(l *Layout) { l.Margins.Left = MaxMarginMM + 1 }, true, "margin must be between")
	check("zero columns", func(l *Layout) { l.Columns = 0 }, true, "columns must be between 1 and")
	check("too many columns", func(l *Layout) { l.Columns = MaxColumns + 1 }, true, "columns must be between 1 and")
	check("unsupported page numbers", func(l *Layout) { l.PageNumbers = "bottom-right" }, true, "unsupported page number placement")
	check("header too long", func(l *Layout) { l.Header = strings.Repeat("A", MaxRunningTextLength+1) }, true, "is longer than")
	check("header contains control char", func(l *Layout) { l.Header = "Title\nNextLine" }, true, "contains a control character")

	check("margins leave too little room", func(l *Layout) {
		// MaxMarginMM might not trigger this if text cols drops below min,
		// so let's push margins to their limit to squeeze out space.
		l.Margins.Left = 90
		l.Margins.Right = 90 // Leaves 215 - 180 = 35mm -> ~13 columns (minTextCols is 20)
	}, true, "margins leave too little room")

	check("columns too narrow", func(l *Layout) {
		l.Columns = MaxColumns // 4
		l.Margins.Left = 60
		l.Margins.Right = 60
	}, true, "columns are too narrow")

	check("invalid geometry config", func(l *Layout) { l.PageSize = "unknown" }, true, "unsupported page size")
}
