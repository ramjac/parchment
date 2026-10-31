package document

import (
	"context"
	"fmt"
	"testing"
)

type benchRepo struct {
	Repository
	changes []Change
}

func (r *benchRepo) ListDocumentChanges(ctx context.Context, path string) ([]Change, error) {
	return r.changes, nil
}

func (r *benchRepo) GetDocumentChange(ctx context.Context, path, changeID string) (Change, error) {
	// simulate a fast direct lookup, e.g., using a map or DB index
	return Change{ID: changeID}, nil
}

type benchRepoNoDirect struct {
	Repository
	changes []Change
}

func (r *benchRepoNoDirect) ListDocumentChanges(ctx context.Context, path string) ([]Change, error) {
	return r.changes, nil
}

func BenchmarkGetChange_Linear(b *testing.B) {
	changes := make([]Change, 10000)
	for i := 0; i < 10000; i++ {
		changes[i] = Change{ID: fmt.Sprintf("change-%d", i)}
	}
	repo := &benchRepoNoDirect{changes: changes}
	service := NewService(repo, 5)
	ctx := context.Background()
	targetID := "change-9999"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = service.getChange(ctx, "doc.md", targetID)
	}
}

func BenchmarkGetChange_Direct(b *testing.B) {
	changes := make([]Change, 10000)
	for i := 0; i < 10000; i++ {
		changes[i] = Change{ID: fmt.Sprintf("change-%d", i)}
	}
	repo := &benchRepo{changes: changes}
	service := NewService(repo, 5)
	ctx := context.Background()
	targetID := "change-9999"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = service.getChange(ctx, "doc.md", targetID)
	}
}

func BenchmarkWithImage(b *testing.B) {
	d := Document{
		Images: []Image{},
	}
	for i := 0; i < 100; i++ {
		d.Images = append(d.Images, Image{Name: "image" + string(rune(i))})
	}

	img := Image{Name: "image50", Data: []byte("newdata")}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = WithImage(d, img)
	}
}
