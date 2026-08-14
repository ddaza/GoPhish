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
> deliver phishing content, nor automate attacks. It produces _detections and
> analysis_, not operational phishing infrastructure.

> **YAGNI / minimal scope.** Build only what answers the three questions above.
> Rely exclusively on **free or freemium public data sources** — no paid,
> premium reputation services for now. Keep the
> dependency surface small and the architecture boring. Defer anything not
> needed to ship the core loop (sources → fuzz → detect → LLM).

## 2. Stack & key decisions

| Concern         | Choice                                              | Notes                                                        |
| --------------- | --------------------------------------------------- | ------------------------------------------------------------ |
| Language        | **Go** (1.22+)                                      | Project name; strong for CLIs, concurrency, single binaries. |
| TUI             | **Bubble Tea** (charmbracelet) + Lipgloss + Bubbles | Idiomatic Go TUI framework.                                  |
| Data fetching   | stdlib `net/http` + `golang.org/x/time/rate`        | Respect rate limits per source.                              |
| Config          | `Viper` + `Cobra`                                   | CLI flags/subcommands + config file.                         |
| Local LLM       | `llama.cpp` via `go-llama.cpp` (or `ollama` HTTP)   | Pluggable backend; default to Ollama for portability.        |
| Storage (local) | SQLite (`modernc.org/sqlite`)                       | Zero-dependency local cache of seen domains/results.         |
| Logging         | `zerolog`                                           | Structured, leveled.                                         |

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
    phishtank.go        # known phishing feed (free, no paid tier)
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
  throttle/             # per-source rate + quota limiting (hits/min, max checks)
  cli/                  # Cobra commands (scan, watch, report, model)
test/                   # integration/behavioral tests & fixtures
docs/                   # architecture, threat model, data-source notes
  adr/                  # ADRs (NNNN-title.md) + template.md
```

## 4. Core loop (the MVP)

GoPhish's value is a single pipeline that turns a seed (a brand/domain of
interest) into a ranked, explained list of suspicious look-alikes:

```text
   seed domain
        │
        ▼
   sources ──► RDAP / WHOIS, Certificate Transparency (crt.sh, CertStream),
        │       PhishTank  →  freshly registered & certified domains
        ▼
   fuzz    ──► generate look-alike candidates from the seed (typosquat,
        │       homoglyph, TLD swap, permutations)
        ▼
   detect  ──► score & cluster candidates against real-world observations
        │       (similarity, bulk-registration clusters, risk score)
        ▼
   llm     ──► summarize clusters into a campaign narrative + explain
                *why* a domain is risky, in structured (JSON) output
```

### 4.1 The fuzz ↔ source-check mini loop

Inside the macro pipeline, `fuzz` and `detect` collaborate through a tighter
**mini loop** that drives discovery of bulk registrations:

```text
   fuzz        ──► generate candidates for a seed / confirmed cluster
     │
     ▼
   source check ──► RDAP / WHOIS / CT: is the candidate actually registered,
     │               and by whom (registrar, nameservers, creation date)?
     ▼
   fuzz again  ──► expand around confirmed hits (same registrar, sibling
                    TLDs, templated name patterns) to pull in the rest of
                    the campaign, then check those too.
```

- The signal we specifically hunt for is **shared registration
  infrastructure**: candidates that resolve to the _same registrar_ (ideally
  also same nameservers / tight creation window) are strong bulk-registration
  / campaign indicators.
- The mini loop keeps fuzzing and re-checking until no new same-registrar
  domains surface, then hands the resulting cluster to `detect` and `llm`.
- **Bound it** (e.g. `--max`, an iteration cap, per-seed candidate limit) so
  combinatorial expansion can't run away.

- The pipeline is orchestrated by `internal/analyze` (`sources → fuzz →
detect → llm`). Each stage — including the mini loop above — is
  independently testable and runs behind a cancellable context so the TUI
  never blocks.
- Everything is derived from **free/freemium** sources (see §2 YAGNI note);
  no paid reputation lookups.
- This loop _is_ the MVP. Anything not on this path (extra sources, extra
  views, extra scoring features) is deferred until the loop works end to
  end. Sections 5 describes the subsystems that implement each stage.

## 5. Core subsystems

### 5.1 Data sources (`internal/sources`)

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
- Only **free / freemium** sources are in scope. No paid, reputation
  services yet until the core loop ships.
- Every fetch records provenance (source, timestamp, query) for auditability.

### 5.2 Fuzzing (`internal/fuzz`)

Generate candidate look-alike domains from a seed legitimate domain:

- **Typosquat**: char omission/insertion/transposition/adjacent-key.
- **Homoglyph / punycode**: confusable Unicode (e.g. `rn` ↔ `m`, Cyrillic `а`).
- **TLD swap**: same SLD across common/generic TLDs.
- **Permutations**: brand+keyword (e.g. `brand-login`, `brand-secure`).
  Cap combinatorial explosion: dedupe, normalize to punycode, and bound output
  (e.g. `--max 5000`). Flag generated domains, do not auto-query them all by
  default — offer "resolve/check" as an explicit action.

### 5.3 Detection (`internal/detect`)

- **Similarity**: Levenshtein/Damerau, Jaccard on tokens, brand-substring match.
- **Bulk registration**: cluster domains by registrar, creation-window,
  nameservers, and templated name patterns to surface campaign-scale activity.
- **Risk score**: weighted combination of signals (age, similarity, CT volume,
  nameserver/registrar overlap, homoglyph use). Output is a score +
  human-readable reasons. No external reputation lookups — derive signals
  solely from the free OSINT sources above.

### 5.4 Local LLM (`internal/llm`)

- Default backend: **Ollama** HTTP API (`/api/generate`) with a configurable
  model (e.g. `llama3.1:8b`). Fallback: direct `llama.cpp` bindings.
- Used for: summarizing a cluster of suspicious domains into a campaign
  narrative, explaining _why_ a domain is risky, and proposing related IOCs.
- Prompt templates live in `prompts.go`. Keep prompts constrained and
  instruction-tight; require the model to return structured output (JSON) that
  is validated before display.
- Never send secrets, cookies, or raw credentials to the model. Only public
  OSINT attributes (domain, registrar, cert, score, reasons).

### 5.5 TUI (`internal/tui`)

- Bubble Tea `Model` with views: **Search/Seed**, **Results list**,
  **Domain detail**, **Clusters/ campaigns**, **LLM analysis**, **Settings**.
- Keyboard-first; mouse optional. Use Lipgloss for theming, Bubbles for
  lists/textinputs/viewport.
- Never block the UI thread: all network/LLM work goes through
  `cmd`/`tea.Cmd` with spinners and cancellable contexts.

### 5.6 Throttling & quotas (`internal/throttle`)

Free/freemium APIs cap usage two ways, and the throttle system models **both**
per source:

- **Rate** — requests over time (e.g. _hits per minute_ / RPM). Smoothly pace
  calls with `golang.org/x/time/rate` so we stay under a source's rate limit.
- **Quota / total cap** — a hard ceiling on checks (e.g. "max N lookups per
  day", or a lifetime cap). This generalizes limits like VirusTotal's
  max-checks-per-period, but the same could be applied to PhishTank, crt.sh, RDAP, etc.

Design:

- One `Limiter` per source, combining a `rate.Limiter` with a quota counter
  (optional rolling window + reset). Both exposed via config (e.g.
  `requests_per_minute`, `max_checks`, `quota_window`).
- Sources call `Wait(ctx)` before each request. The **mini loop (§4.1)** and
  the orchestrator (`internal/analyze`) must honor it: when a source's quota
  is exhausted, stop checking that source and surface "quota exhausted"
  rather than erroring or hammering it.
- Defaults are conservative; operators tune per source. The fuzz/expansion
  bounds from §4.1 compound with the throttle, so a capped source naturally
  limits how far the mini loop expands.

## 6. Conventions

- **Style**: `gofmt`/`goimports`; `go vet` and `golangci-lint` clean.
- **Errors**: wrap with context; don't swallow. Log at source, surface to UI.
- **Tests**: unit tests for `fuzz`, `detect`, `sources` parsing; fixtures under
  `test/`. Network calls mocked via `httptest`.
  - Use **testify** for assertions and error checks (`assert`, `require`) rather
    than the stdlib `t.Errorf`/`t.Fatalf` style — it reads cleaner and gives
    better failure messages. Import `github.com/stretchr/testify/assert` and
    `.../require`; prefer `require` for fatal preconditions (e.g. setup that
    must succeed before the test body runs) and `assert` for per-assertion
    checks.
  - Prefer `require.NoError`/`assert.NoError`, `assert.Contains`,
    `assert.Equal`, `assert.NotNil`, `assert.NotEmpty`, etc. over manual
    comparisons and string searches.
  - Use a **testify `suite`** (`github.com/stretchr/testify/suite`) for a
    package once it has enough related tests to benefit from shared setup/teardown
    (`SetupTest`/`TearDownTest`) or shared fixtures — e.g. many tests against the
    same `config`, store, or TUI model. Don't force a suite on a file with only
    one or two trivial tests; plain `TestXxx(t *testing.T)` functions are fine
    there. Keep suite names descriptive (`type XxxSuite struct { suite.Suite }`)
    and run them with `suite.Run(t, new(XxxSuite))`.
- **Commits**: conventional prefixes (`feat:`, `fix:`, `chore:`, `docs:`,
  `test:`). Keep commits focused.
- **Config secrets**: free sources may still require a (free) API key
  (e.g. PhishTank). Read any key from env/config, never hardcode it;
  `.env` is gitignored. No paid API credentials are used.
- **No external network at build time**; network only at runtime, behind flags.

## 7. Security & legal guardrails (mandatory)

1. **Authorized use only.** Operators must have a legitimate defensive remit
   (own brand, employer IR, sanctioned research). The tool's docs/help must
   state this.
2. **Read-only OSINT.** Only query public data sources and local resolution.
   No scanning of hosts, no exploitation, no paid/reputation services.
   All sources are free/freemium; APIs may require a key supplied
   via env/config.
3. **No phishing generation.** The fuzzer produces _candidate look-alikes for
   detection_. It must not produce ready-to-send lures, payloads, or message
   templates. LLM prompts must forbid generating attack content.
4. **Rate limiting & politeness.** Honor each source's ToS, rate, and quota
   limits (see §5.6). Provide conservative defaults and per-source
   concurrency caps.
5. **Data minimization.** Cache only what's needed for analysis; provide a
   `store purge` command. No PII beyond what public OSINT inherently contains.
6. **Provenance & false positives.** Every detection includes its evidence and
   source. The UI must clearly label low-confidence results as "suspicious,
   unverified" to avoid wrongful takedown claims.

## 8. How to work in this repo (for agents)

- **Architecture Decision Records (ADRs).** Major changes require an ADR under
  `docs/adr/`, named `NNNN-short-title.md` (zero-padded, e.g. `0001-...`) and
  following `docs/adr/template.md` (Status, Context, Decision, Consequences).
  A change is **major** when it:
  - creates a **new system/subsystem** (e.g. the throttle package, §5.6),
  - **implements a core interface** (e.g. adding a `Source`, §5.1),
  - **rewrites a core method** or changes the core loop / mini loop (§4), or
  - **swaps a foundational dependency** in the §2 stack table.
    Minor logic tweaks, refactors, and moving code around are **not** major and
    need no ADR. Keep ADRs lightweight and consistent with the YAGNI scope (§1):
    record only decisions that shape the architecture.
- Before adding a feature, check whether it fits one of the subsystems above
  (§5); place code in the matching `internal/` package.
- When adding a data source, implement the `Source` interface (this is a major
  change — file an ADR per §8), add config keys, document the source in `docs/`,
  and add a parsing test with a fixture.
- When touching the LLM layer, keep prompts in `prompts.go` and add a
  structured-output validation test.
- Prefer small, compilable increments. Run `go build ./...` and
  `go test ./...` before declaring a task done.
- If a requested change would violate Section 7, refuse that part and explain,
  then offer the compliant alternative.
