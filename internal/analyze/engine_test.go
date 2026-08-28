package analyze

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func newTestEngine(t *testing.T) (*Engine, *FakeSource, *FakeFuzzer, *FakeScorer, *FakeClusterer, *FakeSummarizer) {
	t.Helper()
	src := &FakeSource{NameVal: "fake-rdap", ResultsToReturn: []Result{{Domain: "example.com", Registered: true, Source: "fake-rdap"}}}
	fuzz := &FakeFuzzer{NameVal: "fake-fuzz", CandidatesToReturn: MakeCandidates("example.com", 3)}
	score := &FakeScorer{}
	clust := &FakeClusterer{ClustersToReturn: []Cluster{{ID: "c1", DomainIDs: []string{"d1"}}}}
	summ := &FakeSummarizer{NarrativeToReturn: Narrative{Summary: "test summary"}}
	engine := NewEngine(src, fuzz, score, clust, summ, MiniLoopConfig{MaxIterations: 0}, zap.NewNop())
	return engine, src, fuzz, score, clust, summ
}

func TestEngineRunAnalysisCompletes(t *testing.T) {
	engine, _, _, _, _, _ := newTestEngine(t)

	ctx := context.Background()
	jobID, err := engine.RunAnalysis(ctx, AnalysisRequest{Seed: "example.com", Max: 10})
	require.NoError(t, err)
	require.NotEmpty(t, jobID)

	waitForTerminal(t, engine, jobID, 5*time.Second)

	job, ok := engine.GetJob(ctx, jobID)
	require.True(t, ok, "job should exist")
	assert.Equal(t, StateCompleted, job.State)
	assert.Equal(t, "example.com", job.Seed)
}

func TestEngineRunAnalysisCancelled(t *testing.T) {
	engine, _, _, _, _, _ := newTestEngine(t)

	// Use a source that blocks until the context is cancelled.
	engine.source = &blockingFakeSource{results: []Result{{Domain: "example.com", Registered: true}}}

	ctx, cancel := context.WithCancel(context.Background())
	jobID, err := engine.RunAnalysis(ctx, AnalysisRequest{Seed: "example.com", Max: 10})
	require.NoError(t, err)

	// Cancel before the pipeline reaches a terminal state.
	cancel()

	// The pipeline checks ctx.Done() after each stage; with a blocking
	// source it will observe the cancellation during seed-check.
	// Poll until we see CANCELLED or a reasonable timeout.
	deadline := time.Now().Add(3 * time.Second)
	var job Job
	var ok bool
	for time.Now().Before(deadline) {
		job, ok = engine.GetJob(ctx, jobID)
		if ok && job.State == StateCancelled {
			return // success
		}
		time.Sleep(10 * time.Millisecond)
	}
	if ok {
		t.Fatalf("job in state %s, want CANCELLED", job.State)
	}
	t.Fatal("job not found")
}

// blockingFakeSource blocks on ctx.Done() until cancelled, then returns
// an empty result set. This lets us verify cancellation propagates through
// the pipeline without needing a slow network mock.
type blockingFakeSource struct {
	results []Result
}

func (f *blockingFakeSource) Name() string { return "blocking" }

func (f *blockingFakeSource) Fetch(ctx context.Context, q Query) ([]Result, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	// Block until cancelled.
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestEngineSubscribeAndReceiveEvents(t *testing.T) {
	engine, _, _, _, _, _ := newTestEngine(t)

	ctx := context.Background()
	jobID, err := engine.RunAnalysis(ctx, AnalysisRequest{Seed: "example.com", Max: 1})
	require.NoError(t, err)

	ch, err := engine.Subscribe(ctx, jobID)
	require.NoError(t, err)

	var types []string
	deadline := time.After(5 * time.Second)
	for {
		select {
		case ev := <-ch:
			types = append(types, string(ev.Type))
			if ev.Type == EventDone || ev.Type == EventError {
				goto done
			}
		case <-deadline:
			t.Fatal("timed out waiting for events")
		}
	}
done:
	assert.Contains(t, types, EventJobCreated)
	assert.Contains(t, types, EventDone)
	assert.Contains(t, types, EventStageChanged)
}

func TestEngineSubscribeUnknownJob(t *testing.T) {
	engine, _, _, _, _, _ := newTestEngine(t)
	_, err := engine.Subscribe(context.Background(), "nonexistent")
	assert.Error(t, err)
}

func TestEngineGetJobMissing(t *testing.T) {
	engine, _, _, _, _, _ := newTestEngine(t)
	job, ok := engine.GetJob(context.Background(), "nonexistent")
	require.False(t, ok)
	assert.Empty(t, job.ID)
}

func TestEngineCancelMissingJob(t *testing.T) {
	engine, _, _, _, _, _ := newTestEngine(t)
	err := engine.Cancel(context.Background(), "nonexistent")
	assert.Error(t, err)
}

func TestEnginePipelineEmitsAllEventTypes(t *testing.T) {
	engine, _, _, _, _, _ := newTestEngine(t)

	ctx := context.Background()
	jobID, err := engine.RunAnalysis(ctx, AnalysisRequest{Seed: "example.com", Max: 2})
	require.NoError(t, err)

	ch, err := engine.Subscribe(ctx, jobID)
	require.NoError(t, err)

	seen := make(map[string]bool)
	deadline := time.After(5 * time.Second)
	for {
		select {
		case ev := <-ch:
			seen[string(ev.Type)] = true
			if ev.Type == EventDone || ev.Type == EventError {
				goto done
			}
		case <-deadline:
			t.Fatal("timed out waiting for events")
		}
	}
done:
	for _, want := range []string{EventJobCreated, EventStageChanged, EventDone} {
		assert.Contains(t, seen, want, "event %s should be emitted", want)
	}
}

func TestEngineFindingsAndNarrative(t *testing.T) {
	engine, _, _, _, _, _ := newTestEngine(t)

	ctx := context.Background()
	jobID, err := engine.RunAnalysis(ctx, AnalysisRequest{Seed: "example.com", Max: 1})
	require.NoError(t, err)

	waitForTerminal(t, engine, jobID, 5*time.Second)

	job, ok := engine.GetJob(ctx, jobID)
	require.True(t, ok)
	assert.Equal(t, StateCompleted, job.State)
	assert.GreaterOrEqual(t, len(job.Findings), 1)
	require.NotNil(t, job.Narrative)
	assert.Equal(t, "test summary", job.Narrative.Summary)
	assert.GreaterOrEqual(t, job.Progress.Clustered, 1)
}

func TestEngineMiniLoopTransitions(t *testing.T) {
	engine, _, _, _, _, _ := newTestEngine(t)
	engine.loop = MiniLoopConfig{MaxIterations: 2}

	ctx := context.Background()
	jobID, err := engine.RunAnalysis(ctx, AnalysisRequest{Seed: "example.com", Max: 2})
	require.NoError(t, err)

	// Subscribe before the job finishes so we receive all events.
	ch, err := engine.Subscribe(ctx, jobID)
	require.NoError(t, err)

	var fuzzCount int
	deadline := time.After(5 * time.Second)
	for {
		select {
		case ev := <-ch:
			if ev.Type == EventFuzzed {
				fuzzCount++
			}
			if ev.Type == EventDone || ev.Type == EventError {
				goto done
			}
		case <-deadline:
			t.Fatal("timed out waiting for events")
		}
	}
done:
	// MaxIterations=2 means at least 2 mini-loop fuzz cycles.
	assert.GreaterOrEqual(t, fuzzCount, 2)

	// Verify the job is in COMPLETED state.
	job, ok := engine.GetJob(ctx, jobID)
	if ok {
		assert.Equal(t, StateCompleted, job.State)
	}
}

func TestEngineSeedFailureTransitionsToFailed(t *testing.T) {
	engine, _, _, _, _, _ := newTestEngine(t)
	engine.source = &FakeSource{
		NameVal:       "bad-source",
		ShouldError:   true,
		ErrorToReturn: assert.AnError,
	}

	ctx := context.Background()
	jobID, err := engine.RunAnalysis(ctx, AnalysisRequest{Seed: "example.com", Max: 1})
	require.NoError(t, err)

	waitForTerminal(t, engine, jobID, 5*time.Second)

	job, ok := engine.GetJob(ctx, jobID)
	require.True(t, ok)
	assert.Equal(t, StateFailed, job.State)
	assert.Contains(t, job.Error, "seed lookup")
}

func TestEngineFuzzFailureTransitionsToFailed(t *testing.T) {
	engine, _, fuzz, _, _, _ := newTestEngine(t)
	fuzz.ShouldError = true
	fuzz.ErrorToReturn = assert.AnError

	ctx := context.Background()
	jobID, err := engine.RunAnalysis(ctx, AnalysisRequest{Seed: "example.com", Max: 1})
	require.NoError(t, err)

	waitForTerminal(t, engine, jobID, 5*time.Second)

	job, ok := engine.GetJob(ctx, jobID)
	require.True(t, ok)
	assert.Equal(t, StateFailed, job.State)
	assert.Contains(t, job.Error, "fuzz")
}

func TestEngineQuotaExhaustedRecorded(t *testing.T) {
	engine, _, _, _, _, _ := newTestEngine(t)
	// Scorer error triggers quota_exhausted for the scorer.
	engine.scorer = &FakeScorer{ShouldError: true, ErrorToReturn: assert.AnError}

	ctx := context.Background()
	jobID, err := engine.RunAnalysis(ctx, AnalysisRequest{Seed: "example.com", Max: 1})
	require.NoError(t, err)

	waitForTerminal(t, engine, jobID, 5*time.Second)

	job, ok := engine.GetJob(ctx, jobID)
	require.True(t, ok)
	assert.Contains(t, job.Progress.QuotaExhausted, "scorer")
}

func TestStateMachineTransitions(t *testing.T) {
	js := &jobState{
		Job: Job{
			ID:        "test-1",
			Seed:      "example.com",
			State:     StateCreated,
			Stage:     "created",
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		},
	}

	require.NoError(t, js.transition(StateSeeding, "seed-check"))
	assert.Equal(t, StateSeeding, js.Job.State)

	require.NoError(t, js.transition(StateFuzzing, "fuzz"))
	assert.Equal(t, StateFuzzing, js.Job.State)

	require.NoError(t, js.transition(StateChecking, "check"))
	assert.Equal(t, StateChecking, js.Job.State)

	// Mini loop: CHECKING -> FUZZING -> CHECKING.
	require.NoError(t, js.transition(StateFuzzing, "fuzz-loop"))
	assert.Equal(t, StateFuzzing, js.Job.State)

	require.NoError(t, js.transition(StateChecking, "check-loop"))
	assert.Equal(t, StateChecking, js.Job.State)

	require.NoError(t, js.transition(StateScoring, "score"))
	assert.Equal(t, StateScoring, js.Job.State)

	require.NoError(t, js.transition(StateSummarizing, "summarize"))
	assert.Equal(t, StateSummarizing, js.Job.State)

	require.NoError(t, js.transition(StateCompleted, "done"))
	assert.Equal(t, StateCompleted, js.Job.State)

	err := js.transition(StateFuzzing, "reopen")
	assert.Error(t, err)
}

func TestStateMachineIllegalTransition(t *testing.T) {
	js := &jobState{Job: Job{ID: "test-2", State: StateCreated}}
	err := js.transition(StateChecking, "skip")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "illegal transition")
}

func TestStateMachineFailFromAny(t *testing.T) {
	js := &jobState{Job: Job{ID: "test-3", State: StateChecking}}
	js.fail(assert.AnError)
	assert.Equal(t, StateFailed, js.Job.State)
	assert.NotEmpty(t, js.Job.Error)
}

func TestStateMachineCancelFromAny(t *testing.T) {
	js := &jobState{Job: Job{ID: "test-4", State: StateFuzzing}}
	js.cancelJob()
	assert.Equal(t, StateCancelled, js.Job.State)
}

func TestStateMachineAlreadyTerminal(t *testing.T) {
	js := &jobState{Job: Job{ID: "test-5", State: StateCompleted}}
	err := js.transition(StateFuzzing, "reopen")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "already in terminal state")
}

func TestJobSnapshotIsIsolated(t *testing.T) {
	js := &jobState{
		Job: Job{
			ID:       "snap",
			State:    StateCompleted,
			Findings: []Finding{{Domain: "a.com"}},
			Progress: JobProgress{Fuzzed: 5},
		},
	}
	snap := js.snapshot()
	snap.Findings = append(snap.Findings, Finding{Domain: "b.com"})
	snap.Progress.Fuzzed = 999

	js.mu.Lock()
	assert.Len(t, js.Job.Findings, 1)
	assert.Equal(t, 5, js.Job.Progress.Fuzzed)
	js.mu.Unlock()
}

func TestEventBrokerRegisterAndGet(t *testing.T) {
	b := NewEventBroker()
	js := &jobState{Job: Job{ID: "broker-1"}}
	require.NoError(t, b.Register("broker-1", js))
	assert.Error(t, b.Register("broker-1", js))

	job, ok := b.Get("broker-1")
	assert.True(t, ok)
	assert.Equal(t, "broker-1", job.ID)

	_, ok = b.Get("nonexistent")
	assert.False(t, ok)
}

func TestEventBrokerSubscribe(t *testing.T) {
	engine, _, _, _, _, _ := newTestEngine(t)

	ctx := context.Background()
	jobID, err := engine.RunAnalysis(ctx, AnalysisRequest{Seed: "example.com", Max: 1})
	require.NoError(t, err)

	ch, err := engine.Subscribe(ctx, jobID)
	require.NoError(t, err)
	require.NotNil(t, ch)

	select {
	case ev := <-ch:
		assert.Equal(t, jobID, ev.JobID)
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for event on channel")
	}
}

func waitForTerminal(t *testing.T, engine *Engine, jobID string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		job, ok := engine.GetJob(context.Background(), jobID)
		if !ok {
			return
		}
		if isTerminal(job.State) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("job %s did not reach terminal state in %s, last state=%s", jobID, timeout, job.State)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
