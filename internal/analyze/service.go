// Package analyze implements the GoPhish orchestrator (ADR-0003).
//
// The orchestrator is the commander: it oversees the progress of the
// services, schedules them, and logs events. It exposes a consumer-facing
// Service interface so TUI, CLI, and a future API share the same logic
// and differ only in transport.
//
// MVP transport: in-process goroutines behind the interfaces. The
// WS/gRPC adapter is deferred to Slice 10 (internal/transport).
package analyze

import (
	"context"
	"time"
)

// ---- Source interface (matches Plan.md §4.2 / AGENTS.md §5.1) ----

// Query is the input to a source lookup.
type Query struct {
	Domain string
	Mode   string // e.g. "registration", "certificate"
}

// Provenance records where/when a result came from.
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

// Source is the contract for an OSINT data source.
type Source interface {
	Name() string
	Fetch(ctx context.Context, q Query) ([]Result, error)
}

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
	Domain     string
	Score      float64
	Confidence string   // "high" | "medium" | "low" (drives UI label, §8.3)
	Label      string   // e.g. "suspicious, unverified" for low confidence
	Reasons    []string
	ClusterIDs []string
	Results    []Result
}

// Narrative is the LLM-generated summary of a set of findings.
type Narrative struct {
	Summary  string
	Campaign string
	IOCs     []string
	RawJSON  string
	Model    string
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

// ---- Consumer-facing Service interface (shared by TUI / CLI / API) ----

// AnalysisRequest is what a client submits.
type AnalysisRequest struct {
	Seed      string
	Max       int  // candidate cap (from config)
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

// JobProgress tracks pipeline throughput.
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

// Note: the concrete *Engine type lives in engine.go and implements
// the Service interface declared above.
