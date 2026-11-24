package tui

import (
	"context"
	"testing"

	"github.com/ramjac/parchment/internal/artifact"
	"github.com/ramjac/parchment/internal/document"
)

func TestDocumentTrackingRecoveryAndReview(t *testing.T) {
	h := newDocumentHarness(t, document.Draft{Body: "original"})
	s := h.m.documents
	h.key("f3")
	s.body.SetValue("first tracked")
	h.key("ctrl+s")
	if len(s.snapshot.TrackedChanges) == 0 {
		t.Fatal("tracked edit was not saved")
	}
	s.body.SetValue("second tracked")
	h.run(s.saveDocumentRecovery(s.autosaveSession))
	if !h.hasDraft() {
		t.Fatal("tracked draft was not saved")
	}
	h.open(artifact.DocumentKind)
	h.key("r")
	s = h.m.documents
	if !s.snapshot.Tracking || len(s.snapshot.TrackedChanges) == 0 || s.body.Value() != "second tracked" {
		t.Fatalf("recovered tracked state: %+v body=%q", s.snapshot, s.body.Value())
	}
	h.key("ctrl+s")
	h.key("f4")
	h.key("R")
	d, err := h.docs.Get(context.Background(), h.path)
	if err != nil || d.Body != "original" || len(d.TrackedChanges) != 0 {
		t.Fatalf("reject all after recovery: %+v, %v", d, err)
	}
}
