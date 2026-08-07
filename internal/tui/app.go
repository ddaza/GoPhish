// Package tui implements the GoPhish terminal UI using Bubble Tea.
//
// This is the initial "hello world" shell: it renders a status view of the
// loaded OSINT sources and exits cleanly on q / ctrl+c / esc. Additional
// views (search/seed, results, domain detail, clusters, LLM analysis,
// settings) are added incrementally per AGENTS.md §5.5.
package tui

import (
	"sort"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"gophish/internal/config"
)

// Model is the root Bubble Tea model for the GoPhish TUI.
type Model struct {
	cfg *config.Config
}

// New constructs the root model from loaded configuration.
func New(cfg *config.Config) Model {
	return Model{cfg: cfg}
}

// Init implements tea.Model. The hello-world shell has no startup commands.
func (m Model) Init() tea.Cmd { return nil }

// Update implements tea.Model.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c", "esc":
			return m, tea.Quit
		}
	}
	return m, nil
}

// View implements tea.Model. It renders the hello-world status screen.
func (m Model) View() string {
	var b strings.Builder

	title := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("205")).
		Render("GoPhish")
	subtitle := lipgloss.NewStyle().
		Faint(true).
		Render("defensive smishing/phishing OSINT detector")

	b.WriteString(title + "\n")
	b.WriteString(subtitle + "\n\n")

	enabled := 0
	for _, svc := range m.cfg.Services {
		if svc.Enabled {
			enabled++
		}
	}
	b.WriteString(lipgloss.NewStyle().Render(
		"Loaded "+strconv.Itoa(len(m.cfg.Services))+
			" services ("+strconv.Itoa(enabled)+" enabled)") + "\n\n")

	names := make([]string, 0, len(m.cfg.Services))
	for name := range m.cfg.Services {
		names = append(names, name)
	}
	sort.Strings(names)

	onStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("46"))
	offStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	nameStyle := lipgloss.NewStyle().Bold(true).Width(14)

	for _, name := range names {
		svc := m.cfg.Services[name]
		state := offStyle.Render("disabled")
		if svc.Enabled {
			state = onStyle.Render("enabled ")
		}
		url := svc.BaseURL
		if url == "" {
			url = "(none)"
		}
		b.WriteString(nameStyle.Render(name) + "  " +
			state + "  " +
			lipgloss.NewStyle().Faint(true).Render(url) + "\n")
	}

	b.WriteString("\n" + lipgloss.NewStyle().Faint(true).Render("press q to quit"))
	return b.String()
}
