package artifact

import "testing"

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
