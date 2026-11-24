package filerepo_test

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/png"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/ramjac/parchment/internal/filerepo"
	"github.com/ramjac/parchment/internal/imageedit"
)

func TestImageRebasedSavesAndHistory(t *testing.T) {
	repo, _, dir := newRepo(t)
	ctx := context.Background()
	source := filepath.Join(dir, "source.bin")
	var data bytes.Buffer
	if err := png.Encode(&data, image.NewNRGBA(image.Rect(0, 0, 6, 4))); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, data.Bytes(), 0o640); err != nil {
		t.Fatal(err)
	}
	service := imageedit.NewService(repo)
	img, err := service.Load(ctx, source)
	if err != nil {
		t.Fatal(err)
	}
	s := imageedit.NewSession(img, 10)
	save := func(path string) {
		t.Helper()
		target, err := service.PrepareSave(ctx, s.SavingImage(), path, path == "", false)
		if err != nil {
			t.Fatal(err)
		}
		saved, err := service.SaveRebased(ctx, s.SavingImage(), target)
		if err != nil {
			t.Fatal(err)
		}
		s.Saved(saved)
		if s.Dirty() {
			t.Fatal("successful save left dirty revision")
		}
	}
	cropped, _ := img.Crop(1, 0, 4, 4)
	s.Apply(cropped)
	save("")
	_ = s.Undo()
	save("")
	resized, _ := s.Image().Resize(3, 2)
	s.Apply(resized)
	dest := filepath.Join(dir, "copy.jpg")
	save(dest)
	_ = s.Undo()
	save("")
	reloaded, err := service.Load(ctx, dest)
	if err != nil || reloaded.Format() != "jpeg" || reloaded.Bounds() != img.Bounds() {
		t.Fatal("save-as did not switch target or retain undo", reloaded, err)
	}
	if current, err := service.Load(ctx, source); err != nil || current.Bounds() != img.Bounds() || current.Format() != "png" {
		t.Fatal("saving the copy changed original", current, err)
	}
	target, err := service.PrepareSave(ctx, s.SavingImage(), "", true, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dest, []byte("external edit"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := service.SaveRebased(ctx, s.SavingImage(), target); err == nil {
		t.Fatal("rebase disabled concurrent edit protection")
	}
	if got, _ := os.ReadFile(dest); string(got) != "external edit" {
		t.Fatal("conflict overwrote external edit")
	}
}

func TestImageFormatProbe(t *testing.T) {
	_, _, dir := newRepo(t)
	path := filepath.Join(dir, "huge.bin")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write([]byte("\x89PNG\r\n\x1a\n")); err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(imageedit.MaxInputBytes + 1); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if format, err := filerepo.ImageFormat(path); err != nil || format != "png" {
		t.Fatal("signature probe applied full-read limits", format, err)
	}
}

func TestImageWrites(t *testing.T) {
	repo, _, dir := newRepo(t)
	ctx := context.Background()
	source := filepath.Join(dir, "source.png")
	output := filepath.Join(dir, "output.png")
	if err := os.WriteFile(source, []byte("source"), 0o640); err != nil {
		t.Fatal(err)
	}
	data, snapshot, err := repo.ReadImage(ctx, source)
	if err != nil || string(data) != "source" {
		t.Fatalf("read: %q, %v", data, err)
	}
	target, err := repo.PrepareImageWrite(ctx, snapshot, output, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.WriteImage(ctx, snapshot, target, []byte("created")); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(output); err != nil || (runtime.GOOS != "windows" && info.Mode().Perm() != 0o600) {
		t.Fatalf("new permissions: %v, %v", info, err)
	}
	if _, err := repo.PrepareImageWrite(ctx, snapshot, output, false, false); !errors.Is(err, fs.ErrExist) {
		t.Fatalf("no-clobber: %v", err)
	}
	if err := os.Chmod(output, 0o640); err != nil {
		t.Fatal(err)
	}
	target, err = repo.PrepareImageWrite(ctx, snapshot, output, false, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.WriteImage(ctx, snapshot, target, []byte("replaced")); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(output); err != nil || (runtime.GOOS != "windows" && info.Mode().Perm() != 0o640) {
		t.Fatalf("replacement permissions: %v, %v", info, err)
	}
	for _, force := range []bool{false, true} {
		if _, err := repo.PrepareImageWrite(ctx, snapshot, source, false, force); err == nil {
			t.Fatal("allowed source overwrite without in-place")
		}
	}
	target, err = repo.PrepareImageWrite(ctx, snapshot, "", true, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.WriteImage(ctx, snapshot, target, []byte("in-place")); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(source); string(got) != "in-place" {
		t.Fatalf("in-place contents: %q", got)
	}
	if info, err := os.Stat(source); err != nil || (runtime.GOOS != "windows" && info.Mode().Perm() != 0o640) {
		t.Fatalf("in-place permissions: %v, %v", info, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".parchment-") {
			t.Fatalf("leaked temporary file %s", entry.Name())
		}
	}
}

func TestImageSaveConflicts(t *testing.T) {
	for _, kind := range []string{"source-content", "source-replaced", "destination-content", "destination-created", "canceled", "different-source"} {
		t.Run(kind, func(t *testing.T) {
			repo, _, dir := newRepo(t)
			ctx := context.Background()
			source, output := filepath.Join(dir, "source"), filepath.Join(dir, "output")
			if err := os.WriteFile(source, []byte("source"), 0o600); err != nil {
				t.Fatal(err)
			}
			if kind == "destination-content" {
				if err := os.WriteFile(output, []byte("old"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			_, snapshot, err := repo.ReadImage(ctx, source)
			if err != nil {
				t.Fatal(err)
			}
			target, err := repo.PrepareImageWrite(ctx, snapshot, output, false, true)
			if err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "source-content":
				err = os.WriteFile(source, []byte("external edit"), 0o600)
			case "source-replaced":
				replacement := filepath.Join(dir, "replacement")
				if err = os.WriteFile(replacement, []byte("source"), 0o600); err == nil {
					err = os.Rename(replacement, source)
				}
			case "destination-content", "destination-created":
				err = os.WriteFile(output, []byte("external edit"), 0o600)
			case "canceled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "different-source":
				_, snapshot, err = repo.ReadImage(ctx, source)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := repo.WriteImage(ctx, snapshot, target, []byte("bad")); err == nil {
				t.Fatal("save ignored conflict or cancellation")
			}
			if got, _ := os.ReadFile(output); string(got) == "bad" {
				t.Fatal("failed save changed the destination")
			}
		})
	}
}

func TestImageSymlinksAndAliases(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation may require privileges")
	}
	repo, _, dir := newRepo(t)
	ctx := context.Background()
	source := filepath.Join(dir, "source")
	other := filepath.Join(dir, "other")
	if err := os.WriteFile(source, []byte("source"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(other, []byte("other"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, snapshot, err := repo.ReadImage(ctx, source)
	if err != nil {
		t.Fatal(err)
	}
	for _, hard := range []bool{false, true} {
		path := filepath.Join(dir, "alias")
		if hard {
			err = os.Link(source, path)
		} else {
			err = os.Symlink(source, path)
		}
		if err != nil {
			t.Fatal(err)
		}
		if _, err := repo.PrepareImageWrite(ctx, snapshot, path, false, true); err == nil {
			t.Fatal("allowed a source alias")
		}
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(other, link); err != nil {
		t.Fatal(err)
	}
	target, err := repo.PrepareImageWrite(ctx, snapshot, link, false, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.WriteImage(ctx, snapshot, target, []byte("updated")); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Lstat(link); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("overwrote symlink: %v, %v", info, err)
	}
	if got, _ := os.ReadFile(other); string(got) != "updated" {
		t.Fatalf("symlink target: %q", got)
	}
	_, linkedSource, err := repo.ReadImage(ctx, link)
	if err != nil {
		t.Fatal(err)
	}
	inPlace, err := repo.PrepareImageWrite(ctx, linkedSource, "", true, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.WriteImage(ctx, linkedSource, inPlace, []byte("in-place")); err != nil {
		t.Fatal(err)
	}
	_, linkedSource, err = repo.ReadImage(ctx, link)
	if err != nil {
		t.Fatal(err)
	}
	inPlace, err = repo.PrepareImageWrite(ctx, linkedSource, "", true, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(source, link); err != nil {
		t.Fatal(err)
	}
	if err := repo.WriteImage(ctx, linkedSource, inPlace, []byte("bad")); err == nil {
		t.Fatal("ignored retargeted source symlink")
	}
	output := filepath.Join(dir, "output-link")
	if err := os.Symlink(other, output); err != nil {
		t.Fatal(err)
	}
	target, err = repo.PrepareImageWrite(ctx, snapshot, output, false, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(output); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(source, output); err != nil {
		t.Fatal(err)
	}
	if err := repo.WriteImage(ctx, snapshot, target, []byte("bad")); err == nil {
		t.Fatal("ignored retargeted destination symlink")
	}
	dangling := filepath.Join(dir, "dangling")
	if err := os.Symlink(filepath.Join(dir, "missing"), dangling); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.PrepareImageWrite(ctx, snapshot, dangling, false, true); err == nil {
		t.Fatal("accepted dangling output symlink")
	}
}

func TestImageReadLimitsAndNonregularPaths(t *testing.T) {
	repo, _, dir := newRepo(t)
	ctx := context.Background()
	if _, _, err := repo.ReadImage(ctx, dir); err == nil {
		t.Fatal("read a directory as an image")
	}
	path := filepath.Join(dir, "large")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	err = file.Truncate(imageedit.MaxInputBytes + 1)
	closeErr := file.Close()
	if err != nil || closeErr != nil {
		t.Fatalf("create sparse input: %v, %v", err, closeErr)
	}
	if _, _, err := repo.ReadImage(ctx, path); err == nil || !strings.Contains(err.Error(), "128 MiB") {
		t.Fatalf("input size bound: %v", err)
	}
	source := filepath.Join(dir, "source")
	if err := os.WriteFile(source, []byte("source"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, snapshot, err := repo.ReadImage(ctx, source)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.PrepareImageWrite(ctx, snapshot, dir, false, true); err == nil {
		t.Fatal("accepted a directory destination")
	}
}

func TestImageBackup(t *testing.T) {
	_, state, dir := newRepo(t)
	backups := t.TempDir()
	repo, err := filerepo.New(state, filerepo.Options{LocalBackup: backups})
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(dir, "source.png")
	if err := os.WriteFile(source, []byte("source"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	_, snapshot, err := repo.ReadImage(ctx, source)
	if err != nil {
		t.Fatal(err)
	}
	target, err := repo.PrepareImageWrite(ctx, snapshot, "", true, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.WriteImage(ctx, snapshot, target, []byte("saved")); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(backups)
	if err != nil || len(entries) != 1 {
		t.Fatalf("backups: %v, %v", entries, err)
	}
	data, err := os.ReadFile(filepath.Join(backups, entries[0].Name()))
	if err != nil || string(data) != "saved" {
		t.Fatalf("backup contents: %q, %v", data, err)
	}
}
