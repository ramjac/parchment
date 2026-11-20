package document

import (
	"strings"
	"testing"
	"time"

	"example.com/parchment/internal/artifact"
)

func TestTrackedIndependentEditsInOneSave(t *testing.T) {
	before := "alpha and a long unchanged middle passage and omega"
	after := "ALPHA and a long unchanged middle passage and OMEGA"
	d := Document{Body: before}
	if err := EditTrackedBody(&d, after, true, time.Now()); err != nil {
		t.Fatal(err)
	}
	if len(d.TrackedChanges) != 2 {
		t.Fatalf("independent edits: %+v", d.TrackedChanges)
	}
	if err := ResolveTrackedChange(&d, d.TrackedChanges[0].ID, false); err != nil {
		t.Fatal(err)
	}
	if d.Body != strings.Replace(after, "ALPHA", "alpha", 1) || len(d.TrackedChanges) != 1 {
		t.Fatalf("reject first: %q, %+v", d.Body, d.TrackedChanges)
	}
	if err := ResolveTrackedChange(&d, "", false); err != nil || d.Body != before {
		t.Fatalf("reject remaining: %q, %v", d.Body, err)
	}
}

func TestTrackedIndependentEditsAroundUnchangedImage(t *testing.T) {
	name := "image-0123456789abcdef.png"
	ref := artifact.ImageMarkdown("pic", name)
	before := "alpha and a long unchanged passage " + ref + " with more unchanged text omega"
	after := "ALPHA and a long unchanged passage " + ref + " with more unchanged text OMEGA"
	d := Document{Body: before}
	if err := EditTrackedBody(&d, after, true, time.Now()); err != nil {
		t.Fatal(err)
	}
	if len(d.TrackedChanges) != 2 {
		t.Fatalf("independent edits around image: %+v", d.TrackedChanges)
	}
	for _, change := range d.TrackedChanges {
		if strings.Contains(change.Deleted, ref) || strings.Contains(change.Inserted, ref) {
			t.Fatalf("unchanged image included in edit: %+v", change)
		}
	}
	if err := ResolveTrackedChange(&d, d.TrackedChanges[0].ID, false); err != nil || len(d.TrackedChanges) != 1 {
		t.Fatalf("reject first edit: %+v, %v", d.TrackedChanges, err)
	}
	if err := ResolveTrackedChange(&d, "", false); err != nil || d.Body != before {
		t.Fatalf("reject second edit: %q, %v", d.Body, err)
	}
}

func TestTrackedSingleSaveImageMoveIsOneReviewAction(t *testing.T) {
	name := "image-0123456789abcdef.png"
	ref := artifact.ImageMarkdown("pic", name)
	before := "one " + ref + " two with a long unchanged passage"
	after := "one two with a long unchanged passage " + ref
	d := Document{Body: before}
	if err := EditTrackedBody(&d, after, true, time.Now()); err != nil {
		t.Fatal(err)
	}
	if len(d.TrackedChanges) != 1 || !strings.Contains(d.TrackedChanges[0].Deleted, ref) ||
		!strings.Contains(d.TrackedChanges[0].Inserted, ref) {
		t.Fatalf("single-save image move was not atomic: %+v", d.TrackedChanges)
	}
	if err := ResolveTrackedChange(&d, d.TrackedChanges[0].ID, false); err != nil || d.Body != before {
		t.Fatalf("reject image move: %q, %+v, %v", d.Body, d.TrackedChanges, err)
	}
}
