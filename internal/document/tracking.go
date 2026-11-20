package document

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"example.com/parchment/internal/artifact"
)

const (
	maxTrackedChanges = 4096
	maxTrackedBytes   = 16 << 20
)

// TrackedChange records a replacement at a UTF-8 byte boundary in the current
// body. Inserted text remains in the body; deleted text is kept for rejection.
type TrackedChange struct {
	ID        string    `json:"id"`
	Group     string    `json:"group,omitempty"`
	Offset    int       `json:"offset"`
	Deleted   string    `json:"deleted,omitempty"`
	Inserted  string    `json:"inserted,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// ValidateTracking checks that pending anchors match the canonical body.
func ValidateTracking(d Document) error {
	return validateTrackedChanges(d.Body, d.TrackedChanges)
}

func validateTrackedChanges(body string, changes []TrackedChange) error {
	if !utf8.ValidString(body) || len(changes) > maxTrackedChanges {
		return errors.New("invalid document tracking data")
	}
	seen := make(map[string]bool, len(changes))
	previousEnd := -1
	total := 0
	for _, change := range changes {
		total += len(change.Deleted) + len(change.Inserted)
		end := change.Offset + len(change.Inserted)
		if !validChangeID(change.ID) || seen[change.ID] || change.CreatedAt.IsZero() ||
			(change.Group != "" && !validChangeID(change.Group)) ||
			!utf8.ValidString(change.Deleted) || !utf8.ValidString(change.Inserted) ||
			(change.Deleted == "" && change.Inserted == "") || change.Offset < 0 ||
			end < change.Offset || end > len(body) || change.Offset <= previousEnd ||
			(change.Offset < len(body) && !utf8.RuneStart(body[change.Offset])) ||
			(end < len(body) && !utf8.RuneStart(body[end])) ||
			body[change.Offset:end] != change.Inserted || total > maxTrackedBytes {
			return errors.New("invalid or overlapping document tracked change")
		}
		seen[change.ID] = true
		previousEnd = end
	}
	return nil
}

func newTrackedID() (string, error) {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(id[:]), nil
}

func EditTrackedBody(d *Document, body string, track bool, now time.Time) error {
	if !utf8.ValidString(body) {
		return errors.New("document body must be valid UTF-8")
	}
	if err := validateTrackedChanges(d.Body, d.TrackedChanges); err != nil {
		return err
	}
	old := d.Body
	if old == body {
		return nil
	}
	start, oldEnd, newEnd := trackedChangedRange(old, body)
	edits := splitTrackedEdits(old[start:oldEnd], body[start:newEnd], start)
	if len(edits) <= 1 {
		return editTrackedSplice(d, body, track, now)
	}
	working := *d
	working.TrackedChanges = append([]TrackedChange(nil), d.TrackedChanges...)
	for i := len(edits) - 1; i >= 0; i-- {
		edit := edits[i]
		next := working.Body[:edit.start] + edit.inserted + working.Body[edit.end:]
		if err := editTrackedSplice(&working, next, track, now); err != nil {
			return err
		}
	}
	if working.Body != body {
		return errors.New("tracked edit did not reconstruct the requested body")
	}
	d.Body, d.TrackedChanges = working.Body, working.TrackedChanges
	return nil
}

func trackedChangedRange(old, body string) (int, int, int) {
	start := 0
	for start < len(old) && start < len(body) && old[start] == body[start] {
		start++
	}
	for start > 0 && ((start < len(old) && !utf8.RuneStart(old[start])) ||
		(start < len(body) && !utf8.RuneStart(body[start]))) {
		start--
	}
	oldEnd, newEnd := len(old), len(body)
	for oldEnd > start && newEnd > start && old[oldEnd-1] == body[newEnd-1] {
		oldEnd--
		newEnd--
	}
	for (oldEnd < len(old) && !utf8.RuneStart(old[oldEnd])) ||
		(newEnd < len(body) && !utf8.RuneStart(body[newEnd])) {
		oldEnd++
		newEnd++
	}
	return start, oldEnd, newEnd
}

type trackedEdit struct {
	start, end int
	inserted   string
}

// splitTrackedEdits keeps distinct edits separately reviewable while bounding
// the comparison cost. Very large replacements are kept as one transaction.
func splitTrackedEdits(old, next string, base int) []trackedEdit {
	oldImages, newImages := artifact.ReferencedImages(old), artifact.ReferencedImages(next)
	for name := range oldImages {
		if newImages[name] && !imageReferenceIsStationary(old, next, name) {
			return nil
		}
	}
	a, b := []rune(old), []rune(next)
	if len(a) == 0 || len(b) == 0 || len(a)+1 > 1_000_000/(len(b)+1) {
		return nil
	}
	columns := len(b) + 1
	lcs := make([]uint32, (len(a)+1)*columns)
	for i := len(a) - 1; i >= 0; i-- {
		for j := len(b) - 1; j >= 0; j-- {
			if a[i] == b[j] {
				lcs[i*columns+j] = lcs[(i+1)*columns+j+1] + 1
			} else {
				lcs[i*columns+j] = max(lcs[(i+1)*columns+j], lcs[i*columns+j+1])
			}
		}
	}
	var edits []trackedEdit
	var current *trackedEdit
	var inserted strings.Builder
	byteOffset := base
	flush := func() {
		if current != nil {
			current.inserted = inserted.String()
			edits = append(edits, *current)
			current = nil
			inserted.Reset()
		}
	}
	for i, j := 0, 0; i < len(a) || j < len(b); {
		if i < len(a) && j < len(b) && a[i] == b[j] {
			flush()
			byteOffset += utf8.RuneLen(a[i])
			i++
			j++
			continue
		}
		if current == nil {
			current = &trackedEdit{start: byteOffset, end: byteOffset}
		}
		if i < len(a) && (j == len(b) || lcs[(i+1)*columns+j] >= lcs[i*columns+j+1]) {
			byteOffset += utf8.RuneLen(a[i])
			current.end = byteOffset
			i++
		} else {
			inserted.WriteRune(b[j])
			j++
		}
	}
	flush()
	if len(edits) < 2 {
		return edits
	}
	merged := []trackedEdit{edits[0]}
	for _, edit := range edits[1:] {
		last := &merged[len(merged)-1]
		if edit.start-last.end <= 8 {
			last.inserted += old[last.end-base:edit.start-base] + edit.inserted
			last.end = edit.end
		} else {
			merged = append(merged, edit)
		}
	}
	return merged
}

func imageReferenceIsStationary(old, next, name string) bool {
	oldOffsets, nextOffsets := imageReferenceOffsets(old, name), imageReferenceOffsets(next, name)
	if len(oldOffsets) != 1 || len(nextOffsets) != 1 {
		return false
	}
	context := func(body string, start, end int) string {
		left, right := start, end
		for i := 0; i < 8 && left > 0; i++ {
			_, size := utf8.DecodeLastRuneInString(body[:left])
			left -= size
		}
		for i := 0; i < 8 && right < len(body); i++ {
			_, size := utf8.DecodeRuneInString(body[right:])
			right += size
		}
		return body[left:start] + "\x00" + body[end:right]
	}
	return context(old, oldOffsets[0], oldOffsets[0]+len(name)) ==
		context(next, nextOffsets[0], nextOffsets[0]+len(name))
}

func imageReferenceOffsets(body, name string) []int {
	isNameByte := func(b byte) bool {
		return b == '-' || b == '_' || b >= '0' && b <= '9' ||
			b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z'
	}
	var offsets []int
	for from := 0; from < len(body); {
		relative := strings.Index(body[from:], name)
		if relative < 0 {
			break
		}
		start := from + relative
		end := start + len(name)
		if (start == 0 || !isNameByte(body[start-1])) &&
			(end == len(body) || !isNameByte(body[end])) {
			offsets = append(offsets, start)
		}
		from = end
	}
	return offsets
}

// editTrackedSplice transforms anchors and coalesces changes it intersects.
func editTrackedSplice(d *Document, body string, track bool, now time.Time) error {
	old := d.Body
	start, oldEnd, newEnd := trackedChangedRange(old, body)
	lo, hi := start, oldEnd
	selected := make([]bool, len(d.TrackedChanges))
	for expanded := true; expanded; {
		expanded = false
		for i, change := range d.TrackedChanges {
			end := change.Offset + len(change.Inserted)
			if !selected[i] && change.Offset <= hi && end >= lo {
				if !track {
					return errors.New("untracked edit intersects pending changes; review them first")
				}
				selected[i] = true
				lo, hi = min(lo, change.Offset), max(hi, end)
				expanded = true
			}
		}
	}
	original := old[lo:hi]
	var existing *TrackedChange
	groups := map[string]bool{}
	for i := len(d.TrackedChanges) - 1; i >= 0; i-- {
		if !selected[i] {
			continue
		}
		change := d.TrackedChanges[i]
		relative := change.Offset - lo
		original = original[:relative] + change.Deleted + original[relative+len(change.Inserted):]
		copy := change
		existing = &copy
		if change.Group != "" {
			groups[change.Group] = true
		}
	}
	replacement := old[lo:start] + body[start:newEnd] + old[oldEnd:hi]
	delta := len(body) - len(old)
	changes := make([]TrackedChange, 0, len(d.TrackedChanges)+1)
	for i, change := range d.TrackedChanges {
		if selected[i] {
			continue
		}
		if change.Offset > hi {
			change.Offset += delta
		}
		changes = append(changes, change)
	}
	if track && original != replacement {
		var change TrackedChange
		if existing != nil {
			change = *existing
		} else {
			id, err := newTrackedID()
			if err != nil {
				return fmt.Errorf("generate tracked change ID: %w", err)
			}
			change = TrackedChange{ID: id, CreatedAt: now.UTC()}
		}
		change.Offset, change.Deleted, change.Inserted = lo, original, replacement
		if len(groups) > 0 {
			group := ""
			for g := range groups {
				if group == "" || g < group {
					group = g
				}
			}
			change.Group = group
			for i := range changes {
				if groups[changes[i].Group] {
					changes[i].Group = group
				}
			}
		}
		// Pair matching image-reference removals and insertions as one move.
		added := artifact.ReferencedImages(change.Inserted)
		removed := artifact.ReferencedImages(change.Deleted)
		for i := range changes {
			otherAdded := artifact.ReferencedImages(changes[i].Inserted)
			otherRemoved := artifact.ReferencedImages(changes[i].Deleted)
			for name := range added {
				if otherRemoved[name] && !otherAdded[name] && !removed[name] {
					group := changes[i].Group
					if group == "" {
						group = changes[i].ID
					}
					if change.Group != "" && change.Group != group {
						for j := range changes {
							if changes[j].Group == change.Group {
								changes[j].Group = group
							}
						}
					}
					changes[i].Group, change.Group = group, group
				}
			}
			for name := range removed {
				if otherAdded[name] && !otherRemoved[name] && !added[name] {
					group := changes[i].Group
					if group == "" {
						group = changes[i].ID
					}
					if change.Group != "" && change.Group != group {
						for j := range changes {
							if changes[j].Group == change.Group {
								changes[j].Group = group
							}
						}
					}
					changes[i].Group, change.Group = group, group
				}
			}
		}
		changes = append(changes, change)
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].Offset < changes[j].Offset })
	if len(changes) == 0 {
		changes = nil
	}
	if err := validateTrackedChanges(body, changes); err != nil {
		return err
	}
	d.Body, d.TrackedChanges = body, changes
	return nil
}

// ResolveTrackedChange accepts or rejects a single change (and its move group).
// An empty id resolves all pending changes as one transaction.
func ResolveTrackedChange(d *Document, id string, accept bool) error {
	if err := validateTrackedChanges(d.Body, d.TrackedChanges); err != nil {
		return err
	}
	selected := make([]bool, len(d.TrackedChanges))
	group := ""
	if id != "" {
		found := false
		for _, change := range d.TrackedChanges {
			if change.ID == id {
				found, group = true, change.Group
				break
			}
		}
		if !found {
			return ErrChangeNotFound
		}
	}
	for i, change := range d.TrackedChanges {
		selected[i] = id == "" || change.ID == id || group != "" && change.Group == group
	}
	body := d.Body
	changes := append([]TrackedChange(nil), d.TrackedChanges...)
	if !accept {
		for i := len(changes) - 1; i >= 0; i-- {
			if !selected[i] {
				continue
			}
			change := changes[i]
			end := change.Offset + len(change.Inserted)
			body = body[:change.Offset] + change.Deleted + body[end:]
			delta := len(change.Deleted) - len(change.Inserted)
			for j := range changes {
				if !selected[j] && changes[j].Offset > end {
					changes[j].Offset += delta
				}
			}
		}
	}
	remaining := changes[:0]
	for i, change := range changes {
		if !selected[i] {
			remaining = append(remaining, change)
		}
	}
	if len(remaining) == 0 {
		remaining = nil
	}
	if err := validateTrackedChanges(body, remaining); err != nil {
		return err
	}
	d.Body, d.TrackedChanges = body, remaining
	return nil
}

// TrackingSummary returns an accessible, terminal-neutral review description.
func TrackingSummary(change TrackedChange) string {
	var parts []string
	if change.Deleted != "" {
		parts = append(parts, "--- Deleted ---\n"+change.Deleted)
	}
	if change.Inserted != "" {
		parts = append(parts, "+++ Inserted +++\n"+change.Inserted)
	}
	return strings.Join(parts, "\n\n")
}
