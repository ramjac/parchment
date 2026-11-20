package document

import (
	"math/rand"
	"testing"
	"time"
)

func TestTrackedEditsRejectAllReconstructsOriginal(t *testing.T) {
	random := rand.New(rand.NewSource(42))
	alphabet := []rune("abcd é猫\n")
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for trial := 0; trial < 300; trial++ {
		original := make([]rune, random.Intn(35))
		for i := range original {
			original[i] = alphabet[random.Intn(len(alphabet))]
		}
		d := Document{Body: string(original)}
		for step := 0; step < 25; step++ {
			current := []rune(d.Body)
			start := random.Intn(len(current) + 1)
			end := start + random.Intn(len(current)-start+1)
			insertion := make([]rune, random.Intn(7))
			for i := range insertion {
				insertion[i] = alphabet[random.Intn(len(alphabet))]
			}
			next := append(append(append([]rune(nil), current[:start]...), insertion...), current[end:]...)
			if err := EditTrackedBody(&d, string(next), true, now); err != nil {
				t.Fatalf("trial %d step %d: edit %q to %q: %v", trial, step, d.Body, string(next), err)
			}
			if len(d.TrackedChanges) > 0 && random.Intn(4) == 0 {
				id := d.TrackedChanges[random.Intn(len(d.TrackedChanges))].ID
				if err := ResolveTrackedChange(&d, id, false); err != nil {
					t.Fatalf("trial %d step %d: reject: %v", trial, step, err)
				}
			}
		}
		if err := ResolveTrackedChange(&d, "", false); err != nil || d.Body != string(original) {
			t.Fatalf("trial %d: got %q; want %q; changes %+v; err %v",
				trial, d.Body, string(original), d.TrackedChanges, err)
		}
	}
}
