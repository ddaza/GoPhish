package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"gophish/internal/config"
)

func TestViewModelRendersStatus(t *testing.T) {
	cfg, err := config.Load("") // defaults
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}

	view := New(cfg).View()

	for _, want := range []string{
		"GoPhish",
		"defensive smishing/phishing OSINT detector",
		"services",
		"rdap",
		"enabled",
		"press q to quit",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("view missing %q\n--- view ---\n%s", want, view)
		}
	}
}

func TestModelQuitsOnQ(t *testing.T) {
	m := New(&config.Config{})
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	if cmd == nil {
		t.Fatal("expected a quit command for 'q'")
	}
	// The returned model should be usable; calling View must not panic.
	_ = updated
	if v := updated.View(); v == "" {
		t.Error("expected non-empty view after update")
	}
}
