# Agents.md — GoPhish

Guidance for AI coding agents (and humans) working in this repository.

## 1. Scope & orientation

GoPhish is a **defensive OSINT tool** for detecting newly registered and
suspicious URLs used in smishing/phishing campaigns. See `README.md` for the
full product description and `docs/Plan.md` for architecture, pipeline, and
interfaces.

This file is about **how to implement** safely and consistently. Architecture
and "what to build" decisions live in `docs/Plan.md`; major decisions are
recorded as ADRs under `docs/adr/`.

Non-negotiable scope (these shape every change):

- **Defensive-only.** Never generate, host, or deliver phishing content;
  produce detections and analysis only.
- **YAGNI / minimal.** Build only what the core loop needs; defer extras until
  it works end-to-end.
- **Free/freemium sources only.** No paid reputation services.

## 2. Stack

The foundational stack is specified in `docs/Plan.md` §12 (Go, Bubble Tea,
Viper/Cobra, Ollama/llama.cpp, SQLite, zerolog, `golang.org/x/time/rate`).

Do **not** silently swap a foundational dependency. If a better-fit library is
clearly superior, propose it in a PR description (see §3).

## 3. Proposed directory layout

The directory layout is specified in `docs/Plan.md` §4.1. Place new code in the
matching `internal/` package for the subsystem it implements.

## 4. Core loop (the MVP)

The pipeline and the fuzz↔source-check mini loop are specified in
`docs/Plan.md` §4.3 and §4.8. The orchestrator (`internal/analyze`) **oversees
the progress of the services, schedules them, and logs events** (see
`docs/adr/0003-orchestrator-subsystem.md`).

## 5. Core subsystems

The `source`/`fuzz`/`detect`/`llm`/`throttle` subsystems and their interfaces
are specified in `docs/Plan.md` §4.2 and §13.

## 6. Conventions

- **Style**: `gofmt`/`goimports`; `go vet` and `golangci-lint` clean.
- **Errors**: wrap with context; don't swallow. Log at source, surface to UI.
- **Tests**: unit tests for `fuzz`, `detect`, `source` parsing; fixtures under
  `test/`. Network calls mocked via `httptest`.
  - **Test style — white-box first.** Write unit tests **in-package**
    (`package analyze` in `internal/analyze/engine_test.go`) so tests can reach
    unexported internals (state machines, helpers, unexported fields). This is
    the default for unit tests of package internals.
  - **Black-box for consumer-facing contracts.** When testing an interface
    consumed from outside the package — e.g. the orchestrator `Service`
    interface used by the TUI/CLI/API (Plan.md §4.2), or any public API/TUI
    surface — prefer an **external test package** (`package analyze_test`).
    This proves the exported API is sufficient for its consumers. Name these
    files with an `_XXX_test.go` suffix (e.g. `engine_api_test.go`,
    `engine_tui_test.go`) so the two styles are distinguishable at a glance.
    Both styles may coexist in one directory.
  - **File naming**: test files mirror the file under test (`engine.go` →
    `engine_test.go`, `job.go` → `job_test.go`). Test helpers, fakes, and
    deterministic doubles live in `_test.go` files too (e.g. `fakes_test.go`),
    **never** in regular `.go` files — helpers in a plain `.go` file get
    compiled into the production binary. Only extract shared helpers into a
    separate package (e.g. `internal/testutil`) once a second package actually
    needs them (YAGNI).
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
- **Dependencies**: do not silently swap foundational dependencies (see §2);
  propose changes in a PR description.

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
   limits (see Plan.md §4.6). Provide conservative defaults and per-source
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
  - creates a **new system/subsystem** (e.g. the orchestrator, ADR-0003),
  - **implements a core interface** (e.g. adding a `Source`, defined in
    Plan.md §4.2),
  - **rewrites a core method** or changes the core loop / mini loop
    (Plan.md §4.3/§4.8), or
  - **swaps a foundational dependency** in the stack (Plan.md §12).
    Minor logic tweaks, refactors, and moving code around are **not** major and
    need no ADR. Keep ADRs lightweight and consistent with the YAGNI scope (§1):
    record only decisions that shape the architecture.
- Before adding a feature, check whether it fits one of the subsystems in
  `docs/Plan.md` §13; place code in the matching `internal/` package.
- When adding a data source, implement the `Source` interface (Plan.md §4.2 —
  this is a major change, file an ADR), add config keys, document the source in
  `docs/`, and add a parsing test with a fixture.
- When touching the LLM layer, keep prompts in `prompts.go` and add a
  structured-output validation test.
- Prefer small, compilable increments. Run `go build ./...` and
  `go test ./...` before declaring a task done.
- If a requested change would violate Section 7, refuse that part and explain,
  then offer the compliant alternative.
