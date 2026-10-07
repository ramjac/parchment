package recovery

import (
	"context"
	"encoding/json"
	"time"
)

// Draft is an unsaved editor snapshot stored separately from canonical artifacts.
type Draft struct {
	ID        string          `json:"id"`
	Kind      string          `json:"kind"`
	Title     string          `json:"title"`
	Artifact  string          `json:"artifact,omitempty"`
	Created   bool            `json:"created,omitempty"`
	Proposal  bool            `json:"proposal,omitempty"`
	Data      json.RawMessage `json:"data"`
	UpdatedAt time.Time       `json:"updated_at"`
}

// Store persists editor snapshots for optional recovery after interruption.
type Store interface {
	ListRecovery(context.Context) ([]Draft, error)
	SaveRecovery(context.Context, Draft) error
	DeleteRecovery(context.Context, string) error
}
