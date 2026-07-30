package ui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestChooserReturnsSortedSelection(t *testing.T) {
	model := newChooser([]Choice{
		{ID: "zeta", Label: "Zeta"},
		{ID: "alpha", Label: "Alpha"},
	})

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeySpace})
	model = updated.(chooser)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyDown})
	model = updated.(chooser)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeySpace})
	model = updated.(chooser)

	selection := model.selection()
	if len(selection) != 2 || selection[0] != "alpha" || selection[1] != "zeta" {
		t.Fatalf("selection = %v, want sorted alpha/zeta", selection)
	}
}
