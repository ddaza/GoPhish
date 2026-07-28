// Command gophish is a defensive OSINT tool for detecting newly registered and
// suspicious URLs used in smishing and phishing campaigns.
//
// This is the initial skeleton: it loads configuration and prints the
// config-driven service endpoints. The core pipeline (sources -> fuzz ->
// detect -> llm) is scaffolded incrementally in the internal/ packages.
package main

import (
	"fmt"
	"os"

	"gophish/internal/config"
)

func main() {
	fmt.Println("GoPhish — defensive smishing/phishing OSINT detector")

	cfg, err := config.Load("gophish.toml")
	if err != nil {
		fmt.Fprintf(os.Stderr, "config error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("loaded %d services (config-driven URLs)\n", len(cfg.Services))
	for name, svc := range cfg.Services {
		state := "disabled"
		if svc.Enabled {
			state = "enabled"
		}
		url := svc.BaseURL
		if url == "" {
			url = "(none)"
		}
		fmt.Printf("  %-12s %-8s %s\n", name, state, url)
	}
}
