package document

import (
	"errors"
	"fmt"
	"math"
	"unicode"
	"unicode/utf8"
)

// LayoutVersion is the supported layout.json format version.
const LayoutVersion = 1

// Supported page sizes, orientations, and page-number placements.
const (
	PageLetter = "letter"
	PageA4     = "a4"
	PageLegal  = "legal"

	Portrait  = "portrait"
	Landscape = "landscape"

	NumbersNone   = "none"
	NumbersLeft   = "left"
	NumbersCenter = "center"
	NumbersRight  = "right"
)

const (
	// MaxColumns is the largest supported column count.
	MaxColumns = 4
	// ColumnGutter is the number of blank characters between columns.
	ColumnGutter = 3
	// MinColumnWidth is the narrowest allowed column, in characters.
	MinColumnWidth = 10
	// MaxMarginMM is the largest allowed page margin.
	MaxMarginMM = 100
	// MaxRunningTextLength bounds header and footer text.
	MaxRunningTextLength = 200

	charWidthMM  = 2.54
	lineHeightMM = 25.4 / 6
	// A header takes a text row plus a separator row.
	headerRows  = 2
	minBodyRows = 5
	minTextCols = 20
)

// Margins are page margins in millimeters.
type Margins struct {
	Top    int `json:"top"`
	Right  int `json:"right"`
	Bottom int `json:"bottom"`
	Left   int `json:"left"`
}

// Layout describes how a document is paginated for printing. It is persisted
// separately from the Markdown body so the body stays readable everywhere.
//
// Header and Footer hold up to three "|"-separated parts: one part is left
// aligned, two are left and right, and three are left, center, and right.
// The tokens {title}, {page}, and {pages} are replaced when printing.
type Layout struct {
	Version     int     `json:"version"`
	PageSize    string  `json:"page_size"`
	Orientation string  `json:"orientation"`
	Margins     Margins `json:"margins_mm"`
	Columns     int     `json:"columns"`
	Header      string  `json:"header"`
	Footer      string  `json:"footer"`
	PageNumbers string  `json:"page_numbers"`
}

// DefaultLayout returns a US Letter portrait layout with one-inch margins.
func DefaultLayout() Layout {
	return Layout{
		Version: LayoutVersion, PageSize: PageLetter, Orientation: Portrait,
		Margins: Margins{Top: 25, Right: 25, Bottom: 25, Left: 25},
		Columns: 1, PageNumbers: NumbersCenter,
	}
}

// Geometry is a layout measured in monospaced printer characters and lines
// (10 characters and 6 lines per inch).
type Geometry struct {
	PageCols, PageRows int
	LeftCols, TopRows  int
	BottomRows         int
	TextCols, TextRows int
	// HeaderRows and FooterRows are the rows reserved inside the text area.
	HeaderRows, FooterRows int
}

// BodyRows is the number of rows available to document content.
func (g Geometry) BodyRows() int { return g.TextRows - g.HeaderRows - g.FooterRows }

func pageSizeMM(size, orientation string) (float64, float64, error) {
	var w, h float64
	switch size {
	case PageLetter:
		w, h = 215.9, 279.4
	case PageA4:
		w, h = 210, 297
	case PageLegal:
		w, h = 215.9, 355.6
	default:
		return 0, 0, fmt.Errorf("unsupported page size %q", size)
	}
	switch orientation {
	case Portrait:
	case Landscape:
		w, h = h, w
	default:
		return 0, 0, fmt.Errorf("unsupported orientation %q", orientation)
	}
	return w, h, nil
}

func floorFuzzy(v float64) int { return int(math.Floor(v + 1e-6)) }
func roundInt(v float64) int   { return int(math.Round(v)) }

func (l Layout) numbered() bool { return l.PageNumbers != NumbersNone && l.PageNumbers != "" }

// Geometry converts the layout to printer characters and lines.
func (l Layout) Geometry() (Geometry, error) {
	w, h, err := pageSizeMM(l.PageSize, l.Orientation)
	if err != nil {
		return Geometry{}, err
	}
	g := Geometry{
		PageCols: floorFuzzy(w / charWidthMM), PageRows: floorFuzzy(h / lineHeightMM),
		LeftCols:   roundInt(float64(l.Margins.Left) / charWidthMM),
		TopRows:    roundInt(float64(l.Margins.Top) / lineHeightMM),
		BottomRows: roundInt(float64(l.Margins.Bottom) / lineHeightMM),
	}
	rightCols := roundInt(float64(l.Margins.Right) / charWidthMM)
	g.TextCols = g.PageCols - g.LeftCols - rightCols
	g.TextRows = g.PageRows - g.TopRows - g.BottomRows
	if l.Header != "" {
		g.HeaderRows = headerRows
	}
	footerLines := 0
	if l.Footer != "" {
		footerLines++
	}
	if l.numbered() {
		footerLines++
	}
	if footerLines > 0 {
		g.FooterRows = footerLines + 1
	}
	return g, nil
}

// ColumnWidth returns the character width of each column when cols columns
// share textCols characters.
func ColumnWidth(textCols, cols int) int {
	if cols < 1 {
		cols = 1
	}
	return (textCols - ColumnGutter*(cols-1)) / cols
}

// Validate checks that the layout is supported and leaves usable page space.
func (l Layout) Validate() error {
	if l.Version != LayoutVersion {
		return fmt.Errorf("unsupported layout version %d", l.Version)
	}
	for name, value := range map[string]int{
		"top": l.Margins.Top, "right": l.Margins.Right, "bottom": l.Margins.Bottom, "left": l.Margins.Left,
	} {
		if value < 0 || value > MaxMarginMM {
			return fmt.Errorf("%s margin must be between 0 and %d mm", name, MaxMarginMM)
		}
	}
	if l.Columns < 1 || l.Columns > MaxColumns {
		return fmt.Errorf("columns must be between 1 and %d", MaxColumns)
	}
	switch l.PageNumbers {
	case NumbersNone, NumbersLeft, NumbersCenter, NumbersRight:
	default:
		return fmt.Errorf("unsupported page number placement %q", l.PageNumbers)
	}
	for name, text := range map[string]string{"header": l.Header, "footer": l.Footer} {
		if utf8.RuneCountInString(text) > MaxRunningTextLength {
			return fmt.Errorf("%s is longer than %d characters", name, MaxRunningTextLength)
		}
		for _, r := range text {
			if unicode.IsControl(r) {
				return fmt.Errorf("%s contains a control character", name)
			}
		}
	}
	g, err := l.Geometry()
	if err != nil {
		return err
	}
	if g.TextCols < minTextCols || g.BodyRows() < minBodyRows {
		return errors.New("margins leave too little room for page content")
	}
	if ColumnWidth(g.TextCols, l.Columns) < MinColumnWidth {
		return errors.New("columns are too narrow for the page width")
	}
	return nil
}
