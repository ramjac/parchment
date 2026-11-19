package imageedit

import (
	"context"
	"errors"
)

// Repository keeps filesystem snapshots opaque to the processing service.
type Repository interface {
	ReadImage(context.Context, string) (data []byte, snapshot any, err error)
	PrepareImageWrite(ctx context.Context, source any, path string, inPlace, overwrite bool) (any, error)
	WriteImage(ctx context.Context, source, destination any, data []byte) error
	WriteImageSnapshot(ctx context.Context, source, destination any, data []byte) (any, error)
}

type Service struct {
	repo Repository
}

func NewService(repo Repository) *Service { return &Service{repo: repo} }

func (s *Service) Load(ctx context.Context, path string) (*Image, error) {
	data, snapshot, err := s.repo.ReadImage(ctx, path)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	img, err := Decode(data)
	if err != nil {
		return nil, err
	}
	img.source = snapshot
	img.path, img.bytes = path, int64(len(data))
	return img, nil
}

// SaveTarget captures the destination before potentially expensive processing.
type SaveTarget struct {
	format   string
	snapshot any
	path     string
}

func (t *SaveTarget) Path() string { return t.path }

func (s *Service) PrepareSave(ctx context.Context, img *Image, path string, inPlace, overwrite bool) (*SaveTarget, error) {
	if inPlace && (path != "" || overwrite) {
		return nil, errors.New("in-place saving cannot specify an output or force")
	}
	format := img.format
	if !inPlace {
		var err error
		format, err = outputFormat(path)
		if err != nil {
			return nil, err
		}
	}
	snapshot, err := s.repo.PrepareImageWrite(ctx, img.source, path, inPlace, overwrite)
	if err != nil {
		return nil, err
	}
	if inPlace {
		path = img.path
	}
	return &SaveTarget{format: format, snapshot: snapshot, path: path}, nil
}

func (s *Service) Save(ctx context.Context, img *Image, target *SaveTarget, quality int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	data, err := img.Encode(target.format, quality)
	if err != nil {
		return err
	}
	return s.repo.WriteImage(ctx, img.source, target.snapshot, data)
}

// SaveRebased retains working pixels while refreshing the committed file identity.
func (s *Service) SaveRebased(ctx context.Context, img *Image, target *SaveTarget) (*Image, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	data, err := img.Encode(target.format, DefaultQuality)
	if err != nil {
		return nil, err
	}
	snapshot, err := s.repo.WriteImageSnapshot(ctx, img.source, target.snapshot, data)
	if err != nil {
		return nil, err
	}
	saved := *img
	saved.source, saved.format = snapshot, target.format
	saved.path, saved.bytes = target.path, int64(len(data))
	return &saved, nil
}
