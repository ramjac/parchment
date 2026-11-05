package docinterop

import (
	"example.com/parchment/internal/document"
	"math"
)

// MapMargins maps generic float margins in mm to clamped, rounded document margins.
// Returns a warning if any rounding/clamping alters geometry beyond 1 mm.
func MapMargins(top, right, bottom, left float64) (document.Margins, *Warning) {
	var warning *Warning

	roundClamp := func(v float64, name string) int {
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
		Top:    roundClamp(top, "top"),
		Right:  roundClamp(right, "right"),
		Bottom: roundClamp(bottom, "bottom"),
		Left:   roundClamp(left, "left"),
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
    // Basic fallback for now
    if w > h {
        return document.PageLetter, document.Landscape, &Warning{Message: "snapped custom page dimensions to closest supported size"}
    }
    return document.PageLetter, document.Portrait, &Warning{Message: "snapped custom page dimensions to closest supported size"}
}
