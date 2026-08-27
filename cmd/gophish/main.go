// Command gophish is a defensive OSINT tool for detecting newly registered and
// suspicious URLs used in smishing and phishing campaigns.
//
// This is the initial skeleton: it loads configuration and launches the
// terminal UI. The core pipeline (sources -> fuzz -> detect -> llm) is
// scaffolded incrementally in the internal/ packages.
package main

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"

	"gophish/internal/config"
	"gophish/internal/tui"
)

func main() {
	cfg, err := config.Load("gophish.toml")
	if err != nil {
		fmt.Fprintf(os.Stderr, "config error: %v\n", err)
		os.Exit(1)
	}

	p := tea.NewProgram(tui.New(cfg))
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "tui error: %v\n", err)
		os.Exit(1)
	}
}
