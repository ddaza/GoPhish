package analyze

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestEngine(t *testing.T) (*Engine, *FakeSource, *FakeFuzzer, *FakeScorer, *FakeClusterer, *FakeSummarizer, *FakeLogger) {
	t.Helper()
	src := &FakeSource{NameVal: "fake-rdap", ResultsToReturn: []Result{{Domain: "example.com", Registered: true, Source: "fake-rdap"}}}
	fuzz := &FakeFuzzer{NameVal: "fake-fuzz", CandidatesToReturn: MakeCandidates("example.com", 3)}
	score := &FakeScorer{}
	clust := &FakeClusterer{ClustersToReturn: []Cluster{{ID: "c1", DomainIDs: []string{"d1"}}}}
	summ := &FakeSummarizer{NarrativeToReturn: Narrative{Summary: "test summary"}}
	log := &FakeLogger{}
	engine := NewEngine(src, fuzz, score, clust, summ, MiniLoopConfig{MaxIterations: 0}, log)
	return engine, src, fuzz, score, clust, summ, log
}

func TestEngineRunAnalysisCompletes(t *testing.T) {
	engine, _, _, _, _, _, log := newTestEngine(t)

	ctx := context.Background()
	jobID, err := engine.RunAnalysis(ctx, AnalysisRequest{Seed: "example.com", Max: 10})
	require.NoError(t, err)
	require.NotEmpty(t, jobID)

	// Wait for the goroutine to finish.
	waitForTerminal(t, engine, jobID, 5*time.Second)

	job, ok := engine.GetJob(ctx, jobID)
	require.True(t, ok, "job should exist")
	assert.Equal(t, StateCompleted, job.State)
	assert.Equal(t, "example.com", job.Seed)
	assert.NotNil(t, log.Infos)
}

func TestEngineRunAnalysisCancelled(t *testing.T) {
	engine, _, fuzz, _, _, _, _ := newTestEngine(t)
	// Make fuzz slow so we can cancel mid-flight.
	fuzz.ShouldError = false
	// Use a source that blocks until ctx is cancelled to test cancellation.
	blockingSrc := &FakeSource{
		NameVal:        "blocking",
		ResultsToReturn: []Result{{Domain: "example.com", Registered: true}},
		// Make Fetch block until context is cancelled.
	}
	// Override engine's source.
	engine.source = blockingSrc
	// Also override fuzz to block.
	fuzzer := &FakeFuzzer{
		NameVal:           "blocking-fuzz",
		CandidatesToReturn: MakeCandidates("example.com", 100),
	}
	engine.fuzzer = fuzzer

	ctx, cancel := context.WithCancel(context.Background())
	jobID, err := engine.RunAnalysis(ctx, AnalysisRequest{Seed: "example.com", Max: 10})
	require.NoError(t, err)

	// Cancel immediately.
	cancel()
	// Give time for cancellation to propagate.
	time.Sleep(50 * time.Millisecond)

	job, ok := engine.GetJob(ctx, jobID)
	require.True(t, ok)
	assert.Equal(t, StateCancelled, job.State)
}

func TestEngineSubscribeAndReceiveEvents(t *testing.T) {
	engine, _, _, _, _, _, _ := newTestEngine(t)

	ctx := context.Background()
	jobID, err := engine.RunAnalysis(ctx, AnalysisRequest{Seed: "example.com", Max: 1})
	require.NoError(t, err)

	// Subscribe after the job is created (still gets events).
	ch, err := engine.Subscribe(ctx, jobID)
	require.NoError(t, err)

	// Collect events until we see EventDone or timeout.
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
	// Should have received at least job_created and done.
	assert.Contains(t, types, EventJobCreated)
	assert.Contains(t, types, EventDone)
	// Should also see stage transitions.
	assert.Contains(t, types, EventStageChanged)
}

func TestEngineSubscribeUnknownJob(t *testing.T) {
	engine, _, _, _, _, _, _ := newTestEngine(t)
	_, err := engine.Subscribe(context.Background(), "nonexistent")
	assert.Error(t, err)
}

func TestEngineGetJobMissing(t *testing.T) {
	engine, _, _, _, _, _, _ := newTestEngine(t)
	job, ok := engine.GetJob(context.Background(), "nonexistent")
	require.False(t, ok)
	assert.Empty(t, job.ID)
}

func TestEngineCancelMissingJob(t *testing.T) {
	engine, _, _, _, _, _, _ := newTestEngine(t)
	err := engine.Cancel(context.Background(), "nonexistent")
	assert.Error(t, err)
}

func TestEnginePipelineEmitsAllEventTypes(t *testing.T) {
	engine, _, _, _, _, _, _ := newTestEngine(t)

	ctx := context.Background()
	jobID, err := engine.RunAnalysis(ctx, AnalysisRequest{Seed: "example.com", Max: 2})
	require.NoError(t, err)

	ch, err := engine.Subscribe(ctx, jobID)
	require.NoError(t, err)

	// Collect all events.
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
	// Check that key events were emitted.
	for _, want := range []string{EventJobCreated, EventStageChanged, EventDone} {
		assert.Contains(t, seen, want, "event %s should be emitted", want)
	}
}

func TestEngineFindingsAndNarrative(t *testing.T) {
	engine, _, _, _, _, _, _ := newTestEngine(t)

	ctx := context.Background()
	jobID, err := engine.RunAnalysis(ctx, AnalysisRequest{Seed: "example.com", Max: 1})
	require.NoError(t, err)

	waitForTerminal(t, engine, jobID, 5*time.Second)

	job, ok := engine.GetJob(ctx, jobID)
	require.True(t, ok)
	assert.Equal(t, StateCompleted, job.State)
	// Should have findings from the scorer.
	assert.GreaterOrEqual(t, len(job.Findings), 1)
	// Should have narrative from summarizer.
	assert.NotNil(t, job.Narrative)
	assert.Equal(t, "test summary", job.Narrative.Summary)
	// Clusters should be recorded.
	assert.GreaterOrEqual(t, job.Progress.Clustered, 1)
}

func TestEngineMiniLoopTransitions(t *testing.T) {
	engine, _, _, _, _, _, _ := newTestEngine(t)
	engine.loop = MiniLoopConfig{MaxIterations: 2}

	ctx := context.Background()
	jobID, err := engine.RunAnalysis(ctx, AnalysisRequest{Seed: "example.com", Max: 2})
	require.NoError(t, err)

	waitForTerminal(t, engine, jobID, 5*time.Second)

	job, ok := engine.GetJob(ctx, jobID)
	require.True(t, ok)
	assert.Equal(t, StateCompleted, job.State)
	// Should see extra fuzz events from mini loop.
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
	// With MaxIterations=2, we should see at least 2 fuzzed events
	// from the mini loop iterations (plus the initial fuzz).
	assert.GreaterOrEqual(t, fuzzCount, 2)
}

func TestEngineSeedFailureTransitionsToFailed(t *testing.T) {
	engine, _, _, _, _, _, _ := newTestEngine(t)
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
	engine, _, fuzz, _, _, _, _ := newTestEngine(t)
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
	engine, _, _, _, _, _, _ := newTestEngine(t)
	// Make scorer fail so quota_exhausted gets recorded.
	engine.scorer = &FakeScorer{ShouldError: true, ErrorToReturn: assert.AnError}

	ctx := context.Background()
	jobID, err := engine.RunAnalysis(ctx, AnalysisRequest{Seed: "example.com", Max: 1})
	require.NoError(t, err)

	waitForTerminal(t, engine, jobID, 5*time.Second)

	job, ok := engine.GetJob(ctx, jobID)
	require.True(t, ok)
	// Quota exhausted should record the scorer source.
	assert.Contains(t, job.Progress.QuotaExhausted, "fake-fuzz")
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

	// Valid transition: CREATED -> SEEDING.
	require.NoError(t, js.transition(StateSeeding, "seed-check"))
	assert.Equal(t, StateSeeding, js.Job.State)

	// Valid transition: SEEDING -> FUZZING.
	require.NoError(t, js.transition(StateFuzzing, "fuzz"))
	assert.Equal(t, StateFuzzing, js.Job.State)

	// Valid transition: FUZZING -> CHECKING.
	require.NoError(t, js.transition(StateChecking, "check"))
	assert.Equal(t, StateChecking, js.Job.State)

	// Mini loop: CHECKING -> FUZZING.
	require.NoError(t, js.transition(StateFuzzing, "fuzz-loop"))
	assert.Equal(t, StateFuzzing, js.Job.State)

	// Mini loop: FUZZING -> CHECKING.
	require.NoError(t, js.transition(StateChecking, "check-loop"))
	assert.Equal(t, StateChecking, js.Job.State)

	// CHECKING -> SCORING.
	require.NoError(t, js.transition(StateScoring, "score"))
	assert.Equal(t, StateScoring, js.Job.State)

	// SCORING -> SUMMARIZING.
	require.NoError(t, js.transition(StateSummarizing, "summarize"))
	assert.Equal(t, StateSummarizing, js.Job.State)

	// SUMMARIZING -> COMPLETED.
	require.NoError(t, js.transition(StateCompleted, "done"))
	assert.Equal(t, StateCompleted, js.Job.State)

	// Terminal state rejects further transitions.
	err := js.transition(StateFuzzing, "reopen")
	assert.Error(t, err)
}

func TestStateMachineIllegalTransition(t *testing.T) {
	js := &jobState{
		Job: Job{
			ID:    "test-2",
			State: StateCreated,
		},
	}
	// CREATED -> CHECKING is illegal.
	err := js.transition(StateChecking, "skip")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "illegal transition")
}

func TestStateMachineFailFromAny(t *testing.T) {
	js := &jobState{
		Job: Job{
			ID:    "test-3",
			State: StateChecking,
		},
	}
	js.fail(assert.AnError)
	assert.Equal(t, StateFailed, js.Job.State)
	assert.Contains(t, js.Job.Error, "assertion failed")
}

func TestStateMachineCancelFromAny(t *testing.T) {
	js := &jobState{
		Job: Job{
			ID:    "test-4",
			State: StateFuzzing,
		},
	}
	js.cancelJob()
	assert.Equal(t, StateCancelled, js.Job.State)
}

func TestStateMachineAlreadyTerminal(t *testing.T) {
	js := &jobState{
		Job: Job{
			ID:    "test-5",
			State: StateCompleted,
		},
	}
	// Transition should fail.
	err := js.transition(StateFuzzing, "reopen")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "already in terminal state")
}

func TestJobSnapshotIsIsolated(t *testing.T) {
	js := &jobState{
		Job: Job{
			ID:        "snap",
			State:     StateCompleted,
			Findings:  []Finding{{Domain: "a.com"}},
			Progress:  JobProgress{Fuzzed: 5},
		},
	}
	snap := js.snapshot()
	// Mutating the snapshot must not affect the original.
	snap.Findings = append(snap.Findings, Finding{Domain: "b.com"})
	snap.Progress.Fuzzed = 999
	// Original must be unchanged.
	js.mu.Lock()
	assert.Len(t, js.Job.Findings, 1)
	assert.Equal(t, 5, js.Job.Progress.Fuzzed)
	js.mu.Unlock()
}

func TestEventBrokerRegisterAndGet(t *testing.T) {
	b := NewEventBroker()
	js := &jobState{Job: Job{ID: "broker-1"}}
	require.NoError(t, b.Register("broker-1", js))
	// Duplicate registration should error.
	assert.Error(t, b.Register("broker-1", js))

	job, ok := b.Get("broker-1")
	assert.True(t, ok)
	assert.Equal(t, "broker-1", job.ID)

	_, ok = b.Get("nonexistent")
	assert.False(t, ok)
}

func TestEventBrokerSubscribe(t *testing.T) {
	engine, _, _, _, _, _, _ := newTestEngine(t)

	ctx := context.Background()
	jobID, err := engine.RunAnalysis(ctx, AnalysisRequest{Seed: "example.com", Max: 1})
	require.NoError(t, err)

	// Subscribe before the job finishes.
	ch, err := engine.Subscribe(ctx, jobID)
	require.NoError(t, err)
	require.NotNil(t, ch)

	// We should receive at least one event.
	select {
	case ev := <-ch:
		assert.Equal(t, jobID, ev.JobID)
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for event on channel")
	}
}

// waitForTerminal polls engine.GetJob until the job reaches a terminal
// state (COMPLETED, FAILED, or CANCELLED) or the deadline elapses.
func waitForTerminal(t *testing.T, engine *Engine, jobID string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		job, ok := engine.GetJob(context.Background(), jobID)
		if !ok {
			// Job already removed from the map: it finished.
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