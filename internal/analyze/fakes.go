package analyze

import (
	"context"
	"strings"
	"sync/atomic"
)

// ---- Fake Source ----

// FakeSource is a deterministic Source for tests. It returns
// canned results or an error based on its configuration.
type FakeSource struct {
	NameVal        string
	ResultsToReturn []Result
	ShouldError    bool
	ErrorToReturn  error
	CallCount      atomic.Int32
	LastQuery      Query
}

func (f *FakeSource) Name() string              { return f.NameVal }
func (f *FakeSource) Fetch(ctx context.Context, q Query) ([]Result, error) {
	f.CallCount.Add(1)
	f.LastQuery = q
	if f.ShouldError {
		return nil, f.ErrorToReturn
	}
	return f.ResultsToReturn, nil
}

// ---- Fake Fuzzer ----

// FakeFuzzer is a deterministic Fuzzer for tests.
type FakeFuzzer struct {
	NameVal           string
	CandidatesToReturn []Candidate
	ShouldError       bool
	ErrorToReturn     error
	CallCount         atomic.Int32
	LastSeed          string
	LastMax           int
}

func (f *FakeFuzzer) Name() string {
	return f.NameVal
}

func (f *FakeFuzzer) Generate(ctx context.Context, seed string, max int) ([]Candidate, error) {
	f.CallCount.Add(1)
	f.LastSeed = seed
	f.LastMax = max
	if f.ShouldError {
		return nil, f.ErrorToReturn
	}
	return f.CandidatesToReturn, nil
}

// ---- Fake Scorer ----

// FakeScorer is a deterministic Scorer for tests.
type FakeScorer struct {
	FindingsToReturn []Finding
	ShouldError     bool
	ErrorToReturn   error
	CallCount       atomic.Int32
}

func (f *FakeScorer) Score(candidate Candidate, results []Result) (Finding, error) {
	f.CallCount.Add(1)
	if f.ShouldError {
		return Finding{}, f.ErrorToReturn
	}
	// Return a deterministic finding. If FindingsToReturn is populated,
	// use its first element; otherwise synthesize one from the candidate.
	if len(f.FindingsToReturn) > 0 {
		return f.FindingsToReturn[0], nil
	}
	return Finding{
		Domain:     candidate.Domain,
		Score:      0.5,
		Confidence: "medium",
		Label:      "suspicious, unverified",
		Reasons:    []string{"fuzzer-generated domain"},
	}, nil
}

// ---- Fake Clusterer ----

// FakeClusterer is a deterministic Clusterer for tests.
type FakeClusterer struct {
	ClustersToReturn []Cluster
	ShouldError     bool
	ErrorToReturn   error
	CallCount       atomic.Int32
}

func (f *FakeClusterer) Cluster(findings []Finding) ([]Cluster, error) {
	f.CallCount.Add(1)
	if f.ShouldError {
		return nil, f.ErrorToReturn
	}
	return f.ClustersToReturn, nil
}

// ---- Fake Summarizer ----

// FakeSummarizer is a deterministic Summarizer for tests.
type FakeSummarizer struct {
	NarrativeToReturn Narrative
	ShouldError      bool
	ErrorToReturn    error
	CallCount        atomic.Int32
}

func (f *FakeSummarizer) Summarize(ctx context.Context, findings []Finding) (Narrative, error) {
	f.CallCount.Add(1)
	if f.ShouldError {
		return Narrative{}, f.ErrorToReturn
	}
	return f.NarrativeToReturn, nil
}

// ---- Fake Logger (records calls) ----

// FakeLogger records log calls for assertion in tests.
type FakeLogger struct {
	Infos  []string
	Warns  []string
	Errors []string
}

func (f *FakeLogger) Info(msg string, _ ...any)  { f.Infos = append(f.Infos, msg) }
func (f *FakeLogger) Warn(msg string, _ ...any)  { f.Warns = append(f.Warns, msg) }
func (f *FakeLogger) Error(msg string, _ ...any) { f.Errors = append(f.Errors, msg) }

// MakeCandidates creates n deterministic candidates from a seed for tests.
func MakeCandidates(seed string, n int) []Candidate {
	base := strings.TrimPrefix(seed, ".")
	candidates := make([]Candidate, n)
	for i := 0; i < n; i++ {
		candidates[i] = Candidate{
			Domain:     base + string(rune('a'+i%26)) + ".com",
			SeedDomain: seed,
			FuzzType:   "tld-swap",
			Normalized: base + string(rune('a'+i%26)) + ".com",
		}
	}
	return candidates
}
