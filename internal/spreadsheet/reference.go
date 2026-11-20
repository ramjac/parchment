package spreadsheet

import (
	"fmt"
	"strings"
)

type ReferenceKind string

const (
	SheetReferenceKind ReferenceKind = "sheet"
	ChartReferenceKind ReferenceKind = "chart"
)

type Reference struct {
	Kind ReferenceKind
	ID   string
}

// FormatReference creates the block-level Markdown reference for a host object.
func FormatReference(reference Reference) (string, error) {
	if !validReferenceID(reference.ID) {
		return "", fmt.Errorf("invalid embedded spreadsheet reference ID %q", reference.ID)
	}
	switch reference.Kind {
	case SheetReferenceKind:
		return "![Spreadsheet](parchment-sheet:" + reference.ID + ")", nil
	case ChartReferenceKind:
		return "![Chart](parchment-chart:" + reference.ID + ")", nil
	default:
		return "", fmt.Errorf("unsupported embedded reference kind %q", reference.Kind)
	}
}

// ParseReference recognizes a complete block-level embedded object reference.
func ParseReference(source string) (Reference, bool) {
	source = strings.TrimSpace(source)
	if !strings.HasPrefix(source, "![") {
		return Reference{}, false
	}
	closeLabel := strings.Index(source, "](")
	if closeLabel < 2 || !strings.HasSuffix(source, ")") {
		return Reference{}, false
	}
	alt := source[2:closeLabel]
	destination := source[closeLabel+2 : len(source)-1]
	for _, prefix := range []struct {
		scheme string
		kind   ReferenceKind
	}{
		{"parchment-sheet:", SheetReferenceKind},
		{"parchment-chart:", ChartReferenceKind},
	} {
		if !strings.HasPrefix(destination, prefix.scheme) {
			continue
		}
		id := strings.TrimPrefix(destination, prefix.scheme)
		if alt == "" || !validReferenceID(id) {
			return Reference{}, false
		}
		return Reference{Kind: prefix.kind, ID: id}, true
	}
	return Reference{}, false
}

func validReferenceID(id string) bool {
	if id == "" || len(id) > 128 {
		return false
	}
	for _, char := range id {
		if !((char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') ||
			(char >= '0' && char <= '9') || char == '-' || char == '_') {
			return false
		}
	}
	return true
}
