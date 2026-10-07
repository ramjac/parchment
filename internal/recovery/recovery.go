package recovery

import (
	"context"
	"encoding/json"
	"time"
)

// Draft is an unsaved editor snapshot for one artifact file. Drafts are kept
// in Parchment's state directory, never beside the artifact.
type Draft struct {
	// Path is the absolute artifact file path the draft belongs to.
	Path      string          `json:"path"`
	Kind      string          `json:"kind"`
	Data      json.RawMessage `json:"data"`
	UpdatedAt time.Time       `json:"updated_at"`
}

// Store persists at most one editor snapshot per artifact file so unsaved
// work can be offered for recovery when the file is opened again.
type Store interface {
	// LoadRecovery returns the draft for path. The boolean is false when no
	// draft exists.
	LoadRecovery(context.Context, string) (Draft, bool, error)
	SaveRecovery(context.Context, Draft) error
	DeleteRecovery(context.Context, string) error
}
