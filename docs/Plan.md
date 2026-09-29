# GoPhish Architecture Plan

> Good enough now, iterable later.
> Keep the surface small, the interfaces stable, and the pipeline reversible.
> Prefer boring Go over clever distributed systems.

This plan reconciles the original diagram with `AGENTS.md` (see §11 for the
decision log). Where it deviates from `AGENTS.md`, the deviation is flagged and,
when it is a "major change" per `AGENTS.md` §8, an ADR is referenced.

## 1. Outcome

This document defines the implementation roadmap and architecture so the next
contributors can ship the MVP fast without rewriting the system every time.

MVP goal: a single defensible loop that turns a seed domain into a ranked,
explained list of suspicious look-alikes:

```text
seed → sources → fuzz → detect → llm → results
```

## 2. Current state

The repo currently has:

- `cmd/gophish/main.go`
- `internal/config`
- `internal/tui` (minimal Bubble Tea app: `app.go`)
- `docs/adr`
- dependency surface: `bubbletea`, `lipgloss`, `viper`, `testify`

Most of the pipeline described in `AGENTS.md` is **not implemented yet**.

## 3. Challenges to the initial diagram

The diagram’s overall direction is sound, but several choices would create
avoidable complexity for a defensive OSINT CLI/TUI.

### 3.1 Two distinct communication boundaries

The diagram shows one “Service Layer → WebSocket/gRPC → Backend services”
boundary. Your clarification splits this into two separate boundaries, and they
must not be conflated:

- **Orchestrator ↔ microservices (source/fuzz/detect/llm)** — this is where
  **WebSocket or gRPC** belongs. The orchestrator connects to each backend
  service over a transport adapter. This matches the diagram’s intent.
- **Interaction services (TUI / API / SDK) ↔ orchestrator** — these do **not**
  use the WS/gRPC transport. They communicate with the orchestrator through a
  **shared interface contract** plus programmatic glue code. The WS/gRPC
  transport is internal to the orchestrator↔service boundary and is never
  exposed to clients.

So the earlier “don’t network-wrap the orchestrator” note is refined: keep
WS/gRPC *inside* the service boundary; keep clients on the `Service` interface.

**Evolution, not big-bang:** In the MVP the microservices run as **goroutines**
in a single binary, wired through in-process adapters behind the same
interfaces — no network transport yet. The WS/gRPC adapter is added later as the
boundary hardens, and the eventual target is to **extract each microservice into
its own container and orchestrate them with Kubernetes**, where the
orchestrator↔service calls become cross-pod RPC. The interfaces stay stable
across all three stages, so each step is a swap of the adapter, not a rewrite.

### 3.2 The Service Layer is split into explicit roles

A single box labeled “Service Layer” tends to absorb everything. We split it:

- **Service-transport adapters** — `internal/transport` (gRPC/WS) carry orchestrator ↔ microservice calls. Hidden from clients.
- **Interaction adapters** — `cmd/gophish` (TUI/CLI) today; `internal/api` later. They call the orchestrator through the `Service` interface + glue, not the WS/gRPC transport.
- **Orchestrator** — `internal/analyze`: *oversees the progress of the services, schedules them, and logs events*. This is the commander (see ADR-0003).
- **Config** — `internal/config`.
- **Services** — `source`, `fuzz`, `detect`, `llm`, each behind its own interface.

### 3.3 State needs to be explicit

The diagram omits durable state. If a job runs and the process restarts, you
need a way to recover or rerun cleanly.

- Phase 1: **in-memory job/run state**, with cancellation and event stream.
- Phase 2: **local cache** via SQLite when resume/reporting becomes useful.
- Don’t design the database schema until you have one working scan.

### 3.4 Seed → Analysis contract must be structured

Analysis consumes **structured `Result` values** from sources, not raw HTTP
responses or internal implementation details. The `Source` interface returns
`[]Result` (per `AGENTS.md` §5.1), which is the handoff contract.

### 3.5 LLM must be an adapter, not an implementation detail

Default backend is **Ollama HTTP** with a **llama.cpp** fallback (per
`AGENTS.md` §2/§5.4). Keep the backend swappable behind the `Summarizer`
interface so detection logic never depends on a provider.

### 3.6 Outbound access is a security boundary

Treat outbound network as a controlled boundary (see §8.1).

## 4. Architecture

### 4.1 Top-level layout

```text
cmd/
  gophish/
    main.go          # wires config + mode selection
    scan.go          # CLI scan command (Cobra)
    tui.go           # Bubble Tea entrypoint (wires internal/tui)
    api.go           # future HTTP/gRPC adapter, defer until needed

internal/
  analyze/           # ORCHESTRATOR: oversees services, schedules, logs events
    engine.go        # pipeline runner (seed→fuzz→check→mini loop→score→summarize)
    job.go           # job/run state model and state machine
    events.go        # typed progress events for TUI/CLI/API

  throttle/          # per-source rate + quota (Limiter per source, Wait(ctx))
    limiter.go

  source/            # OSINT data source integrations (one file per source)
    source.go        # Source interface + Query/Result models
    rdap.go
    certstream.go    # live CT stream (configurable "primary early signal")
    crtsh.go         # historical CT lookup
    whois.go         # fallback WHOIS
    phishtank.go     # known phishing feed

  fuzz/
    fuzz.go          # Fuzzer interface
    typosquat.go
    homoglyph.go
    tldswap.go
    permutations.go
    normalize.go     # unicode/punycode normalization

  detect/
    similarity.go    # Levenshtein/Jaccard/brand match
    bulkreg.go       # bulk-registration clustering
    score.go         # weighted risk score

  llm/
    client.go        # local LLM client: Ollama (default) + llama.cpp fallback
    prompts.go       # prompt templates + structured output validation

  store/             # optional cache, defer until after first working scan
    store.go
    sqlite.go

  tui/               # Bubble Tea models, views, styles (existing)
    app.go
    views/

  api/               # future programmatic client (shares Service interface)
    server.go

  config/
    config.go        # existing config loader

  transport/        # orchestrator <-> microservice adapters; MVP=goroutines, later gRPC/WS; deferred
    grpc.go
    ws.go
```

Notes on reconciliation with `AGENTS.md`:

- Orchestration uses **`internal/analyze`** (not `internal/orchestrate`) to match `AGENTS.md` §3/§4. The orchestrator role is described in ADR-0003.
- `throttle` is its **own package** (`internal/throttle`), matching `AGENTS.md` §5.6, not a file inside `source`.
- `detect` keeps `AGENTS.md` filenames: `similarity.go`, `bulkreg.go`, `score.go`.
- `internal/tui` is retained (existing code); `internal/api` is a future package.

### 4.2 Shared interfaces (the stable contract)

Two interface layers matter:

1. **Service interfaces** (data sources, fuzz, detect, llm) — behind the orchestrator.
2. **The consumer-facing `Service` interface** — used by TUI, CLI, and (future) API so they share the same logic and differ only in transport.

```go
// ---- Source interface (matches AGENTS.md §5.1) ----

// Query is the input to a source lookup.
type Query struct {
    Domain string
    Mode   string // e.g. "registration", "certificate"
}

// Provenance records where/when a result came from (AGENTS.md §5.1, §7.6).
type Provenance struct {
    Source    string
    Timestamp time.Time
    Query     Query
}

// Result is structured evidence for one domain from one source.
type Result struct {
    Source      string
    Domain      string
    Registered  bool
    Registrar   string
    Nameservers []string
    CreatedAt   *time.Time
    Provenance  Provenance
    Raw         map[string]any // source-specific structured fields only
}

type Source interface {
    Name() string
    Fetch(ctx context.Context, q Query) ([]Result, error)
}
```

```go
// ---- Fuzz / detect / llm service interfaces ----

// Candidate is a generated look-alike domain in normalized/punycode form.
type Candidate struct {
    Domain     string
    SeedDomain string
    FuzzType   string
    Normalized string
}

// Finding is a scored/classified result for a candidate.
type Finding struct {
    Domain      string
    Score       float64
    Confidence  string // "high" | "medium" | "low" (drives UI label, §8.3)
    Label       string // e.g. "suspicious, unverified" for low confidence
    Reasons     []string
    ClusterIDs  []string
    Results     []Result
}

// Narrative is the LLM-generated summary of a set of findings.
type Narrative struct {
    Summary string
    Campaign string
    IOCs    []string
    RawJSON string
    Model   string
}

// Cluster is a bulk-registration group produced by detect.
type Cluster struct {
    ID        string
    DomainIDs []string
    Reason    string
}

type Fuzzer interface {
    Name() string
    Generate(ctx context.Context, seed string, max int) ([]Candidate, error)
}

type Scorer interface {
    Score(candidate Candidate, results []Result) (Finding, error)
}

type Clusterer interface {
    Cluster(findings []Finding) ([]Cluster, error)
}

type Summarizer interface {
    Summarize(ctx context.Context, findings []Finding) (Narrative, error)
}
```

```go
// ---- Consumer-facing Service interface (shared by TUI / CLI / API) ----

// AnalysisRequest is what a client submits.
type AnalysisRequest struct {
    Seed  string
    Max   int // candidate cap (from config)
    Immediate bool // resolve/check now vs. flag-only
}

// Job is the run/state model (in-memory in Phase 1).
type Job struct {
    ID        string
    Seed      string
    State     string // see §4.4 state machine
    Stage     string
    Progress  JobProgress
    CreatedAt time.Time
    UpdatedAt time.Time
    Error     string
    Findings  []Finding
    Narrative *Narrative
}

type JobProgress struct {
    Fuzzed        int
    Checked       int
    Scored        int
    Clustered     int
    QuotaExhausted []string
}

// Service is the contract TUI, CLI, and API all consume.
type Service interface {
    RunAnalysis(ctx context.Context, req AnalysisRequest) (jobID string, err error)
    GetJob(ctx context.Context, jobID string) (Job, bool)
    Subscribe(ctx context.Context, jobID string) (<-chan Event, error)
    Cancel(ctx context.Context, jobID string) error
}
```

The `Store` interface (Phase 2) mirrors `Service` persistence:

```go
type Store interface {
    SaveRun(ctx context.Context, run Job) error
    LoadRun(ctx context.Context, id string) (Job, bool)
}
```

Two call styles use these interfaces:

1. **Orchestrator → service**: calls travel over a transport adapter
   (`internal/transport`, WS/gRPC target). In MVP we start with an in-process
   adapter behind the same interface; the WS/gRPC adapter is the intended
   production transport.
2. **Interaction → orchestrator**: TUI/API/SDK call the `Service` interface
   via glue code. They never see the WS/gRPC transport.

Keep interfaces small. Use dependency injection in `cmd/gophish` only.

### 4.3 Orchestrator responsibility

The orchestrator (`internal/analyze`) is the commander. Per ADR-0003 it
**oversees the progress of the services, schedules them, and logs events**. It:

- accepts an `AnalysisRequest` via the `Service` interface
- creates a `Job`
- runs the pipeline in this order (reconciled with `AGENTS.md` §4):

  ```text
  seed (check) → fuzz → check → back to seed (per config) → exit
  ```

  1. **seed check** — look up the seed domain in sources
  2. **fuzz** — generate candidates from the seed
  3. **check** — source-check candidates (through throttle)
  4. **mini loop** — expand around confirmed hits (see §4.8), looping back to fuzz/check per config
  5. **score + cluster** — deterministic detection
  6. **summarize** — optional LLM narrative
- emits progress `Event`s and logs them (zerolog)
- updates job state
- honors cancellation at the job level

It does **not** render UI and it does **not** own transport.

### 4.4 Job state machine

Jobs are asynchronous because network work is slow. TUI/CLI subscribe to job
events and can reconnect to current job state.

```text
CREATED
  → SEEDING        (source lookup on the seed domain)
  → FUZZING        (generate candidates)
  → CHECKING       (source-check candidates through throttle)
  ⇄ MINI-LOOP      (CHECKING ⇄ FUZZING; optionally re-seed, per config)
  → SCORING        (score + cluster)
  → SUMMARIZING    (LLM narrative)
  → COMPLETED
  → FAILED
  → CANCELLED
```

State transitions:

| From | To | Meaning |
| --- | --- | --- |
| CREATED | SEEDING | starting source lookup on seed |
| SEEDING | FUZZING | seed observations ready |
| SEEDING | FAILED | unrecoverable seed lookup failure |
| FUZZING | CHECKING | candidates generated |
| CHECKING | FUZZING | mini loop: expand around confirmed hits |
| CHECKING | SEEDING | mini loop: re-seed from confirmed cluster (per config) |
| CHECKING | SCORING | checks complete or quota exhausted for all sources |
| SCORING | SUMMARIZING | deterministic scoring done |
| SUMMARIZING | COMPLETED | LLM summary ready |
| ANY | CANCELLED | user cancelled |
| ANY | FAILED | fatal error |

The `CHECKING ⇄ FUZZING` (and optional `CHECKING → SEEDING`) edges are the
**mini loop** from `AGENTS.md` §4.1.

### 4.5 Event/progress model

Use a typed event stream instead of callbacks. The orchestrator logs each event (zerolog) and forwards it to subscribers.

```go
type Event struct {
    JobID   string
    Type    string
    At      time.Time
    Payload any
}

const (
    EventJobCreated     = "job_created"
    EventStageChanged   = "stage_changed"
    EventFuzzed         = "fuzzed"
    EventSourceChecked  = "source_checked"
    EventScored         = "scored"
    EventSummarized     = "summarized"
    EventQuotaExhausted = "quota_exhausted"
    EventDone           = "done"
    EventError          = "error"
)
```

TUI renders events as live updates. CLI prints a condensed timeline. Future API
can expose events over WebSocket/gRPC streaming.

### 4.6 Throttle placement

Per `AGENTS.md` §5.6, throttle is a **dedicated `internal/throttle` package**
with one `Limiter` per source combining a `rate.Limiter` and a quota counter.
**Sources call `Wait(ctx)` before each request.**

```text
orchestrator → source.Fetch → throttle.Limiter.Wait(ctx) → real request
```

Throttle enforces:

- rate limit via `golang.org/x/time/rate`
- quota ceiling with rolling window reset
- graceful `quota_exhausted` event instead of fatal error

The mini loop (§4.8) honors the throttle: when a source’s quota is exhausted,
that source stops being checked and `quota_exhausted` is logged; the loop
continues with remaining sources.

### 4.7 Storage policy

Phase 1: **no persistent store**.
Phase 2: add `store` only when you need to cache results or resume runs.
Phase 3: add `store purge` command (`AGENTS.md` §7.5).

Avoid premature schema design. Start with plain structs, not generated protobuf.

### 4.8 Mini loop (fuzz ↔ source-check)

This implements `AGENTS.md` §4.1, the core bulk-registration-discovery signal.

- The mini loop lives **at the edge of the services** (between fuzz and
  source-check), not inside a single source.
- It **logs info** on each iteration (via the orchestrator’s event/log path).
- **Config determines the stipulations** of how much looping and throttling
  occurs: max iterations, per-seed candidate limit, same-registrar/expansion
  rules, and which source is the primary early signal.
- **Once started, it should not stop until done**: it runs to completion per
  its config bounds (no new same-registrar domains surface, or iteration cap
  reached). Cancellation applies at the job level, not mid-iteration.
- On each iteration it expands around confirmed hits (same registrar, sibling
  TLDs, templated name patterns) and re-checks them.

```text
fuzz ──► candidate
  │
  ▼
source check ──► registered? by whom? (registrar, nameservers, created)
  │
  ▼
fuzz again ──► expand around confirmed hits (config-bound)
  │
  ▼
source check again ──► until no new same-registrar domains or cap reached
```

### 4.9 Communication boundaries

Two boundaries, deliberately different:

| Boundary | Transport (MVP → target) | Notes |
| --- | --- | --- |
| Orchestrator ↔ microservices (source/fuzz/detect/llm) | **goroutines (MVP)** → **WebSocket/gRPC** → **containers orchestrated by Kubernetes** | Internal; not exposed to clients. The `Source`/`Fuzzer`/`Scorer`/`Clusterer`/`Summarizer` interfaces are the contract the adapters implement; each stage swaps the adapter, not the logic. |
| Interaction services (TUI/API/SDK) ↔ orchestrator | **Interface contract + glue code** (`Service` interface) | No WS/gRPC to clients. TUI and API share logic, differ only in presentation/transport. |

In the MVP the microservices run as **goroutines** in one binary, wired through
in-process adapters — no network transport. The WS/gRPC adapter is added later
as the boundary hardens, and the eventual target is to **extract each
microservice into its own container and orchestrate them with Kubernetes**, where
orchestrator↔service calls become cross-pod RPC. This keeps the “good enough,
iterate” principle while honoring your target topology.

## 5. Implementation order

Do this in vertical slices. Each slice should be runnable end-to-end.

1. **ADR + interface contract**
   - file **ADR-0003** (orchestrator subsystem)
   - define `Query`, `Result`, `Candidate`, `Finding`, `Job`, `Event`, `Service`
   - add unit tests with fake implementations

2. **One working source + one fuzzer** (config-driven primary source)
   - implement the **config-selected primary early signal** source first
     (RDAP or CertStream by default; any source can be primary per config)
   - implement `tldswap.go`
   - wire a CLI `scan` command that prints findings
   - proves end-to-end without UI complexity

3. **Throttle + quota**
   - add `internal/throttle` with `Limiter.Wait(ctx)`
   - tests with short windows and deterministic clocks

4. **Scoring + clustering**
   - add `detect/similarity.go`, `detect/bulkreg.go`, `detect/score.go`
   - no LLM yet

5. **Job/event model + mini loop**
   - add `analyze/job.go`, `analyze/events.go`, `analyze/engine.go`
   - implement the `CHECKING ⇄ FUZZING` loop with config bounds
   - wire CLI output to events

6. **CLI packaging**
   - `scan` command with stage/progress output
   - config-driven source enablement + primary-source selection

7. **TUI wiring**
   - `internal/tui` calls the `Service` interface via `tea.Cmd`
   - views per `AGENTS.md` §5.5: Search/Seed, Results list, Domain detail,
     Clusters/campaigns, LLM analysis, Settings
   - live event list + current stage + result table

8. **LLM**
   - Ollama client (default) + llama.cpp fallback
   - summarize only after #1-7 are stable

9. **Store / cache / API**
   - defer until UI and CLI feel solid
   - `internal/api` later, reusing the `Service` interface

10. **Service-transport adapter + containerization (WS/gRPC → k8s)**
   - add `internal/transport` (gRPC/WS) implementing the service interfaces
   - only after the in-process (goroutine) pipeline (slices 1–8) is proven
   - later: extract each microservice into its own container, orchestrate with
     Kubernetes; orchestrator↔service calls become cross-pod RPC
   - clients (TUI/API/SDK) stay on the `Service` interface; this does not
     change their code

## 6. Constraints

- **Go 1.22+** (per `AGENTS.md` §2; repo `go.mod` is 1.25.0).
- **Logging**: `zerolog` (per `AGENTS.md` §2).
- **No paid APIs.**
- **No scanning/external exploitation.** Only public OSINT + local checks.
- **Defensive only.** Fuzzer outputs candidates for detection, never lures.
- **No HTTP API in MVP.**
- **No database before the pipeline works once end-to-end.**
- **No network transport between CLI/TUI and orchestrator in MVP.**

## 7. Boundaries

Allowed:

- `internal/` packages listed above
- `cmd/gophish`
- `docs/` updates

Avoid:

- generated code unless generated by local tooling
- external services requiring paid tiers
- secrets in config besides `api_key_env` references

## 8. Security boundaries

Treat these as hard constraints, not nice-to-haves.

### 8.1 Outbound network policy

The tool fetches the Internet and generates candidate domains. Model outbound
requests with explicit controls:

- allowed schemes: `http`, `https`
- block private/reserved IP ranges unless explicitly allowed
- follow redirects with scheme/host validation
- response body size limit
- per-request timeout
- per-source concurrency cap (honored by throttle)

### 8.2 Fuzzer output is untrusted input

Generated domains are **inputs to external lookups**, not trusted data.
Validation, normalization, and throttling apply to fuzzer output like user input.

### 8.3 Provenance & false positives

- Every `Result` carries `Provenance` (source, timestamp, query) per
  `AGENTS.md` §5.1/§7.6.
- `Finding.Confidence`/`Label` drive UI labeling. Low-confidence results must
  be shown as **“suspicious, unverified”** (`AGENTS.md` §7.6) to avoid wrongful
  takedown claims.

### 8.4 LLM output is not trusted

LLM summaries are displayed, not executed. Validate structured JSON before use.
Never send secrets, cookies, or raw credentials to the model. Only public OSINT
attributes. Ollama is default; llama.cpp is a fallback backend.

## 9. Iteration policy

After each slice:

1. run `go build ./...`
2. run `go test ./...`
3. verify a single end-to-end scenario still works
4. if a slice blocks, reduce scope, do not broaden architecture

## 10. Blocked stop condition

If any slice is blocked because:

- external source behavior is undocumented/unstable,
- Ollama/LLM backend is unavailable,
- security/legal scope is unclear, or
- the chosen slice requires transport/database decisions that are out of scope,

then stop and write a short report with:
- what was attempted,
- exact blocker evidence,
- the minimum input needed to continue,
- a proposed narrower next slice that avoids the blocker.

Do not invent fallback behavior that weakens the scope.

## 11. Reconciliation log (Plan vs AGENTS.md)

| # | Topic | Resolution |
| --- | --- | --- |
| 1 | Orchestration package | Use `internal/analyze`; orchestrator role defined in ADR-0003. |
| 2 | Mini loop | Added §4.8 + cyclic `CHECKING ⇄ FUZZING` state edge. |
| 3 | Source interface | Keep `AGENTS.md` §5.1 signature `Fetch(ctx, Query) ([]Result, error)`; add consumer-facing `Service` interface for TUI/API. |
| 4 | Pipeline order | `seed (check) → fuzz → check → back to seed (per config) → exit`. |
| 5 | Throttle | `internal/throttle` package; `Limiter.Wait(ctx)` per `AGENTS.md` §5.6. |
| 6 | CLI/TUI/API | Commands in `cmd/gophish`; `internal/tui` retained; future `internal/api` shares `Service` interface. |
| 7 | `internal/tui` | Retained in layout (existing code). |
| 8 | detect filenames | `similarity.go`, `bulkreg.go`, `score.go` per `AGENTS.md`. |
| 9 | CertStream/primary source | Config-driven: any source can be the primary early signal. |
| L1 | Logging | `zerolog` added to constraints. |
| L2 | Provenance | `Result.Provenance` added. |
| L3 | False-positive label | `Finding.Confidence`/`Label` + UI labeling rule. |
| L4 | LLM fallback | Ollama default + llama.cpp fallback noted. |
| L5 | Go version | Pinned 1.22+ in constraints. |
| L6 | TUI views | Listed 6 views from `Plan.md` §5.7. |
| 10 | Transport boundaries | Orchestrator↔services use WS/gRPC (`internal/transport`); interaction↔orchestrator use `Service` interface + glue, no WS/gRPC to clients. |

## 12. Stack

Foundational dependencies (consolidated from the former `AGENTS.md` §2). Do
not silently swap these; propose changes in a PR per `AGENTS.md` §2/§6.

| Concern         | Choice                                                | Notes                                                        |
| --------------- | ----------------------------------------------------- | ------------------------------------------------------------ |
| Language        | **Go** (1.22+)                                        | Strong for CLIs, concurrency, single binaries.              |
| TUI             | **Bubble Tea** + Lipgloss + Bubbles                   | Idiomatic Go TUI framework.                                  |
| Data fetching   | stdlib `net/http` + `golang.org/x/time/rate`          | Respect rate limits per source.                             |
| Config          | `Viper` + `Cobra`                                     | CLI flags/subcommands + config file.                        |
| Local LLM       | `llama.cpp` (or `ollama` HTTP); default **Ollama**    | Pluggable backend; default to Ollama for portability.        |
| Storage (local) | SQLite (`modernc.org/sqlite`)                          | Zero-dependency local cache of seen domains/results.        |
| Logging         | `zerolog`                                              | Structured, leveled.                                         |

## 13. Subsystem guidance (condensed from former AGENTS.md §5)

- **Sources** (`internal/source`): prefer **RDAP** over legacy WHOIS
  (structured, no scraping, rate-friendly). **Certificate Transparency**
  (crt.sh + CertStream) is a primary early signal — made configurable as the
  primary source per §4.1/§4.8. Only free/freemium sources; record provenance
  on every `Result` (§4.2).
- **Fuzzing** (`internal/fuzz`): typosquat, homoglyph/punycode, TLD swap,
  permutations. Cap combinatorial explosion: dedupe, normalize to punycode,
  bound output (`--max`). Flag generated domains; do **not** auto-query them
  by default — offer resolve/check as an explicit action.
- **Detection** (`internal/detect`): similarity via Levenshtein/Damerau,
  Jaccard on tokens, brand-substring match; bulk-registration clustering by
  registrar, creation-window, nameservers, and templated name patterns;
  weighted risk score from free OSINT signals only (no external reputation
  lookups).
- **Local LLM** (`internal/llm`): default **Ollama** HTTP with **llama.cpp**
  fallback; summarize clusters into a campaign narrative, explain *why* a
  domain is risky, propose related IOCs; prompts in `prompts.go`; require and
  validate structured (JSON) output; never send secrets/cookies/credentials —
  only public OSINT attributes.
- **Throttle** (`internal/throttle`): per-source `Limiter` with both rate
  (RPM) and quota (max checks / rolling window); sources call `Wait(ctx)`;
  surface `quota_exhausted` rather than hammering (§4.6).

## 14. Config reference

Per-source keys (from the former `README.md` table):

| Key                  | Purpose                                              |
| -------------------- | ---------------------------------------------------- |
| `base_url`           | Endpoint used by the source client.                  |
| `api_key`            | Optional credential (prefer `api_key_env`).          |
| `api_key_env`        | Env var that, if set, overrides `api_key`.           |
| `enabled`            | Toggle the source on/off.                            |
| `requests_per_minute`| Per-source rate limit.                              |
| `max_checks`        | Hard quota ceiling for this source.                  |
| `quota_window`       | Duration after which the quota counter resets.      |

Global knobs: fuzzing cap (`--max`), LLM model/endpoint, and the mini-loop
bounds (max iterations, per-seed candidate limit, expansion rules) are also
config-driven (see §4.8). See `config.example.toml`.
