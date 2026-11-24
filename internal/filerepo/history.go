package filerepo

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/ramjac/parchment/internal/artifact"
	"github.com/ramjac/parchment/internal/artifactfile"
	"github.com/ramjac/parchment/internal/document"
	"github.com/ramjac/parchment/internal/history"
)

const historySchemaVersion = 1
const pendingHistoryManifest = "index.pending.json"

var _ history.Store = (*Repository)(nil)

func (r *Repository) historyDir() string {
	return filepath.Join(r.stateDir, "history")
}

func (r *Repository) historyPaths(path string) (string, string, error) {
	canonical, err := resolve(path)
	if err != nil {
		return "", "", err
	}
	sum := sha256.Sum256([]byte(canonical))
	return canonical, filepath.Join(r.historyDir(), hex.EncodeToString(sum[:])), nil
}

func (r *Repository) recoverHistory(ctx context.Context, path string) error {
	return r.withLock(ctx, func() error {
		canonical, dir, err := r.historyPaths(path)
		if err != nil {
			return err
		}
		return r.recoverHistoryJournal(ctx, canonical, dir)
	})
}

func historyDigest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func (r *Repository) readHistoryManifest(dir, path string) (history.Manifest, error) {
	data, err := os.ReadFile(filepath.Join(dir, "index.json"))
	if errors.Is(err, fs.ErrNotExist) {
		return history.Manifest{SchemaVersion: historySchemaVersion, ArtifactPath: path, UndoLimit: r.historyLimit}, nil
	}
	if err != nil {
		return history.Manifest{}, fmt.Errorf("read history manifest: %w", err)
	}
	var manifest history.Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return history.Manifest{}, fmt.Errorf("decode history manifest: %w", err)
	}
	if err := validateHistoryManifest(manifest, path); err != nil {
		return history.Manifest{}, err
	}
	return manifest, nil
}

func validateHistoryManifest(manifest history.Manifest, path string) error {
	if manifest.SchemaVersion != historySchemaVersion || manifest.ArtifactPath != path ||
		manifest.HeadIndex < 0 || manifest.HeadIndex >= len(manifest.Entries) {
		return errors.New("invalid history manifest")
	}
	return nil
}

func encodeHistoryManifest(manifest history.Manifest) ([]byte, error) {
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode history manifest: %w", err)
	}
	return data, nil
}

func (r *Repository) writeHistoryManifest(ctx context.Context, dir string, manifest history.Manifest) error {
	data, err := encodeHistoryManifest(manifest)
	if err != nil {
		return err
	}
	if err := writeAtomic(ctx, filepath.Join(dir, "index.json"), data, 0o600); err != nil {
		return fmt.Errorf("write history manifest: %w", err)
	}
	return nil
}

func (r *Repository) writeHistoryBlob(ctx context.Context, data []byte) (string, error) {
	hash := historyDigest(data)
	dir := filepath.Join(r.historyDir(), "blobs")
	if err := ensureDirectory(dir); err != nil {
		return "", fmt.Errorf("create history blob directory: %w", err)
	}
	path := filepath.Join(dir, hash)
	err := createAtomic(ctx, path, data, 0o600)
	if errors.Is(err, fs.ErrExist) {
		existing, readErr := os.ReadFile(path)
		if readErr != nil {
			return "", fmt.Errorf("verify history blob: %w", readErr)
		}
		if historyDigest(existing) != hash {
			return "", errors.New("history blob digest mismatch")
		}
		return hash, nil
	}
	if err != nil {
		return "", fmt.Errorf("write history blob: %w", err)
	}
	return hash, nil
}

func (r *Repository) readHistoryBlob(hash string) ([]byte, error) {
	decodedHash, err := hex.DecodeString(hash)
	if err != nil || len(decodedHash) != sha256.Size {
		return nil, errors.New("invalid history blob hash")
	}
	data, err := os.ReadFile(filepath.Join(r.historyDir(), "blobs", hash))
	if err != nil {
		return nil, fmt.Errorf("read history blob: %w", err)
	}
	if historyDigest(data) != hash {
		return nil, errors.New("history blob digest mismatch")
	}
	return data, nil
}

func (r *Repository) snapshotHistory(ctx context.Context, data []byte) (history.Snapshot, error) {
	snapshot, supported, err := r.decomposeHistorySnapshot(ctx, data)
	if err != nil {
		return history.Snapshot{}, err
	}
	if supported {
		return snapshot, nil
	}
	hash, err := r.writeHistoryBlob(ctx, data)
	if err != nil {
		return history.Snapshot{}, err
	}
	return history.Snapshot{FileDigest: historyDigest(data), ByteSize: int64(len(data)), RawBlobHash: hash}, nil
}

type historyMetadata struct {
	ID            string        `json:"id"`
	Kind          artifact.Kind `json:"kind"`
	Title         string        `json:"title"`
	FormatVersion int           `json:"format_version"`
	CreatedAt     time.Time     `json:"created_at"`
	ModifiedAt    time.Time     `json:"modified_at"`
}

type historyEmbeddedImage struct {
	Name string `json:"name"`
	Data string `json:"data"`
}

type historyDocumentData struct {
	Layout          document.Layout          `json:"layout"`
	TrackingVersion int                      `json:"tracking_version,omitempty"`
	Tracking        bool                     `json:"tracking,omitempty"`
	TrackedChanges  []document.TrackedChange `json:"tracked_changes,omitempty"`
	Images          []historyEmbeddedImage   `json:"images,omitempty"`
}

type historyPresentationData struct {
	Version int                    `json:"version"`
	Font    string                 `json:"font,omitempty"`
	Images  []historyEmbeddedImage `json:"images,omitempty"`
}

func (r *Repository) decomposeHistorySnapshot(ctx context.Context, data []byte) (history.Snapshot, bool, error) {
	file, err := artifactfile.Decode(data)
	if err != nil {
		return history.Snapshot{}, false, nil
	}
	blocks := make(map[string]any, len(file.Blocks))
	for name, payload := range file.Blocks {
		blocks[name] = payload
	}
	canonical, err := artifactfile.Encode(file.Artifact, file.Body, blocks)
	if err != nil || !bytes.Equal(canonical, data) {
		return history.Snapshot{}, false, nil
	}
	meta, err := json.Marshal(historyMetadata{
		ID: file.Artifact.ID, Kind: file.Artifact.Kind, Title: file.Artifact.Title,
		FormatVersion: file.Artifact.FormatVersion,
		CreatedAt:     file.Artifact.CreatedAt.UTC(), ModifiedAt: file.Artifact.ModifiedAt.UTC(),
	})
	if err != nil {
		return history.Snapshot{}, false, err
	}
	metaHash, err := r.writeHistoryBlob(ctx, meta)
	if err != nil {
		return history.Snapshot{}, false, err
	}
	bodyHash, err := r.writeHistoryBlob(ctx, []byte(file.Body))
	if err != nil {
		return history.Snapshot{}, false, err
	}
	blockHashes := make(map[string]string, len(file.Blocks))
	imageHashes := make(map[string]string)
	for name, payload := range file.Blocks {
		transformed, supported, err := r.detachHistoryBlock(ctx, name, payload, imageHashes)
		if err != nil {
			return history.Snapshot{}, false, err
		}
		if !supported {
			return history.Snapshot{}, false, nil
		}
		hash, err := r.writeHistoryBlob(ctx, transformed)
		if err != nil {
			return history.Snapshot{}, false, err
		}
		blockHashes[name] = hash
	}
	snapshot := history.Snapshot{
		FileDigest: historyDigest(data), ByteSize: int64(len(data)),
		MetadataBlobHash: metaHash, BodyBlobHash: bodyHash,
		BlockBlobHashes: blockHashes, ImageBlobHashes: imageHashes,
	}
	reconstructed, err := r.reconstructHistorySnapshot(ctx, snapshot)
	if err != nil || !bytes.Equal(reconstructed, data) {
		return history.Snapshot{}, false, nil
	}
	return snapshot, true, nil
}

func (r *Repository) detachHistoryBlock(ctx context.Context, name string, payload []byte, images map[string]string) ([]byte, bool, error) {
	detach := func(items []historyEmbeddedImage) ([]historyEmbeddedImage, error) {
		for i := range items {
			data, err := base64.StdEncoding.DecodeString(items[i].Data)
			if err != nil || len(data) == 0 {
				continue
			}
			hash, err := r.writeHistoryBlob(ctx, data)
			if err != nil {
				return nil, err
			}
			images[hash] = hash
			items[i].Data = historyImageMarker(hash)
		}
		return items, nil
	}
	switch name {
	case "parchment-document":
		var value historyDocumentData
		if err := json.Unmarshal(payload, &value); err != nil {
			return nil, false, nil
		}
		var err error
		value.Images, err = detach(value.Images)
		if err != nil {
			return nil, false, err
		}
		encoded, err := json.MarshalIndent(value, "", "  ")
		return encoded, err == nil, err
	case "parchment-presentation":
		var value historyPresentationData
		if err := json.Unmarshal(payload, &value); err != nil {
			return nil, false, nil
		}
		var err error
		value.Images, err = detach(value.Images)
		if err != nil {
			return nil, false, err
		}
		encoded, err := json.MarshalIndent(value, "", "  ")
		return encoded, err == nil, err
	default:
		return append([]byte(nil), payload...), true, nil
	}
}

func historyImageMarker(hash string) string { return "!parchment-history-image:" + hash + "!" }

func (r *Repository) restoreHistoryImages(items []historyEmbeddedImage, images map[string]string) ([]historyEmbeddedImage, error) {
	for i := range items {
		marker := items[i].Data
		if !strings.HasPrefix(marker, "!parchment-history-image:") || !strings.HasSuffix(marker, "!") {
			continue
		}
		hash := strings.TrimSuffix(strings.TrimPrefix(marker, "!parchment-history-image:"), "!")
		chunk, ok := images[hash]
		if !ok {
			return nil, errors.New("missing embedded image history chunk")
		}
		data, err := r.readHistoryBlob(chunk)
		if err != nil {
			return nil, err
		}
		items[i].Data = base64.StdEncoding.EncodeToString(data)
	}
	return items, nil
}

func nextHistoryID(manifest *history.Manifest) string {
	if manifest.NextEntryID == 0 {
		for _, entry := range manifest.Entries {
			parsed, err := strconv.ParseUint(entry.ID, 10, 64)
			if err == nil && parsed >= manifest.NextEntryID {
				manifest.NextEntryID = parsed + 1
			}
		}
		if manifest.NextEntryID == 0 {
			manifest.NextEntryID = 1
		}
	}
	id := strconv.FormatUint(manifest.NextEntryID, 10)
	manifest.NextEntryID++
	return id
}

func (r *Repository) prepareHistoryUnlocked(ctx context.Context, path string, before, after []byte, description string, create bool) (history.Manifest, string, bool, error) {
	canonical, dir, err := r.historyPaths(path)
	if err != nil {
		return history.Manifest{}, "", false, err
	}
	if err := ensureDirectory(dir); err != nil {
		return history.Manifest{}, "", false, fmt.Errorf("create per-file history directory: %w", err)
	}
	manifest, err := r.readHistoryManifest(dir, canonical)
	if err != nil {
		return history.Manifest{}, "", false, err
	}
	limitChanged := manifest.UndoLimit != r.historyLimit
	manifest.UndoLimit = r.historyLimit
	if create {
		snapshot, err := r.snapshotHistory(ctx, after)
		if err != nil {
			return history.Manifest{}, "", false, err
		}
		manifest.Entries = []history.Entry{{ID: nextHistoryID(&manifest), Timestamp: time.Now().UTC(), Description: "Initial state", Snapshot: snapshot}}
		manifest.HeadIndex = 0
		return manifest, dir, true, nil
	}
	if len(manifest.Entries) > 0 && manifest.HeadIndex < len(manifest.Entries)-1 {
		manifest.Entries = manifest.Entries[:manifest.HeadIndex+1]
	}
	if len(manifest.Entries) > 0 && manifest.Entries[manifest.HeadIndex].Snapshot.FileDigest != historyDigest(before) {
		snapshot, err := r.snapshotHistory(ctx, before)
		if err != nil {
			return history.Manifest{}, "", false, err
		}
		manifest.Entries = append(manifest.Entries[:manifest.HeadIndex+1], history.Entry{
			ID: nextHistoryID(&manifest), Timestamp: time.Now().UTC(),
			Description: "External edit",
			Snapshot:    snapshot,
		})
		manifest.HeadIndex++
	}
	if len(manifest.Entries) == 0 {
		snapshot, err := r.snapshotHistory(ctx, before)
		if err != nil {
			return history.Manifest{}, "", false, err
		}
		manifest.Entries = append(manifest.Entries, history.Entry{
			ID: nextHistoryID(&manifest), Timestamp: time.Now().UTC(),
			Description: "Initial state", Snapshot: snapshot,
		})
		manifest.HeadIndex = 0
	}
	if manifest.Entries[manifest.HeadIndex].Snapshot.FileDigest == historyDigest(after) {
		if !limitChanged {
			return manifest, dir, false, nil
		}
		if len(manifest.Entries) > manifest.UndoLimit+1 {
			drop := len(manifest.Entries) - (manifest.UndoLimit + 1)
			manifest.Entries = append([]history.Entry(nil), manifest.Entries[drop:]...)
			manifest.HeadIndex -= drop
		}
		return manifest, dir, true, nil
	}
	snapshot, err := r.snapshotHistory(ctx, after)
	if err != nil {
		return history.Manifest{}, "", false, err
	}
	if description == "" {
		description = "Update artifact"
	}
	manifest.Entries = append(manifest.Entries, history.Entry{
		ID: nextHistoryID(&manifest), Timestamp: time.Now().UTC(),
		Description: description, Snapshot: snapshot,
	})
	manifest.HeadIndex = len(manifest.Entries) - 1
	if len(manifest.Entries) > manifest.UndoLimit+1 {
		drop := len(manifest.Entries) - (manifest.UndoLimit + 1)
		manifest.Entries = append([]history.Entry(nil), manifest.Entries[drop:]...)
		manifest.HeadIndex -= drop
	}
	return manifest, dir, true, nil
}

func (r *Repository) commitHistoryTransition(ctx context.Context, dir string, from, to []byte, manifest history.Manifest, writeArtifact func() error) error {
	pendingBytes, err := encodeHistoryManifest(manifest)
	if err != nil {
		return err
	}
	pendingPath := filepath.Join(dir, pendingHistoryManifest)
	if err := writeAtomic(ctx, pendingPath, pendingBytes, 0o600); err != nil {
		return fmt.Errorf("prepare history manifest: %w", err)
	}
	journal := history.Journal{
		TransitionID: strconv.FormatInt(time.Now().UTC().UnixNano(), 10),
		FromDigest:   historyDigest(from), ToDigest: historyDigest(to),
		TargetHead: manifest.HeadIndex, ManifestDigest: historyDigest(pendingBytes),
	}
	journalBytes, err := json.Marshal(journal)
	if err != nil {
		_ = os.Remove(pendingPath)
		return err
	}
	journalPath := filepath.Join(dir, "journal.json")
	if err := writeAtomic(ctx, journalPath, journalBytes, 0o600); err != nil {
		return fmt.Errorf("prepare history transition: %w", err)
	}
	if err := writeArtifact(); err != nil {
		return err
	}
	if err := r.writeHistoryManifest(ctx, dir, manifest); err != nil {
		return err
	}
	r.finishHistoryTransition(ctx, dir)
	return nil
}

func (r *Repository) finishHistoryTransition(ctx context.Context, dir string) {
	journalPath := filepath.Join(dir, "journal.json")
	if err := os.Remove(journalPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
		r.addWarning(fmt.Errorf("finalize history transition: %w", err))
		return
	}
	pendingPath := filepath.Join(dir, pendingHistoryManifest)
	if err := os.Remove(pendingPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
		r.addWarning(fmt.Errorf("remove committed history manifest: %w", err))
	}
	if err := r.gcUnlocked(ctx); err != nil {
		r.addWarning(fmt.Errorf("history garbage collection failed: %w", err))
	}
}

func boundRestoreManifest(manifest *history.Manifest, targetHead, checkpointHead, limit int) (int, int) {
	manifest.UndoLimit = limit
	maxEntries := limit + 1
	if maxEntries < 2 && checkpointHead >= 0 {
		maxEntries = 2
	}
	if len(manifest.Entries) <= maxEntries {
		return targetHead, checkpointHead
	}
	keep := make([]bool, len(manifest.Entries))
	keep[targetHead] = true
	remaining := maxEntries - 1
	if checkpointHead >= 0 {
		keep[checkpointHead] = true
		remaining--
	}
	for i := len(manifest.Entries) - 1; i >= 0 && remaining > 0; i-- {
		if !keep[i] {
			keep[i] = true
			remaining--
		}
	}
	entries := make([]history.Entry, 0, maxEntries)
	newTarget, newCheckpoint := -1, -1
	for i, entry := range manifest.Entries {
		if !keep[i] {
			continue
		}
		if i == targetHead {
			newTarget = len(entries)
		}
		if i == checkpointHead {
			newCheckpoint = len(entries)
		}
		entries = append(entries, entry)
	}
	manifest.Entries = entries
	return newTarget, newCheckpoint
}

func queryHistory[T any](r *Repository, ctx context.Context, path string, query func(history.Manifest) (T, error)) (result T, resultErr error) {
	resultErr = r.withLock(ctx, func() error {
		canonical, dir, err := r.historyPaths(path)
		if err != nil {
			return err
		}
		if err := r.recoverHistoryJournal(ctx, canonical, dir); err != nil {
			return err
		}
		manifest, err := r.readHistoryManifest(dir, canonical)
		if err != nil {
			return err
		}
		result, err = query(manifest)
		return err
	})
	return result, resultErr
}

func (r *Repository) CanUndo(ctx context.Context, path string) (bool, error) {
	return queryHistory(r, ctx, path, func(manifest history.Manifest) (bool, error) {
		return len(manifest.Entries) > 0 && manifest.HeadIndex > 0, nil
	})
}

func (r *Repository) CanRedo(ctx context.Context, path string) (bool, error) {
	return queryHistory(r, ctx, path, func(manifest history.Manifest) (bool, error) {
		return len(manifest.Entries) > 0 && manifest.HeadIndex < len(manifest.Entries)-1, nil
	})
}

func (r *Repository) Log(ctx context.Context, path string) ([]history.Entry, int, error) {
	manifest, err := queryHistory(r, ctx, path, func(manifest history.Manifest) (history.Manifest, error) {
		return manifest, nil
	})
	if err != nil {
		return nil, 0, err
	}
	return manifest.Entries, manifest.HeadIndex, nil
}

func (r *Repository) Undo(ctx context.Context, path string) (string, error) {
	return r.moveHistoryHead(ctx, path, -1)
}

func (r *Repository) Redo(ctx context.Context, path string) (string, error) {
	return r.moveHistoryHead(ctx, path, 1)
}

func (r *Repository) moveHistoryHead(ctx context.Context, path string, delta int) (description string, resultErr error) {
	err := r.withLock(ctx, func() error {
		canonical, dir, err := r.historyPaths(path)
		if err != nil {
			return err
		}
		if err := r.recoverHistoryJournal(ctx, canonical, dir); err != nil {
			return err
		}
		manifest, err := r.readHistoryManifest(dir, canonical)
		if err != nil {
			return err
		}
		targetHead := manifest.HeadIndex + delta
		if targetHead < 0 || targetHead >= len(manifest.Entries) {
			if delta < 0 {
				return errors.New("nothing to undo")
			}
			return errors.New("nothing to redo")
		}
		current, err := readRegularFile(canonical, 64<<20)
		if err != nil {
			return err
		}
		if historyDigest(current) != manifest.Entries[manifest.HeadIndex].Snapshot.FileDigest {
			return errors.New("File modified externally; persistent history preserved for recovery.")
		}
		currentDescription := manifest.Entries[manifest.HeadIndex].Description
		targetEntry := manifest.Entries[targetHead]
		target, err := r.reconstructHistorySnapshot(ctx, targetEntry.Snapshot)
		if err != nil {
			return err
		}
		info, err := os.Stat(canonical)
		if err != nil {
			return err
		}
		manifest.HeadIndex = targetHead
		if err := r.commitHistoryTransition(ctx, dir, current, target, manifest, func() error {
			if err := writeAtomic(ctx, canonical, target, info.Mode().Perm()); err != nil {
				return fmt.Errorf("apply history transition: %w", err)
			}
			return nil
		}); err != nil {
			return err
		}
		if delta < 0 {
			description = currentDescription
		} else {
			description = targetEntry.Description
		}
		return nil
	})
	return description, err
}

func (r *Repository) Restore(ctx context.Context, path, stepID string) (string, error) {
	var description string
	err := r.withLock(ctx, func() error {
		canonical, dir, err := r.historyPaths(path)
		if err != nil {
			return err
		}
		if err := r.recoverHistoryJournal(ctx, canonical, dir); err != nil {
			return err
		}
		manifest, err := r.readHistoryManifest(dir, canonical)
		if err != nil {
			return err
		}
		targetHead := -1
		for i, entry := range manifest.Entries {
			if entry.ID == stepID {
				targetHead = i
				break
			}
		}
		if targetHead < 0 {
			return fmt.Errorf("history step %q not found", stepID)
		}
		targetEntry := manifest.Entries[targetHead]
		current, err := readRegularFile(canonical, 64<<20)
		if err != nil {
			return err
		}
		manifest.UndoLimit = r.historyLimit
		if historyDigest(current) != manifest.Entries[manifest.HeadIndex].Snapshot.FileDigest {
			checkpoint, err := r.snapshotHistory(ctx, current)
			if err != nil {
				return err
			}
			manifest.Entries = append(manifest.Entries[:manifest.HeadIndex+1], history.Entry{
				ID: nextHistoryID(&manifest), Timestamp: time.Now().UTC(),
				Description: "External edit", Snapshot: checkpoint,
			})
			checkpointHead := len(manifest.Entries) - 1
			targetHead = -1
			for i, entry := range manifest.Entries {
				if entry.ID == stepID {
					targetHead = i
					targetEntry = entry
					break
				}
			}
			if targetHead < 0 {
				targetEntry.ID = nextHistoryID(&manifest)
				targetEntry.Timestamp = time.Now().UTC()
				targetEntry.Description = "Restore " + stepID
				manifest.Entries = append(manifest.Entries, targetEntry)
				targetHead = len(manifest.Entries) - 1
			}
			targetHead, checkpointHead = boundRestoreManifest(&manifest, targetHead, checkpointHead, r.historyLimit)
			manifest.HeadIndex = checkpointHead
			if err := r.writeHistoryManifest(ctx, dir, manifest); err != nil {
				return err
			}
		} else {
			targetHead, _ = boundRestoreManifest(&manifest, targetHead, -1, r.historyLimit)
		}
		targetEntry = manifest.Entries[targetHead]
		if targetEntry.Snapshot.FileDigest == historyDigest(current) {
			manifest.HeadIndex = targetHead
			if err := r.writeHistoryManifest(ctx, dir, manifest); err != nil {
				return err
			}
			if err := r.gcUnlocked(ctx); err != nil {
				r.addWarning(fmt.Errorf("history garbage collection failed: %w", err))
			}
			description = targetEntry.Description
			return nil
		}
		content, err := r.reconstructHistorySnapshot(ctx, targetEntry.Snapshot)
		if err != nil {
			return err
		}
		info, err := os.Stat(canonical)
		if err != nil {
			return err
		}
		manifest.HeadIndex = targetHead
		if err := r.commitHistoryTransition(ctx, dir, current, content, manifest, func() error {
			if err := writeAtomic(ctx, canonical, content, info.Mode().Perm()); err != nil {
				return fmt.Errorf("apply history restore: %w", err)
			}
			return nil
		}); err != nil {
			return err
		}
		description = targetEntry.Description
		return nil
	})
	return description, err
}

func (r *Repository) reconstructHistorySnapshot(ctx context.Context, snapshot history.Snapshot) ([]byte, error) {
	var data []byte
	var err error
	if snapshot.RawBlobHash != "" {
		data, err = r.readHistoryBlob(snapshot.RawBlobHash)
	} else {
		data, err = r.reconstructChunkedHistorySnapshot(ctx, snapshot)
	}
	if err != nil {
		return nil, err
	}
	if int64(len(data)) != snapshot.ByteSize || historyDigest(data) != snapshot.FileDigest {
		return nil, errors.New("history snapshot digest mismatch")
	}
	return data, nil
}

func (r *Repository) reconstructChunkedHistorySnapshot(ctx context.Context, snapshot history.Snapshot) ([]byte, error) {
	metaBytes, err := r.readHistoryBlob(snapshot.MetadataBlobHash)
	if err != nil {
		return nil, err
	}
	var metadata historyMetadata
	if err := json.Unmarshal(metaBytes, &metadata); err != nil {
		return nil, fmt.Errorf("decode history metadata chunk: %w", err)
	}
	body, err := r.readHistoryBlob(snapshot.BodyBlobHash)
	if err != nil {
		return nil, err
	}
	blocks := make(map[string]any, len(snapshot.BlockBlobHashes))
	for name, hash := range snapshot.BlockBlobHashes {
		blockBytes, err := r.readHistoryBlob(hash)
		if err != nil {
			return nil, err
		}
		switch name {
		case "parchment-document":
			var value historyDocumentData
			if err := json.Unmarshal(blockBytes, &value); err != nil {
				return nil, err
			}
			value.Images, err = r.restoreHistoryImages(value.Images, snapshot.ImageBlobHashes)
			if err != nil {
				return nil, err
			}
			blockBytes, err = json.MarshalIndent(value, "", "  ")
		case "parchment-presentation":
			var value historyPresentationData
			if err := json.Unmarshal(blockBytes, &value); err != nil {
				return nil, err
			}
			value.Images, err = r.restoreHistoryImages(value.Images, snapshot.ImageBlobHashes)
			if err != nil {
				return nil, err
			}
			blockBytes, err = json.MarshalIndent(value, "", "  ")
		}
		if err != nil {
			return nil, err
		}
		blocks[name] = json.RawMessage(blockBytes)
	}
	item := artifact.Artifact{
		ID: metadata.ID, Kind: metadata.Kind, Title: metadata.Title,
		FormatVersion: metadata.FormatVersion,
		CreatedAt:     metadata.CreatedAt, ModifiedAt: metadata.ModifiedAt,
	}
	encoded, err := artifactfile.Encode(item, string(body), blocks)
	if err != nil {
		return nil, err
	}
	return encoded, nil
}

func (r *Repository) recoverHistoryJournal(ctx context.Context, path, dir string) error {
	journalPath := filepath.Join(dir, "journal.json")
	data, err := os.ReadFile(journalPath)
	if errors.Is(err, fs.ErrNotExist) {
		pendingPath := filepath.Join(dir, pendingHistoryManifest)
		if removeErr := os.Remove(pendingPath); removeErr != nil && !errors.Is(removeErr, fs.ErrNotExist) {
			r.addWarning(fmt.Errorf("remove orphaned history manifest: %w", removeErr))
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("read history journal: %w", err)
	}
	var journal history.Journal
	if err := json.Unmarshal(data, &journal); err != nil {
		return fmt.Errorf("decode history journal: %w", err)
	}
	current, err := readRegularFile(path, 64<<20)
	if err != nil {
		return fmt.Errorf("resolve history journal: %w", err)
	}
	digest := historyDigest(current)
	switch digest {
	case journal.ToDigest:
		if journal.ManifestDigest != "" {
			manifestBytes, err := os.ReadFile(filepath.Join(dir, pendingHistoryManifest))
			if err != nil {
				return fmt.Errorf("read pending history manifest: %w", err)
			}
			if historyDigest(manifestBytes) != journal.ManifestDigest {
				return errors.New("pending history manifest digest mismatch")
			}
			var manifest history.Manifest
			if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
				return fmt.Errorf("decode pending history manifest: %w", err)
			}
			if err := validateHistoryManifest(manifest, path); err != nil {
				return err
			}
			if journal.TargetHead != manifest.HeadIndex ||
				manifest.Entries[journal.TargetHead].Snapshot.FileDigest != journal.ToDigest {
				return errors.New("history journal target does not match pending manifest")
			}
			if err := r.writeHistoryManifest(ctx, dir, manifest); err != nil {
				return err
			}
		} else {
			manifest, err := r.readHistoryManifest(dir, path)
			if err != nil {
				return err
			}
			if journal.TargetHead < 0 || journal.TargetHead >= len(manifest.Entries) ||
				manifest.Entries[journal.TargetHead].Snapshot.FileDigest != journal.ToDigest {
				return errors.New("history journal target does not match manifest")
			}
			manifest.HeadIndex = journal.TargetHead
			if err := r.writeHistoryManifest(ctx, dir, manifest); err != nil {
				return err
			}
		}
	case journal.FromDigest:
	default:
		return errors.New("File modified externally during persistent history transition; history preserved.")
	}
	r.finishHistoryTransition(ctx, dir)
	return nil
}

func (r *Repository) Clean(ctx context.Context, path string) error {
	return r.withLock(ctx, func() error {
		_, dir, err := r.historyPaths(path)
		if err != nil {
			return err
		}
		if err := ensureDirectory(r.historyDir()); err != nil {
			return fmt.Errorf("inspect history directory: %w", err)
		}
		if err := os.RemoveAll(dir); err != nil {
			return fmt.Errorf("clean file history: %w", err)
		}
		return r.gcUnlocked(ctx)
	})
}

func (r *Repository) CleanAll(ctx context.Context) error {
	return r.withLock(ctx, func() error {
		root := r.historyDir()
		if err := ensureDirectory(root); err != nil {
			return fmt.Errorf("inspect history directory: %w", err)
		}
		entries, err := os.ReadDir(root)
		if err != nil {
			return fmt.Errorf("list history: %w", err)
		}
		for _, entry := range entries {
			if entry.Name() == "blobs" {
				continue
			}
			if err := os.RemoveAll(filepath.Join(root, entry.Name())); err != nil {
				return fmt.Errorf("clean history for %s: %w", entry.Name(), err)
			}
		}
		return r.gcUnlocked(ctx)
	})
}

func (r *Repository) GC(ctx context.Context) error {
	return r.withLock(ctx, func() error { return r.gcUnlocked(ctx) })
}

func (r *Repository) gcUnlocked(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	root := r.historyDir()
	if err := ensureDirectory(root); err != nil {
		return fmt.Errorf("inspect history directory: %w", err)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return fmt.Errorf("list history: %w", err)
	}
	referenced := make(map[string]bool)
	for _, entry := range entries {
		if !entry.IsDir() || entry.Name() == "blobs" {
			continue
		}
		manifestBytes, err := os.ReadFile(filepath.Join(root, entry.Name(), "index.json"))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("read history manifest: %w", err)
		}
		var manifest history.Manifest
		if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
			return fmt.Errorf("decode history manifest: %w", err)
		}
		if manifest.SchemaVersion != historySchemaVersion || manifest.HeadIndex < 0 ||
			manifest.HeadIndex >= len(manifest.Entries) {
			return errors.New("invalid history manifest")
		}
		for _, item := range manifest.Entries {
			snapshot := item.Snapshot
			for _, hash := range []string{snapshot.RawBlobHash, snapshot.MetadataBlobHash, snapshot.BodyBlobHash} {
				if hash != "" {
					referenced[hash] = true
				}
			}
			for _, chunks := range []map[string]string{snapshot.BlockBlobHashes, snapshot.ImageBlobHashes} {
				for _, hash := range chunks {
					referenced[hash] = true
				}
			}
		}
	}
	blobDir := filepath.Join(root, "blobs")
	if err := ensureDirectory(blobDir); err != nil {
		return fmt.Errorf("inspect history blob directory: %w", err)
	}
	blobs, err := os.ReadDir(blobDir)
	if err != nil {
		return fmt.Errorf("list history blobs: %w", err)
	}
	for _, blob := range blobs {
		if blob.IsDir() || referenced[blob.Name()] {
			continue
		}
		if err := os.Remove(filepath.Join(blobDir, blob.Name())); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("remove orphaned history blob %s: %w", blob.Name(), err)
		}
	}
	return nil
}
