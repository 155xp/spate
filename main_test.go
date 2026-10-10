package main

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestSearchAcceptsShortcutLetters(t *testing.T) {
	m := model{}
	keys := []tea.KeyMsg{
		{Type: tea.KeyRunes, Runes: []rune("jo")},
		{Type: tea.KeySpace},
		{Type: tea.KeyRunes, Runes: []rune("k")},
	}
	for _, key := range keys {
		next, _ := m.Update(key)
		m = next.(model)
	}
	if m.query != "jo k" {
		t.Fatalf("query = %q, want %q", m.query, "jo k")
	}
}
