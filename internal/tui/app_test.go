package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gophish/internal/config"
)

func TestViewModelRendersStatus(t *testing.T) {
	cfg, err := config.Load("") // defaults
	require.NoError(t, err)

	view := New(cfg).View()

	for _, want := range []string{
		"GoPhish",
		"defensive smishing/phishing OSINT detector",
		"services",
		"rdap",
		"enabled",
		"press q to quit",
	} {
		assert.Contains(t, view, want)
	}
}

func TestModelQuitsOnQ(t *testing.T) {
	m := New(&config.Config{})
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	require.NotNil(t, cmd, "expected a quit command for 'q'")
	// The returned model should be usable; calling View must not panic.
	_ = updated
	assert.NotEmpty(t, updated.View(), "expected non-empty view after update")
}
