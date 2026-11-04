import re

with open("internal/tui/model_test.go", "r") as f:
    content = f.read()

# Refactor TestRun
test_run_search = """	t.Run("canceled context with note config", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel() // pre-canceled context so program shuts down immediately

		cfg := Config{
			Path:     path,
			Kind:     artifact.NoteKind,
			Notes:    note.NewService(repo, 10),
			Recovery: repo,
		}

		err := Run(ctx, cfg, headlessOpts...)
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("unexpected error running program: %v", err)
		}
	})

	t.Run("canceled context with documents config", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		cfg := Config{
			Path:      filepath.Join(t.TempDir(), "doc.md"),
			Kind:      artifact.DocumentKind,
			Documents: document.NewService(repo, 10),
			Recovery:  repo,
		}

		err := Run(ctx, cfg, headlessOpts...)
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("unexpected error running program with documents config: %v", err)
		}
	})

	t.Run("canceled context with spreadsheet config", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		cfg := Config{
			Path:         filepath.Join(t.TempDir(), "sheet.csv"),
			Kind:         artifact.SpreadsheetKind,
			Spreadsheets: spreadsheet.NewService(repo, 10),
			Recovery:     repo,
		}

		err := Run(ctx, cfg, headlessOpts...)
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("unexpected error running program with spreadsheet config: %v", err)
		}
	})

	t.Run("canceled context with presentation config", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		cfg := Config{
			Path:          filepath.Join(t.TempDir(), "deck.md"),
			Kind:          artifact.PresentationKind,
			Presentations: presentation.NewService(repo, 10),
			Recovery:      repo,
		}

		err := Run(ctx, cfg, headlessOpts...)
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("unexpected error running program with presentation config: %v", err)
		}
	})

	t.Run("canceled context with choose kind config", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		cfg := Config{
			Path:     filepath.Join(t.TempDir(), "unknown.md"),
			Kind:     "",
			Recovery: repo,
		}

		err := Run(ctx, cfg, headlessOpts...)
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("unexpected error running program with choose kind config: %v", err)
		}
	})"""

test_run_replace = """	testCases := []struct {
		name string
		cfg  Config
		errMsg string
	}{
		{"canceled context with note config", Config{Path: path, Kind: artifact.NoteKind, Notes: note.NewService(repo, 10), Recovery: repo}, "unexpected error running program"},
		{"canceled context with documents config", Config{Path: filepath.Join(t.TempDir(), "doc.md"), Kind: artifact.DocumentKind, Documents: document.NewService(repo, 10), Recovery: repo}, "unexpected error running program with documents config"},
		{"canceled context with spreadsheet config", Config{Path: filepath.Join(t.TempDir(), "sheet.csv"), Kind: artifact.SpreadsheetKind, Spreadsheets: spreadsheet.NewService(repo, 10), Recovery: repo}, "unexpected error running program with spreadsheet config"},
		{"canceled context with presentation config", Config{Path: filepath.Join(t.TempDir(), "deck.md"), Kind: artifact.PresentationKind, Presentations: presentation.NewService(repo, 10), Recovery: repo}, "unexpected error running program with presentation config"},
		{"canceled context with choose kind config", Config{Path: filepath.Join(t.TempDir(), "unknown.md"), Kind: "", Recovery: repo}, "unexpected error running program with choose kind config"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			cancel() // pre-canceled context so program shuts down immediately
			err := Run(ctx, tc.cfg, headlessOpts...)
			if err != nil && !errors.Is(err, context.Canceled) {
				t.Fatalf("%s: %v", tc.errMsg, err)
			}
		})
	}"""

content = content.replace(test_run_search, test_run_replace)

# Refactor TestModelInit_detects_recovery_draft_for_note_and_document
test_draft_search = """	// 1. Note draft recovery
	notePath := filepath.Join(t.TempDir(), "note_with_draft.md")
	if _, err := notes.Create(ctx, notePath, "original body"); err != nil {
		t.Fatal(err)
	}

	noteDraft := recovery.Draft{
		Path:      notePath,
		Kind:      string(artifact.NoteKind),
		Data:      json.RawMessage(`{"body":"unsaved body draft"}`),
		UpdatedAt: time.Now(),
	}
	if err := repo.SaveRecovery(ctx, noteDraft); err != nil {
		t.Fatal(err)
	}

	mNote := NewModel(Config{
		Path:     notePath,
		Kind:     artifact.NoteKind,
		Notes:    notes,
		Recovery: repo,
	})
	cmdNote := mNote.Init()
	if cmdNote == nil {
		t.Fatal("expected non-nil cmd from Init for note")
	}
	msgNote := cmdNote()
	openedMsgNote, ok := msgNote.(fileOpenedMsg)
	if !ok {
		t.Fatalf("expected fileOpenedMsg, got %T", msgNote)
	}
	if openedMsgNote.err != nil {
		t.Fatalf("unexpected error: %v", openedMsgNote.err)
	}
	if !openedMsgNote.hasDraft {
		t.Fatal("expected hasDraft = true when note recovery draft exists")
	}
	if openedMsgNote.draft.Path != notePath {
		t.Fatalf("expected draft path %q, got %q", notePath, openedMsgNote.draft.Path)
	}

	// 2. Document draft recovery
	docPath := filepath.Join(t.TempDir(), "doc_with_draft.md")
	if _, err := docs.Create(ctx, docPath, document.Draft{Layout: document.DefaultLayout()}); err != nil {
		t.Fatal(err)
	}

	docDraft := recovery.Draft{
		Path:      docPath,
		Kind:      string(artifact.DocumentKind),
		Data:      json.RawMessage(`{"body":"unsaved doc draft"}`),
		UpdatedAt: time.Now(),
	}
	if err := repo.SaveRecovery(ctx, docDraft); err != nil {
		t.Fatal(err)
	}

	mDoc := NewModel(Config{
		Path:      docPath,
		Kind:      artifact.DocumentKind,
		Documents: docs,
		Recovery:  repo,
	})
	cmdDoc := mDoc.Init()
	if cmdDoc == nil {
		t.Fatal("expected non-nil cmd from Init for document")
	}
	msgDoc := cmdDoc()
	openedMsgDoc, ok := msgDoc.(fileOpenedMsg)
	if !ok {
		t.Fatalf("expected fileOpenedMsg, got %T", msgDoc)
	}
	if openedMsgDoc.err != nil {
		t.Fatalf("unexpected error: %v", openedMsgDoc.err)
	}
	if !openedMsgDoc.hasDraft {
		t.Fatal("expected hasDraft = true when document recovery draft exists")
	}
	if openedMsgDoc.draft.Path != docPath {
		t.Fatalf("expected draft path %q, got %q", docPath, openedMsgDoc.draft.Path)
	}"""

test_draft_replace = """	notePath := filepath.Join(t.TempDir(), "note_with_draft.md")
	if _, err := notes.Create(ctx, notePath, "original body"); err != nil {
		t.Fatal(err)
	}
	docPath := filepath.Join(t.TempDir(), "doc_with_draft.md")
	if _, err := docs.Create(ctx, docPath, document.Draft{Layout: document.DefaultLayout()}); err != nil {
		t.Fatal(err)
	}

	testCases := []struct {
		name string
		path string
		kind artifact.Kind
		data string
		cfg  Config
	}{
		{"note", notePath, artifact.NoteKind, `{"body":"unsaved body draft"}`, Config{Path: notePath, Kind: artifact.NoteKind, Notes: notes, Recovery: repo}},
		{"document", docPath, artifact.DocumentKind, `{"body":"unsaved doc draft"}`, Config{Path: docPath, Kind: artifact.DocumentKind, Documents: docs, Recovery: repo}},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			draft := recovery.Draft{
				Path:      tc.path,
				Kind:      string(tc.kind),
				Data:      json.RawMessage(tc.data),
				UpdatedAt: time.Now(),
			}
			if err := repo.SaveRecovery(ctx, draft); err != nil {
				t.Fatal(err)
			}

			m := NewModel(tc.cfg)
			cmd := m.Init()
			if cmd == nil {
				t.Fatalf("expected non-nil cmd from Init for %s", tc.name)
			}
			msg := cmd()
			openedMsg, ok := msg.(fileOpenedMsg)
			if !ok {
				t.Fatalf("expected fileOpenedMsg, got %T", msg)
			}
			if openedMsg.err != nil {
				t.Fatalf("unexpected error: %v", openedMsg.err)
			}
			if !openedMsg.hasDraft {
				t.Fatalf("expected hasDraft = true when %s recovery draft exists", tc.name)
			}
			if openedMsg.draft.Path != tc.path {
				t.Fatalf("expected draft path %q, got %q", tc.path, openedMsg.draft.Path)
			}
		})
	}"""

content = content.replace(test_draft_search, test_draft_replace)

with open("internal/tui/model_test.go", "w") as f:
    f.write(content)
