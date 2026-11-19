package imageedit

import (
	"errors"
	"image"
)

const HistoryBytes = 256 << 20

type revision struct {
	image *Image
	id    uint64
}

// Session keeps pixel history independent from the current on-disk identity.
type Session struct {
	current     revision
	disk        *Image
	undo, redo  []revision
	saved, next uint64
	limit       int
	budget      int64
}

func NewSession(img *Image, limit int) *Session {
	return newSession(img, limit, HistoryBytes)
}

func newSession(img *Image, limit int, budget int64) *Session {
	if limit < 1 {
		limit = 100
	}
	return &Session{current: revision{img, 1}, disk: img, saved: 1, next: 1, limit: limit, budget: budget}
}

func (s *Session) Image() *Image { return s.current.image }
func (s *Session) Dirty() bool   { return s.current.id != s.saved }
func (s *Session) CanUndo() bool { return len(s.undo) > 0 }
func (s *Session) CanRedo() bool { return len(s.redo) > 0 }

// SavingImage binds any historical pixels to the latest file snapshot/format.
func (s *Session) SavingImage() *Image {
	img := *s.disk
	img.pixels = s.current.image.pixels
	return &img
}

func (s *Session) Saved(img *Image) {
	s.disk = img
	// Only the disk metadata is retained separately, not an extra old pixel buffer.
	s.current.image = img
	s.saved = s.current.id
}

// Apply records a successful immutable edit. False means it was a no-op.
func (s *Session) Apply(img *Image) bool {
	if img == s.current.image {
		return false
	}
	s.undo = append(s.undo, s.current)
	s.redo = nil
	s.next++
	s.current = revision{img, s.next}
	// The disk handle needs the source identity, not saved pixels.
	disk := *s.disk
	disk.pixels = img.pixels
	s.disk = &disk
	for len(s.undo) > s.limit || s.retainedBytes() > s.budget {
		if len(s.undo) == 0 {
			break
		}
		s.undo[0] = revision{}
		s.undo = s.undo[1:]
	}
	return true
}

func (s *Session) Undo() error {
	if len(s.undo) == 0 {
		return errors.New("nothing to undo")
	}
	s.redo = append(s.redo, s.current)
	s.current = popRevision(&s.undo)
	s.rebindDiskPixels()
	s.trimRedo()
	return nil
}

func (s *Session) Redo() error {
	if len(s.redo) == 0 {
		return errors.New("nothing to redo")
	}
	s.undo = append(s.undo, s.current)
	s.current = popRevision(&s.redo)
	s.rebindDiskPixels()
	for s.retainedBytes() > s.budget && len(s.undo) > 0 {
		s.undo[0] = revision{}
		s.undo = s.undo[1:]
	}
	return nil
}

func popRevision(items *[]revision) revision {
	n := len(*items) - 1
	result := (*items)[n]
	(*items)[n] = revision{}
	*items = (*items)[:n]
	return result
}

func (s *Session) rebindDiskPixels() {
	disk := *s.disk
	disk.pixels = s.current.image.pixels
	s.disk = &disk
}

func (s *Session) trimRedo() {
	for s.retainedBytes() > s.budget {
		if len(s.undo) > 0 {
			s.undo[0] = revision{}
			s.undo = s.undo[1:]
		} else if len(s.redo) > 0 {
			s.redo[0] = revision{}
			s.redo = s.redo[1:]
		} else {
			break
		}
	}
}

func (s *Session) retainedBytes() int64 {
	seen := map[image.Image]bool{s.current.image.pixels: true}
	var size int64
	for _, list := range [][]revision{s.undo, s.redo} {
		for _, rev := range list {
			if !seen[rev.image.pixels] {
				seen[rev.image.pixels] = true
				b := rev.image.Bounds()
				// PNG can decode into 16-bit RGBA: budget conservatively.
				size += int64(b.Dx()) * int64(b.Dy()) * 8
			}
		}
	}
	return size
}
