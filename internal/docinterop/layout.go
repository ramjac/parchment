package docinterop

import (
	"example.com/parchment/internal/document"
	"math"
	"strings"
)

// TwipToMM converts twips (1/1440 inch) to millimeters.
func TwipToMM(twips int) float64 {
	return float64(twips) / 1440.0 * 25.4
}

// EMUToMM converts English Metric Units to millimeters.
func EMUToMM(emu int) float64 {
	return float64(emu) / 36000.0
}

// MapMargins maps generic float margins in mm to clamped, rounded document margins.
// Returns a warning if any rounding/clamping alters geometry beyond 1 mm.
func MapMargins(top, right, bottom, left float64) (document.Margins, *Warning) {
	var warning *Warning

	roundClamp := func(v float64) int {
		clamped := v
		if clamped < 0 {
			clamped = 0
		} else if clamped > document.MaxMarginMM {
			clamped = document.MaxMarginMM
		}
		rounded := int(math.Round(clamped))

		if math.Abs(float64(rounded)-v) > 1.0 {
			if warning == nil {
				warning = &Warning{Message: "margin clamping or rounding altered geometry beyond 1 mm"}
			}
		}
		return rounded
	}

	return document.Margins{
		Top:    roundClamp(top),
		Right:  roundClamp(right),
		Bottom: roundClamp(bottom),
		Left:   roundClamp(left),
	}, warning
}

// MapColumns maps a column count to a clamped 1..document.MaxColumns range.
func MapColumns(cols int) (int, *Warning) {
	if cols < 1 {
		return 1, &Warning{Message: "columns clamped to 1"}
	}
	if cols > document.MaxColumns {
		return document.MaxColumns, &Warning{Message: "columns clamped to max"}
	}
	return cols, nil
}

// MapPageSize maps a page size string to a recognized size and orientation,
// returning a warning if it had to snap to a default.
func MapPageSize(w, h float64) (size, orientation string, warning *Warning) {
	if w > h {
		orientation = document.Landscape
		w, h = h, w
	} else {
		orientation = document.Portrait
	}

	sizes := []struct {
		name string
		w    float64
		h    float64
	}{
		{document.PageLetter, 215.9, 279.4},
		{document.PageA4, 210.0, 297.0},
		{document.PageLegal, 215.9, 355.6},
	}

	best := sizes[0].name
	minDist := math.MaxFloat64

	for _, s := range sizes {
		dist := math.Hypot(s.w-w, s.h-h)
		if dist < minDist {
			minDist = dist
			best = s.name
		}
	}

	if minDist > 1.0 {
		warning = &Warning{Message: "snapped custom page dimensions to closest supported size"}
	}

	return best, orientation, warning
}

// MapFontFamily maps standard font families to Parchment's font defaults.
func MapFontFamily(name string) string {
	lname := strings.ToLower(name)
	if strings.Contains(lname, "courier") || strings.Contains(lname, "mono") {
		return "courier"
	}
	if strings.Contains(lname, "times") || strings.Contains(lname, "serif") && !strings.Contains(lname, "sans") {
		return "times"
	}
	// Default sans-serif fallback
	return "helvetica"
}

// MapHeaderFooter checks for dynamic fields or valid strings and emits a warning if complex features are used.
func MapHeaderFooter(text string, isComplex bool) (string, *Warning) {
	if isComplex {
		return text, &Warning{Message: "collapsed distinct headers/footers to single document style"}
	}
	return text, nil
}
