package filerepo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/ramjac/parchment/internal/artifact"
	"github.com/ramjac/parchment/internal/artifactfile"
	"github.com/ramjac/parchment/internal/document"
	"github.com/ramjac/parchment/internal/spreadsheet"
)

const documentDataBlock = "parchment-document"
const workbookDataBlock = "parchment-workbook"

type embeddedDocumentImage struct {
	Name string `json:"name"`
	Data []byte `json:"data"`
}

type documentFileData struct {
	Layout          document.Layout          `json:"layout"`
	TrackingVersion int                      `json:"tracking_version,omitempty"`
	Tracking        bool                     `json:"tracking,omitempty"`
	TrackedChanges  []document.TrackedChange `json:"tracked_changes,omitempty"`
	Images          []embeddedDocumentImage  `json:"images,omitempty"`
}

// GetDocument reads the document stored at path, including embedded images.
func (r *Repository) GetDocument(ctx context.Context, path string) (document.Document, error) {
	if err := ctx.Err(); err != nil {
		return document.Document{}, err
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return document.Document{}, err
	}
	if err := r.recoverHistory(ctx, path); err != nil {
		return document.Document{}, err
	}
	content, err := readContent(path, artifact.DocumentKind, document.ErrNotFound)
	if err != nil {
		return document.Document{}, err
	}
	return decodeDocument(path, content)
}

// TransitionDocument writes target only if the file still matches expected.
func (r *Repository) TransitionDocument(ctx context.Context, path string, expected, target *document.Document) error {
	if target == nil {
		return errors.New("Parchment does not delete artifact files")
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	return r.update(ctx, path, expected == nil, document.ErrNotFound, func(content []byte) ([]byte, error) {
		if expected != nil {
			if err := checkKind(path, content, artifact.DocumentKind); err != nil {
				return nil, err
			}
			current, err := decodeDocument(path, content)
			if err != nil {
				return nil, err
			}
			previous := *expected
			previous.Path = current.Path
			if !document.Equal(current, previous) || target.ID != current.ID {
				return nil, changedError(current.Path)
			}
		}
		return encodeDocument(*target)
	})
}

func encodeDocument(d document.Document) ([]byte, error) {
	if err := validateTarget(d.Artifact, artifact.DocumentKind); err != nil {
		return nil, err
	}
	if err := d.Layout.Validate(); err != nil {
		return nil, err
	}
	if err := document.ValidateTracking(d); err != nil {
		return nil, err
	}
	workbook := d.Workbook.Clone()
	if err := spreadsheet.NormalizeEmbeddedWorkbook(&workbook); err != nil {
		return nil, fmt.Errorf("validate embedded workbook: %w", err)
	}
	payload := documentFileData{Layout: d.Layout, Tracking: d.Tracking, TrackedChanges: d.TrackedChanges}
	if d.Tracking || len(d.TrackedChanges) > 0 {
		payload.TrackingVersion = 1
	}
	seenImages := map[string]bool{}
	for _, img := range d.Images {
		if err := img.Validate(); err != nil {
			return nil, err
		}
		if seenImages[img.Name] {
			return nil, fmt.Errorf("duplicate document image %s", img.Name)
		}
		seenImages[img.Name] = true
		payload.Images = append(payload.Images, embeddedDocumentImage{Name: img.Name, Data: img.Data})
	}
	blocks := encodeBlocks(d.Blocks)
	blocks[documentDataBlock] = payload
	if len(workbook.Sheets) > 0 {
		encoded, err := spreadsheet.EncodeEmbeddedWorkbook(workbook)
		if err != nil {
			return nil, err
		}
		blocks[workbookDataBlock] = json.RawMessage(encoded)
	}
	data, err := artifactfile.Encode(d.Artifact, d.Body, blocks)
	if err != nil {
		return nil, fmt.Errorf("encode document: %w", err)
	}
	return data, nil
}

func decodeDocument(path string, content []byte) (document.Document, error) {
	file, err := decodeArtifact(path, content)
	if err != nil {
		return document.Document{}, err
	}
	payloadJSON, ok := file.Blocks[documentDataBlock]
	if !ok {
		return document.Document{}, fmt.Errorf("%s: document data block is missing", path)
	}
	var payload documentFileData
	if err := json.Unmarshal(payloadJSON, &payload); err != nil {
		return document.Document{}, fmt.Errorf("decode document data %s: %w", path, err)
	}
	if payload.TrackingVersion < 0 || payload.TrackingVersion > 1 ||
		(payload.TrackingVersion != 1 && (payload.Tracking || len(payload.TrackedChanges) > 0)) {
		return document.Document{}, fmt.Errorf("unsupported document tracking version %d", payload.TrackingVersion)
	}
	if payload.Layout == (document.Layout{}) {
		payload.Layout = document.DefaultLayout()
	}
	if err := payload.Layout.Validate(); err != nil {
		return document.Document{}, fmt.Errorf("validate document layout %s: %w", path, err)
	}
	d := document.Document{
		Artifact: file.Artifact, Body: file.Body, Layout: payload.Layout,
		Tracking: payload.Tracking, TrackedChanges: payload.TrackedChanges,
		Blocks: copyPayloadBlocks(file.Blocks, documentDataBlock, workbookDataBlock),
	}
	if data, ok := file.Blocks[workbookDataBlock]; ok {
		d.Workbook, err = spreadsheet.DecodeEmbeddedWorkbook(data)
		if err != nil {
			return document.Document{}, fmt.Errorf("decode embedded workbook %s: %w", path, err)
		}
	}
	if err := document.ValidateTracking(d); err != nil {
		return document.Document{}, fmt.Errorf("validate document tracking %s: %w", path, err)
	}
	seenImages := map[string]bool{}
	for _, imageData := range payload.Images {
		img := artifact.Image{Name: imageData.Name, Data: imageData.Data}
		if err := img.Validate(); err != nil {
			return document.Document{}, fmt.Errorf("validate document image %s: %w", img.Name, err)
		}
		if seenImages[img.Name] {
			return document.Document{}, fmt.Errorf("duplicate document image %s", img.Name)
		}
		seenImages[img.Name] = true
		d.Images = append(d.Images, img)
	}
	return d, nil
}
