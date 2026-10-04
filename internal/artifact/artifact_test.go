package artifact

import (
	"testing"
	"time"
)

func TestNewIDUsesCompactKindPrefixedEncoding(t *testing.T) {
	kinds := []struct {
		kind   Kind
		prefix byte
	}{
		{NoteKind, 'n'},
		{DocumentKind, 'd'},
		{SpreadsheetKind, 's'},
		{PresentationKind, 'p'},
		{ImageKind, 'i'},
	}
	seen := make(map[string]bool)
	for _, test := range kinds {
		id, err := NewID(test.kind)
		if err != nil {
			t.Fatal(err)
		}
		if len(id) < 2 || len(id) > 26 || id[0] != test.prefix || !ValidID(id) {
			t.Errorf("NewID(%q) = %q, which is not a valid compact ID", test.kind, id)
		}
		if seen[id] {
			t.Errorf("duplicate generated ID %q", id)
		}
		seen[id] = true
	}
}

func TestValidIDRejectsMalformedValues(t *testing.T) {
	for _, id := range []string{
		"",
		"n",
		"x1",
		"N1",
		"n01",
		"0123456789abcdef0123456789abcdef",
		"n" + string(make([]byte, 26)),
		"n" + "zzzzzzzzzzzzzzzzzzzzzzzzz",
		"n!",
	} {
		if ValidID(id) {
			t.Errorf("ValidID(%q) = true", id)
		}
	}
	for _, id := range []string{"n0", "n1", "d2", "s3", "p4", "i5"} {
		if !ValidID(id) {
			t.Errorf("ValidID(%q) = false", id)
		}
	}
}

func TestArtifactValidationRequiresIDPrefixForKind(t *testing.T) {
	now := time.Now().UTC()
	for _, test := range []struct {
		kind Kind
		id   string
		ok   bool
	}{
		{NoteKind, "n1", true},
		{DocumentKind, "d1", true},
		{SpreadsheetKind, "s1", true},
		{PresentationKind, "p1", true},
		{ImageKind, "i1", true},
		{DocumentKind, "n1", false},
		{NoteKind, "d1", false},
		{Kind("unknown"), "n1", false},
	} {
		t.Run(string(test.kind)+"/"+test.id, func(t *testing.T) {
			a := Artifact{
				ID: test.id, Kind: test.kind, Title: "Example",
				CreatedAt: now, ModifiedAt: now,
				FormatVersion: FormatVersion, Location: "parchment/artifacts/" + test.id + "/content.md",
			}
			err := a.Validate()
			if (err == nil) != test.ok {
				t.Fatalf("Artifact.Validate() error = %v, want valid=%t", err, test.ok)
			}
		})
	}
}
