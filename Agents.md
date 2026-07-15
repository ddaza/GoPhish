# Agents.md — GoPhish

Guidance for AI coding agents (and humans) working in this repository.

## 1. Purpose

GoPhish is a **defensive OSINT tool** for detecting newly registered and
suspicious URLs used in **smishing** (SMS phishing) and **phishing** campaigns.

It helps defenders answer three questions:

1. **What domains were just registered** that look like they belong to a brand
   or target we care about?
2. **What look-alike / bulk-registered URLs exist** that are similar to a known
   legitimate domain (typosquats, homoglyphs, TLD swaps, sibling registrations)?
3. **Which of these are actually malicious or suspicious**, and why — using
   both deterministic signals and a local LLM for narrative analysis?

The product is a **terminal UI (TUI)** with **local LLM integration** for data
analysis. No external LLM APIs are used by default; analysis runs on a model
hosted locally.

> **Defensive-only scope.** This tool is for authorized security research,
> brand protection, and incident response. It must never generate, host, or
> deliver phishing content, nor automate attacks. It produces *detections and
> analysis*, not operational phishing infrastructure.

## 2. Stack & key decisions

| Concern            | Choice                                   | Notes |
|--------------------|------------------------------------------|-------|
| Language           | **Go** (1.22+)                           | Project name; strong for CLIs, concurrency, single binaries. |
| TUI                | **Bubble Tea** (charmbracelet) + Lipgloss + Bubbles | Idiomatic Go TUI framework. |
| Data fetching      | stdlib `net/http` + `golang.org/x/time/rate` | Respect rate limits per source. |
| Config             | `Viper` + `Cobra`                        | CLI flags/subcommands + config file. |
| Local LLM          | `llama.cpp` via `go-llama.cpp` (or `ollama` HTTP) | Pluggable backend; default to Ollama for portability. |
| Storage (local)    | SQLite (`modernc.org/sqlite`)            | Zero-dependency local cache of seen domains/results. |
| Logging            | `zerolog`                                | Structured, leveled. |

If a better-fit library is clearly superior, propose it in a PR description;
do not silently swap foundational dependencies.

## 3. Proposed directory layout

```text
cmd/gophish/            # main entrypoint, wires TUI + commands
internal/
  config/               # config loading, API keys, model settings
  sources/              # OSINT data source integrations (one file per source)
    rdap.go             # RDAP registration data
    whois.go            # fallback WHOIS (where RDAP unavailable)
    certstream.go       # certificate transparency live stream
    crtsh.go            # certificate transparency historical lookup
    virustotal.go       # reputation (optional, key-gated)
    urlscan.go          # scan/live results (optional, key-gated)
    phishtank.go        # known phishing feed
  fuzz/                 # URL/domain fuzzing generators
    typosquat.go
    homoglyph.go
    tldswap.go
    permutations.go
  detect/               # scoring + detection logic
    similarity.go       # edit distance, jaccard, brand matching
    bulkreg.go          # cluster bulk/templated registrations
    score.go            # weighted risk scoring
  llm/                  # local LLM client + prompt templates
    client.go
    prompts.go
  store/                # SQLite persistence + cache
  tui/                  # Bubble Tea models, views, styles
    app.go
    views/
  analyze/              # orchestration: sources -> fuzz -> detect -> llm
  cli/                  # Cobra commands (scan, watch, report, model)
test/                   # integration/behavioral tests & fixtures
docs/                   # architecture, threat model, data-source notes
```

## 4. Core subsystems

### 4.1 Data sources (`internal/sources`)
Each source implements a common interface:
```go
type Source interface {
    Name() string
    Fetch(ctx context.Context, q Query) ([]Result, error)
}
```
- Prefer **RDAP** over legacy WHOIS (structured, no scraping, rate-friendly).
- **Certificate Transparency** (crt.sh + CertStream) is the primary early signal
  for newly issued certificates — often earlier than WHOIS.
- Key-gated sources (VirusTotal, URLScan) are disabled unless a key is present;
  never fail the whole pipeline if an optional source errors.
- Every fetch records provenance (source, timestamp, query) for auditability.

### 4.2 Fuzzing (`internal/fuzz`)
Generate candidate look-alike domains from a seed legitimate domain:
- **Typosquat**: char omission/insertion/transposition/adjacent-key.
- **Homoglyph / punycode**: confusable Unicode (e.g. `rn` ↔ `m`, Cyrillic `а`).
- **TLD swap**: same SLD across common/generic TLDs.
- **Permutations**: brand+keyword (e.g. `brand-login`, `brand-secure`).
Cap combinatorial explosion: dedupe, normalize to punycode, and bound output
(e.g. `--max 5000`). Flag generated domains, do not auto-query them all by
default — offer "resolve/check" as an explicit action.

### 4.3 Detection (`internal/detect`)
- **Similarity**: Levenshtein/Damerau, Jaccard on tokens, brand-substring match.
- **Bulk registration**: cluster domains by registrar, creation-window,
  nameservers, and templated name patterns to surface campaign-scale activity.
- **Risk score**: weighted combination of signals (age, similarity, CT volume,
  ASN/reputation, homoglyph use). Output is a score + human-readable reasons.

### 4.4 Local LLM (`internal/llm`)
- Default backend: **Ollama** HTTP API (`/api/generate`) with a configurable
  model (e.g. `llama3.1:8b`). Fallback: direct `llama.cpp` bindings.
- Used for: summarizing a cluster of suspicious domains into a campaign
  narrative, explaining *why* a domain is risky, and proposing related IOCs.
- Prompt templates live in `prompts.go`. Keep prompts constrained and
  instruction-tight; require the model to return structured output (JSON) that
  is validated before display.
- Never send secrets, cookies, or raw credentials to the model. Only public
  OSINT attributes (domain, registrar, cert, score, reasons).

### 4.5 TUI (`internal/tui`)
- Bubble Tea `Model` with views: **Search/Seed**, **Results list**,
  **Domain detail**, **Clusters/ campaigns**, **LLM analysis**, **Settings**.
- Keyboard-first; mouse optional. Use Lipgloss for theming, Bubbles for
  lists/textinputs/viewport.
- Never block the UI thread: all network/LLM work goes through
  `cmd`/`tea.Cmd` with spinners and cancellable contexts.

## 5. Conventions

- **Style**: `gofmt`/`goimports`; `go vet` and `golangci-lint` clean.
- **Errors**: wrap with context; don't swallow. Log at source, surface to UI.
- **Tests**: unit tests for `fuzz`, `detect`, `sources` parsing; fixtures under
  `test/`. Network calls mocked via `httptest`.
- **Commits**: conventional prefixes (`feat:`, `fix:`, `chore:`, `docs:`,
  `test:`). Keep commits focused.
- **Config secrets**: read from env/config file; never hardcode API keys;
  `.env` is gitignored.
- **No external network at build time**; network only at runtime, behind flags.

## 6. Security & legal guardrails (mandatory)

1. **Authorized use only.** Operators must have a legitimate defensive remit
   (own brand, employer IR, sanctioned research). The tool's docs/help must
   state this.
2. **Read-only OSINT.** Only query public data sources and local resolution.
   No scanning of hosts, no exploitation, no credential use beyond API keys the
   user explicitly provides for reputation services.
3. **No phishing generation.** The fuzzer produces *candidate look-alikes for
   detection*. It must not produce ready-to-send lures, payloads, or message
   templates. LLM prompts must forbid generating attack content.
4. **Rate limiting & politeness.** Honor each source's ToS and rate limits.
   Provide conservative defaults and per-source concurrency caps.
5. **Data minimization.** Cache only what's needed for analysis; provide a
   `store purge` command. No PII beyond what public OSINT inherently contains.
6. **Provenance & false positives.** Every detection includes its evidence and
   source. The UI must clearly label low-confidence results as "suspicious,
   unverified" to avoid wrongful takedown claims.

## 7. How to work in this repo (for agents)

- Before adding a feature, check whether it fits one of the subsystems above;
  place code in the matching `internal/` package.
- When adding a data source, implement the `Source` interface, add config keys,
  document the source in `docs/`, and add a parsing test with a fixture.
- When touching the LLM layer, keep prompts in `prompts.go` and add a
  structured-output validation test.
- Prefer small, compilable increments. Run `go build ./...` and
  `go test ./...` before declaring a task done.
- If a requested change would violate Section 6, refuse that part and explain,
  then offer the compliant alternative.
