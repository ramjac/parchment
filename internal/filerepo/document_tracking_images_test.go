package filerepo

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"example.com/parchment/internal/artifact"
	"example.com/parchment/internal/document"
)

func TestTrackedImageAdditionAndMove(t *testing.T) {
	ctx := context.Background()
	repo, err := New(t.TempDir(), Options{})
	if err != nil {
		t.Fatal(err)
	}

	service := document.NewService(repo, 20)
	path := filepath.Join(t.TempDir(), "image.md")
	if _, err := service.Create(ctx, path, document.Draft{Body: "one two"}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.SetTracking(ctx, path, true); err != nil {
		t.Fatal(err)
	}
	d, _, err := service.AddImage(ctx, path, "pic", tinyPNG)
	if err != nil || len(d.Images) != 1 || len(d.TrackedChanges) == 0 {
		t.Fatalf("tracked image insertion: %+v, %v", d, err)
	}
	d, err = service.ReviewTracked(ctx, d, "", false)
	if err != nil || d.Body != "one two" || len(d.Images) != 0 {
		t.Fatalf("rejected insertion: %+v, %v", d, err)
	}
	d, _, err = service.AddImage(ctx, path, "pic", tinyPNG)
	if err != nil {
		t.Fatal(err)
	}
	d, err = service.ReviewTracked(ctx, d, "", true)
	if err != nil {
		t.Fatal(err)
	}
	original := d.Body
	ref := artifact.ImageMarkdown("pic", d.Images[0].Name)
	d, err = service.Save(ctx, d, document.Draft{Body: strings.Replace(d.Body, ref, "", 1), Layout: d.Layout, Images: d.Images})
	if err != nil {
		t.Fatal(err)
	}
	d, err = service.Save(ctx, d, document.Draft{Body: ref + d.Body, Layout: d.Layout, Images: d.Images})
	if err != nil || len(d.TrackedChanges) != 2 || d.TrackedChanges[0].Group == "" {
		t.Fatalf("tracked move: %+v, %v", d.TrackedChanges, err)
	}
	d, err = service.ReviewTracked(ctx, d, d.TrackedChanges[0].ID, false)
	if err != nil || d.Body != original || len(d.Images) != 1 || len(d.TrackedChanges) != 0 {
		t.Fatalf("rejected move: %+v, %v", d, err)
	}
}

func TestTrackedImageMoveInOneSaveBothDirections(t *testing.T) {
	for _, forward := range []bool{false, true} {
		t.Run(map[bool]string{false: "backward", true: "forward"}[forward], func(t *testing.T) {
			ctx := context.Background()
			repo, err := New(t.TempDir(), Options{})
			if err != nil {
				t.Fatal(err)
			}
			service := document.NewService(repo, 20)
			path := filepath.Join(t.TempDir(), "image.md")
			if _, err := service.Create(ctx, path, document.Draft{Body: "left text and right text"}); err != nil {
				t.Fatal(err)
			}
			d, _, err := service.AddImage(ctx, path, "pic", tinyPNG)
			if err != nil {
				t.Fatal(err)
			}
			ref := artifact.ImageMarkdown("pic", d.Images[0].Name)
			start, end := ref+" left text and right text", "left text and right text "+ref
			if !forward {
				start, end = end, start
			}
			d, err = service.Save(ctx, d, document.Draft{Body: start, Layout: d.Layout, Images: d.Images})
			if err != nil {
				t.Fatal(err)
			}
			d, err = service.SetTracking(ctx, path, true)
			if err != nil {
				t.Fatal(err)
			}
			d, err = service.Save(ctx, d, document.Draft{Body: end, Layout: d.Layout, Images: d.Images})
			if err != nil || len(d.TrackedChanges) == 0 || len(d.Images) != 1 {
				t.Fatalf("tracked move: %+v, %v", d, err)
			}
			d, err = service.ReviewTracked(ctx, d, "", false)
			if err != nil || d.Body != start || len(d.Images) != 1 {
				t.Fatalf("reject move: %+v, %v", d, err)
			}
		})
	}
}
