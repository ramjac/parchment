// Package printing adapts artifacts to the shared printer renderers.
package printing

import (
	"example.com/parchment/internal/document"
	"example.com/parchment/internal/note"
)

// NoteDocument supplies the default printer layout without changing the note.
func NoteDocument(n note.Note) document.Document {
	return document.Document{Artifact: n.Artifact, Body: n.Body, Layout: document.DefaultLayout()}
}
