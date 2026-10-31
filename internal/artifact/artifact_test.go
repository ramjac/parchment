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
				FormatVersion: FormatVersion,
			}
			err := a.Validate()
			if (err == nil) != test.ok {
				t.Fatalf("Artifact.Validate() error = %v, want valid=%t", err, test.ok)
			}
		})
	}
}

func TestValidIDRejectsSignedPayloads(t *testing.T) {
	for _, id := range []string{"n-1", "n+1", "n 1", "nA", "n1_"} {
		if ValidID(id) {
			t.Errorf("ValidID(%q) = true", id)
		}
	}
}

func TestValidIDForKind(t *testing.T) {
	for _, test := range []struct {
		id   string
		kind Kind
		ok   bool
	}{
		// Valid cases
		{"n1", NoteKind, true},
		{"d1", DocumentKind, true},
		{"s1", SpreadsheetKind, true},
		{"p1", PresentationKind, true},
		{"i1", ImageKind, true},

		// Invalid kinds
		{"n1", Kind("unknown"), false},
		{"d1", NoteKind, false},
		{"n1", DocumentKind, false},

		// Invalid IDs
		{"", NoteKind, false},
		{"", DocumentKind, false},
		{"", SpreadsheetKind, false},
		{"", PresentationKind, false},
		{"", ImageKind, false},
		{"d0", DocumentKind, true},
		{"d1234567890abcdef1234567890", DocumentKind, false},
		{"S1", SpreadsheetKind, false},
		{"p!", PresentationKind, false},
		{"x1", NoteKind, false},
		{"N1", NoteKind, false},
		{"n", NoteKind, false},
		{"n01", NoteKind, false},
		{"0123456789abcdef0123456789abcdef", NoteKind, false},
		{"n-1", NoteKind, false},
	} {
		t.Run(string(test.kind)+"/"+test.id, func(t *testing.T) {
			if got := ValidIDForKind(test.id, test.kind); got != test.ok {
				t.Errorf("ValidIDForKind(%q, %q) = %v, want %v", test.id, test.kind, got, test.ok)
			}
		})
	}
}

func TestArtifactValidationErrors(t *testing.T) {
	now := time.Now().UTC()
	validID := "n1"
	validKind := NoteKind

	tests := []struct {
		name string
		art  Artifact
		want string
	}{
		{
			name: "missing CreatedAt",
			art: Artifact{
				ID: validID, Kind: validKind, Title: "Example",
				ModifiedAt:    now,
				FormatVersion: FormatVersion,
			},
			want: "artifact timestamps are required",
		},
		{
			name: "missing ModifiedAt",
			art: Artifact{
				ID: validID, Kind: validKind, Title: "Example",
				CreatedAt:     now,
				FormatVersion: FormatVersion,
			},
			want: "artifact timestamps are required",
		},
		{
			name: "unsupported format version",
			art: Artifact{
				ID: validID, Kind: validKind, Title: "Example",
				CreatedAt: now, ModifiedAt: now,
				FormatVersion: 1, // Invalid, since FormatVersion is 2
			},
			want: "unsupported artifact format version 1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.art.Validate()
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tt.want)
			}
			if err.Error() != tt.want {
				t.Errorf("got error %q, want %q", err.Error(), tt.want)
			}
		})
	}
}
