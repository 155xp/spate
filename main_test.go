package main

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestSearchAcceptsShortcutLetters(t *testing.T) {
	m := model{}
	for _, r := range "jok" {
		next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = next.(model)
	}
	if m.query != "jok" {
		t.Fatalf("query = %q, want jok", m.query)
	}
}
