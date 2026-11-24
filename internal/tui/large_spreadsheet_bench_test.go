package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/ramjac/parchment/internal/filerepo"
	"github.com/ramjac/parchment/internal/spreadsheet"
)

func BenchmarkLargeSpreadsheet(b *testing.B) {
	ctx := context.Background()
	repo, err := filerepo.New(b.TempDir(), filerepo.Options{})
	if err != nil {
		b.Fatal(err)
	}
	service := spreadsheet.NewService(repo, 10)
	data, err := os.ReadFile(filepath.Join("testdata", "mortgage-budget.md"))
	if err != nil {
		b.Fatal(err)
	}
	path := filepath.Join(b.TempDir(), "mortgage-budget.md")
	if err := os.WriteFile(path, data, 0600); err != nil {
		b.Fatal(err)
	}
	item, err := service.Get(ctx, path)
	if err != nil {
		b.Fatal(err)
	}
	newModel := func(width, height int) *spreadsheetModel {
		m := newSpreadsheetModel(service, path, Theme{}, 10)
		_, _ = m.Update(tea.WindowSizeMsg{Width: width, Height: height})
		if message := m.Init()(); message != nil {
			_, _ = m.Update(message)
		}
		if !m.loaded || m.pending || m.errMessage != "" {
			b.Fatalf("spreadsheet did not load: %s", m.errMessage)
		}
		return m
	}
	b.Run("Open80x24", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = newModel(80, 24)
		}
	})
	for _, size := range []struct{ width, height int }{{80, 24}, {120, 40}} {
		b.Run(fmt.Sprintf("ScrollAndSwitch/%dx%d", size.width, size.height), func(b *testing.B) {
			m := newModel(size.width, size.height)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyPgDown})
				_ = m.View().Content
				_, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyPgUp})
				_, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
				_ = m.View().Content
				_, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
			}
			b.StopTimer()
			if m.sheet != 0 || m.row != 0 {
				b.Fatalf("navigation state was not restored: sheet=%d row=%d", m.sheet, m.row)
			}
		})
	}
	b.Run("EditRecalculateSave", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			value := "400000"
			if item.Sheets[0].Rows[0][1].Value == value {
				value = "300000"
			}
			item, err = service.SetCellInSheet(ctx, path, spreadsheet.SetCellOptions{
				Sheet: "Mortgage", Row: 1, Column: 2, Cell: spreadsheet.Cell{Value: value},
			})
			if err != nil {
				b.Fatal(err)
			}
		}
		b.StopTimer()
		loaded, err := service.Get(ctx, path)
		if err != nil || loaded.Sheets[0].Rows[0][1] != item.Sheets[0].Rows[0][1] {
			b.Fatalf("spreadsheet edit did not round-trip: %v", err)
		}
	})
	b.Run("CycleError", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_, err := service.SetCellInSheet(ctx, path, spreadsheet.SetCellOptions{
				Sheet: "Department1", Row: 2, Column: 14, Cell: spreadsheet.Cell{Formula: "=N2"},
			})
			if err == nil {
				b.Fatal("circular formula was accepted")
			}
		}
	})
}
