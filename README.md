# GoPhish

**Defensive OSINT tool for detecting newly registered and suspicious URLs used
in smishing (SMS phishing) and phishing campaigns.**

GoPhish helps defenders answer three questions:

1. **What domains were just registered** that look like they belong to a
   brand or target we care about?
2. **What look-alike / bulk-registered URLs exist** (typosquats, homoglyphs,
   TLD swaps, sibling registrations) similar to a known legitimate domain?
3. **Which of these are actually malicious or suspicious**, and why — using
   deterministic signals and a local LLM for narrative analysis?

> **Defensive-only scope.** Authorized use only. The tool must never generate,
> host, or deliver phishing content. See [`AGENTS.md`](./AGENTS.md) for full
> guardrails.

## Status

This repository is an **early skeleton**. The current code loads configuration
and launches a minimal TUI. The full pipeline (`sources → fuzz → detect → llm`)
is planned but not yet implemented.

See [`docs/Plan.md`](./docs/Plan.md) for the architecture and roadmap.

## Getting started

### Requirements

- Go 1.22+

### Build & run

```bash
go build -o gophish ./cmd/gophish
./gophish
```

### Configuration

Copy the example config and edit it:

```bash
cp config.example.toml gophish.toml
```

Sources are config-driven (enabled state, rate, quota, optional API key). See
[`config.example.toml`](./config.example.toml) and
[`docs/Plan.md`](./docs/Plan.md) for details.

## Documentation

- Architecture & roadmap: [`docs/Plan.md`](./docs/Plan.md)
- Contributor guidance (how to implement): [`AGENTS.md`](./AGENTS.md)
- Architecture decisions: [`docs/adr/`](./docs/adr/)
