package artifact

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

type Kind string

const (
	NoteKind         Kind = "note"
	DocumentKind     Kind = "document"
	SpreadsheetKind  Kind = "spreadsheet"
	PresentationKind Kind = "presentation"
	ImageKind        Kind = "image"
	FormatVersion         = 1
)

// Artifact contains metadata shared by every artifact type.
type Artifact struct {
	ID            string    `json:"id"`
	Kind          Kind      `json:"kind"`
	Title         string    `json:"title"`
	CreatedAt     time.Time `json:"created_at"`
	ModifiedAt    time.Time `json:"modified_at"`
	FormatVersion int       `json:"format_version"`
	Tags          []string  `json:"tags,omitempty"`
	Links         []string  `json:"links,omitempty"`
	// Path is the file the artifact was read from or will be written to. It
	// identifies the artifact to repositories and is not persisted.
	Path string `json:"-"`
}

// Validate checks the common metadata required for a persisted artifact.
func (a Artifact) Validate() error {
	if a.ID == "" {
		return errors.New("artifact ID is required")
	}
	if a.Kind != NoteKind && a.Kind != DocumentKind && a.Kind != SpreadsheetKind &&
		a.Kind != PresentationKind && a.Kind != ImageKind {
		return fmt.Errorf("unsupported artifact kind %q", a.Kind)
	}
	if strings.TrimSpace(a.Title) == "" {
		return errors.New("artifact title is required")
	}
	if a.CreatedAt.IsZero() || a.ModifiedAt.IsZero() {
		return errors.New("artifact timestamps are required")
	}
	if a.FormatVersion != FormatVersion {
		return fmt.Errorf("unsupported artifact format version %d", a.FormatVersion)
	}
	return nil
}
