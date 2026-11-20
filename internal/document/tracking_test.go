package document

import (
	"strings"
	"testing"
	"time"

	"example.com/parchment/internal/artifact"
)

func TestTrackedEditsCoalesceAndResolve(t *testing.T) {
	now := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	d := Document{Body: "one café three"}
	if err := EditTrackedBody(&d, "one café bright three", true, now); err != nil {
		t.Fatal(err)
	}
	if err := EditTrackedBody(&d, "one café brighter three", true, now); err != nil {
		t.Fatal(err)
	}
	if len(d.TrackedChanges) != 1 || d.TrackedChanges[0].Inserted != "brighter " {
		t.Fatalf("overlapping edits did not coalesce: %+v", d.TrackedChanges)
	}
	first := d.TrackedChanges[0].ID
	if err := EditTrackedBody(&d, "ONE café brighter three", true, now); err != nil {
		t.Fatal(err)
	}
	if len(d.TrackedChanges) != 2 {
		t.Fatalf("independent changes were not retained: %+v", d.TrackedChanges)
	}
	if err := ResolveTrackedChange(&d, first, false); err != nil {
		t.Fatal(err)
	}
	if d.Body != "ONE café three" || len(d.TrackedChanges) != 1 {
		t.Fatalf("reject produced %q, %+v", d.Body, d.TrackedChanges)
	}
	if err := ResolveTrackedChange(&d, "", true); err != nil || d.Body != "ONE café three" || len(d.TrackedChanges) != 0 {
		t.Fatalf("accept all: %q, %+v, %v", d.Body, d.TrackedChanges, err)
	}
}

func TestTrackedDeletionAndUntrackedEditSafety(t *testing.T) {
	now := time.Now().UTC()
	d := Document{Body: "αβγ rest"}
	if err := EditTrackedBody(&d, "αγ rest", true, now); err != nil {
		t.Fatal(err)
	}
	if err := EditTrackedBody(&d, "αγ rest!", false, now); err != nil {
		t.Fatal(err)
	}
	if err := EditTrackedBody(&d, "αxγ rest!", false, now); err == nil {
		t.Fatal("untracked edit at a pending deletion was accepted")
	}
	if d.Body != "αγ rest!" {
		t.Fatal("failed edit mutated the document")
	}
	if err := ResolveTrackedChange(&d, "", false); err != nil || d.Body != "αβγ rest!" {
		t.Fatalf("reject all = %q, %v", d.Body, err)
	}
}

func TestTrackedChangeRejectsTamperedAnchors(t *testing.T) {
	d := Document{Body: "some text"}
	if err := EditTrackedBody(&d, "some new text", true, time.Now()); err != nil {
		t.Fatal(err)
	}

	d.Body = strings.Replace(d.Body, "new", "bad", 1)
	if err := ResolveTrackedChange(&d, "", false); err == nil {
		t.Fatal("tampered insertion accepted")
	}
}

func TestTrackedImageMoveIsOneReviewAction(t *testing.T) {
	name := "image-0123456789abcdef.png"
	ref := artifact.ImageMarkdown("pic", name)
	if len(artifact.ReferencedImages(ref)) == 0 {
		t.Fatal("invalid image fixture")
	}
	d := Document{Body: "one " + ref + " two"}
	original := d.Body
	if err := EditTrackedBody(&d, "one  two", true, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := EditTrackedBody(&d, "one  two "+ref, true, time.Now()); err != nil {
		t.Fatal(err)
	}
	if len(d.TrackedChanges) != 2 || d.TrackedChanges[0].Group == "" ||
		d.TrackedChanges[0].Group != d.TrackedChanges[1].Group {
		t.Fatalf("image move was not grouped: %+v", d.TrackedChanges)
	}
	if err := ResolveTrackedChange(&d, d.TrackedChanges[1].ID, false); err != nil ||
		d.Body != original || len(d.TrackedChanges) != 0 {
		t.Fatalf("reject image move: %q, %+v, %v", d.Body, d.TrackedChanges, err)
	}
}
