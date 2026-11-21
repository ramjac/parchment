package note

import (
	"context"
	"errors"

	"example.com/parchment/internal/history"
	"example.com/parchment/internal/notetext"
)

// TextSnapshot keeps an editable Unicode document separate from its saved bytes.
type TextSnapshot struct {
	Note     Note
	Plain    bool
	Raw      []byte
	Format   notetext.Format
	Document notetext.Document
}

func (s TextSnapshot) Clone() TextSnapshot {
	s.Note = *cloneNote(&s.Note)
	s.Raw = append([]byte(nil), s.Raw...)
	s.Document = s.Document.Clone()
	return s
}

type TextRepository interface {
	GetText(context.Context, string, string) (TextSnapshot, error)
	TransitionText(context.Context, string, TextSnapshot, TextSnapshot) (TextSnapshot, error)
}

func (s *Service) OpenText(ctx context.Context, path, encoding string) (TextSnapshot, error) {
	repo, ok := s.repository.(TextRepository)
	if !ok {
		return TextSnapshot{}, errors.New("note repository does not support text sessions")
	}
	s.activePath = path
	return repo.GetText(ctx, path, encoding)
}

func (s *Service) SaveTextExpected(ctx context.Context, expected TextSnapshot, doc notetext.Document) (TextSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return TextSnapshot{}, err
	}
	if !expected.Plain {
		n, err := s.UpdateExpected(ctx, expected.Note, doc.Text)
		if err != nil {
			return TextSnapshot{}, err
		}
		return TextSnapshot{Note: n, Format: expected.Format, Document: notetext.Document{Text: n.Body}}, nil
	}
	repo, ok := s.repository.(TextRepository)
	if !ok {
		return TextSnapshot{}, errors.New("note repository does not support text sessions")
	}
	if expected.Document.Equal(doc) {
		return repo.TransitionText(ctx, expected.Note.Path, expected, expected)
	}
	target := expected.Clone()
	target.Document = doc.Clone()
	target.Note.ModifiedAt = s.now().UTC()
	s.activePath = expected.Note.Path
	if s.store != nil {
		return repo.TransitionText(history.WithDescription(ctx, "Edit note"), expected.Note.Path, expected, target)
	}
	op := &textOperation{repository: repo, before: expected.Clone(), after: target}
	if err := s.history.Execute(ctx, op); err != nil {
		return TextSnapshot{}, err
	}
	return op.after.Clone(), nil
}

// RestoreTextSnapshot rebuilds a byte-safe recovery baseline without changing disk.
func RestoreTextSnapshot(n Note, raw []byte, format notetext.Format) (TextSnapshot, error) {
	doc, actual, err := notetext.Decode(raw, format.Encoding)
	if err != nil {
		return TextSnapshot{}, err
	}
	if actual != format {
		return TextSnapshot{}, errors.New("recovery encoding does not match its saved bytes")
	}
	n.Body = string(raw)
	return TextSnapshot{Note: n, Plain: true, Raw: append([]byte(nil), raw...), Format: format, Document: doc}, nil
}

type textOperation struct {
	repository TextRepository
	before     TextSnapshot
	after      TextSnapshot
}

func (o *textOperation) Apply(ctx context.Context) error {
	result, err := o.repository.TransitionText(ctx, o.before.Note.Path, o.before, o.after)
	if err == nil {
		o.after = result.Clone()
	}
	return err
}

func (o *textOperation) Undo(ctx context.Context) error {
	_, err := o.repository.TransitionText(ctx, o.before.Note.Path, o.after, o.before)
	return err
}

func (o *textOperation) Description() string { return "Edit note" }
