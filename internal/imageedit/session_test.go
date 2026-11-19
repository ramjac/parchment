package imageedit

import (
	"context"
	"image"
	"testing"
)

func TestSessionHistoryAndSavedRevision(t *testing.T) {
	img, err := Decode(pngBytes(t, sampleImage()))
	if err != nil {
		t.Fatal(err)
	}
	s := NewSession(img, 10)
	noOp, _ := img.Crop(0, 0, 3, 2)
	if s.Apply(noOp) || s.Dirty() || s.CanUndo() {
		t.Fatal("no-op crop changed history")
	}
	noOp, _ = img.Resize(3, 0)
	if s.Apply(noOp) {
		t.Fatal("no-op resize changed history")
	}
	cropped, _ := img.Crop(1, 0, 2, 2)
	s.Apply(cropped)
	saved := *s.SavingImage()
	saved.path, saved.format, saved.source = "new.jpg", "jpeg", "new snapshot"
	s.Saved(&saved)
	if s.Dirty() || !s.CanUndo() {
		t.Fatal("save cleared history or left current revision dirty")
	}
	if err := s.Undo(); err != nil || !s.Dirty() || s.Image().Bounds() != img.Bounds() {
		t.Fatal("undo across save failed", err)
	}
	bound := s.SavingImage()
	if bound.path != "new.jpg" || bound.format != "jpeg" || bound.source != "new snapshot" {
		t.Fatal("historical image lost the active file identity")
	}
	if err := s.Redo(); err != nil || s.Dirty() {
		t.Fatal("redo to saved revision must be clean", err)
	}
	_ = s.Undo()
	branch, _ := s.Image().Resize(1, 1)
	s.Apply(branch)
	if s.CanRedo() || !s.Dirty() {
		t.Fatal("new edit failed to clear redo")
	}
}

func TestSessionHistoryBounds(t *testing.T) {
	img, _ := Decode(pngBytes(t, sampleImage()))
	for _, tt := range []struct {
		name   string
		limit  int
		budget int64
	}{
		{"count", 1, HistoryBytes},
		{"memory", 10, 32},
		{"none-fit", 10, 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s := newSession(img, tt.limit, tt.budget)
			for _, size := range []int{2, 1, 3, 1} {
				next, _ := s.Image().Resize(size, size)
				s.Apply(next)
				if len(s.undo)+len(s.redo) > tt.limit || s.retainedBytes() > tt.budget {
					t.Fatal("edit exceeded history limits")
				}
			}
			for s.CanUndo() {
				if err := s.Undo(); err != nil {
					t.Fatal(err)
				}
				if s.retainedBytes() > tt.budget {
					t.Fatal("undo exceeded history budget")
				}
			}
			for s.CanRedo() {
				_ = s.Redo()
				if s.retainedBytes() > tt.budget {
					t.Fatal("redo exceeded history budget")
				}
			}
			if !s.Dirty() {
				t.Fatal("eviction falsely marked session clean")
			}
		})
	}
}

func TestServiceSaveRebased(t *testing.T) {
	repo := &fakeRepository{data: pngBytes(t, sampleImage())}
	service := NewService(repo)
	img, err := service.Load(context.Background(), "original.png")
	if err != nil {
		t.Fatal(err)
	}
	target, err := service.PrepareSave(context.Background(), img, "new.jpg", false, false)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := service.SaveRebased(context.Background(), img, target)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Path() != "new.jpg" || saved.Format() != "jpeg" || saved.FileSize() != int64(len(repo.saved)) ||
		saved.pixels != img.pixels || saved.source != "saved" {
		t.Fatal("save did not retain working pixels and refresh disk metadata")
	}
	if saved.Bounds() != image.Rect(0, 0, 3, 2) {
		t.Fatal("save changed geometry")
	}
}
