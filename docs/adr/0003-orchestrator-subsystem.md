# ADR-0003: Orchestrator Subsystem (`internal/analyze`)

- Status: Proposed
- Date: 2026-08-27

## Context
GoPhish needs a single commander that drives the core loop (`AGENTS.md` §4):
seed → sources → fuzz → detect → llm. The original diagram implied a vague
“Service Layer” box. `AGENTS.md` §3/§4 already names `internal/analyze` as the
orchestration home, and §8 treats a **new subsystem** as a major change
requiring an ADR.

Open questions the orchestrator must answer: how services are scheduled, how
progress/state is tracked for TUI/CLI/API consumers, how events are logged, and
where the fuzz↔source-check mini loop (§4.1) lives.

## Decision
We introduce the **orchestrator** as a subsystem in `internal/analyze` with the
explicit role:

> The orchestrator **oversees the progress of the services, schedules them, and
> logs events.**

Concretely:

- `internal/analyze` owns the pipeline runner (`engine.go`), the job/run state
  model and state machine (`job.go`), and the typed event stream (`events.go`).
- It exposes a **consumer-facing `Service` interface** (`RunAnalysis`,
  `GetJob`, `Subscribe`, `Cancel`) so TUI, CLI, and a future API share the same
  logic and differ only in transport.
- It runs the pipeline in the order: `seed (check) → fuzz → check → back to
  seed (per config) → exit`, including the iterative mini loop (`AGENTS.md`
  §4.1) at the edge of the services.
- It schedules service work behind cancellable contexts and logs each progress
  event (zerolog).
- Data-source/fuzz/detect/llm services remain behind their own interfaces
  (`Source`, `Fuzzer`, `Scorer`, `Clusterer`, `Summarizer`); the orchestrator
  does not render UI or own transport.

This is a major change per `AGENTS.md` §8 (new subsystem + core interface), so
this ADR records it.

## Consequences
**Easier:**
- One stable place for pipeline ordering, scheduling, and progress tracking.
- TUI/CLI/API stay thin and consistent via the shared `Service` interface.
- The mini loop is explicit and config-bounded, not buried in a source.

**Harder:**
- The orchestrator must stay narrow; risk of becoming a god object if business
  logic leaks in. Mitigated by keeping services behind interfaces and injecting
  them in `cmd/gophish`.

**Consistency Check:**
- **YAGNI Scope (§1):** No transport/API/database is added in MVP; store and
  `internal/api` are explicitly deferred.
- **Security Guardrails (§7):** Orchestrator honors throttle/quota, treats
  fuzzer output as untrusted input, and logs provenance; it does not execute
  LLM output.
