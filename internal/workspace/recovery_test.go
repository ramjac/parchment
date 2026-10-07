package workspace

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"example.com/parchment/internal/recovery"
)

func TestRecoveryDraftsPersistPrivatelyAndCanBeDeleted(t *testing.T) {
	ws := openTestWorkspace(t, t.TempDir())
	draft := recovery.Draft{
		ID: "0123456789abcdef0123456789abcdef", Kind: "note", Title: "Unsaved",
		Data: json.RawMessage(`{"body":"draft"}`), UpdatedAt: time.Now().UTC(),
	}
	if err := ws.SaveRecovery(context.Background(), draft); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(ws.root, ".parchment", "recovery", draft.ID+".json")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("recovery file permissions = %o", info.Mode().Perm())
	}
	drafts, err := ws.ListRecovery(context.Background())
	if err != nil || len(drafts) != 1 || drafts[0].Title != draft.Title {
		t.Fatalf("recovery drafts = %+v, %v", drafts, err)
	}
	if err := ws.DeleteRecovery(context.Background(), draft.ID); err != nil {
		t.Fatal(err)
	}
	drafts, err = ws.ListRecovery(context.Background())
	if err != nil || len(drafts) != 0 {
		t.Fatalf("recovery drafts after delete = %+v, %v", drafts, err)
	}
}
