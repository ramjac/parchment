package artifact

import (
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"time"
)

type Kind string

const (
	NoteKind         Kind = "note"
	DocumentKind     Kind = "document"
	SpreadsheetKind  Kind = "spreadsheet"
	PresentationKind Kind = "presentation"
	ImageKind        Kind = "image"
	FormatVersion         = 2
)

var idPrefixes = map[Kind]byte{
	NoteKind:         'n',
	DocumentKind:     'd',
	SpreadsheetKind:  's',
	PresentationKind: 'p',
	ImageKind:        'i',
}

// NewID creates a 128-bit random artifact ID using a kind prefix and compact
// lowercase base-36 encoding.
func NewID(kind Kind) (string, error) {
	prefix, ok := idPrefixes[kind]
	if !ok {
		return "", fmt.Errorf("unsupported artifact kind %q", kind)
	}
	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		return "", fmt.Errorf("generate artifact ID: %w", err)
	}
	return string(prefix) + new(big.Int).SetBytes(random).Text(36), nil
}

// ValidID reports whether id matches the compact artifact ID format.
func ValidID(id string) bool {
	if len(id) < 2 || len(id) > 26 {
		return false
	}
	var validPrefix bool
	for _, prefix := range idPrefixes {
		if id[0] == prefix {
			validPrefix = true
			break
		}
	}
	if !validPrefix {
		return false
	}
	payload := id[1:]
	for i := 0; i < len(payload); i++ {
		if c := payload[i]; (c < '0' || c > '9') && (c < 'a' || c > 'z') {
			return false
		}
	}
	if len(payload) > 1 && payload[0] == '0' {
		return false
	}
	value, ok := new(big.Int).SetString(payload, 36)
	return ok && value.BitLen() <= 128 && value.Text(36) == payload
}

// ValidIDForKind reports whether id is valid and uses kind's required prefix.
func ValidIDForKind(id string, kind Kind) bool {
	prefix, ok := idPrefixes[kind]
	return ok && ValidID(id) && id[0] == prefix
}

// Artifact contains runtime metadata shared by every artifact type. The
// artifact-file envelope persists only kind, timestamps, and format version;
// identity, title, and path are runtime values derived from the file.
type Artifact struct {
	ID            string    `json:"id"`
	Kind          Kind      `json:"kind"`
	Title         string    `json:"title"`
	CreatedAt     time.Time `json:"created_at"`
	ModifiedAt    time.Time `json:"modified_at"`
	FormatVersion int       `json:"format_version"`
	// Path is the file the artifact was read from or will be written to.
	Path string `json:"-"`
}

// Validate checks the common metadata required for a persisted artifact.
func (a Artifact) Validate() error {
	if a.Kind != NoteKind && a.Kind != DocumentKind && a.Kind != SpreadsheetKind &&
		a.Kind != PresentationKind && a.Kind != ImageKind {
		return fmt.Errorf("unsupported artifact kind %q", a.Kind)
	}
	if !ValidIDForKind(a.ID, a.Kind) {
		return errors.New("artifact ID is invalid for its kind")
	}
	if a.CreatedAt.IsZero() || a.ModifiedAt.IsZero() {
		return errors.New("artifact timestamps are required")
	}
	if a.FormatVersion != FormatVersion {
		return fmt.Errorf("unsupported artifact format version %d", a.FormatVersion)
	}
	return nil
}
