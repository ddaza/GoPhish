package analyze

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"go.uber.org/zap"
)

// MiniLoopConfig bounds the fuzz↔source-check mini loop (Plan §4.8).
type MiniLoopConfig struct {
	MaxIterations int // max re-fuzz/re-check cycles (0 = disabled)
}

// Engine runs the GoPhish pipeline behind the Service interface.
//
// MVP transport: goroutines in-process. The WS/gRPC adapter is
// deferred to Slice 10 (internal/transport); swapping the adapter
// does not change the interfaces.
//
// DI: concrete (or fake) Source/Fuzzer/Scorer/Clusterer/Summarizer
// instances are injected at construction time, so tests and
// production wiring use the same code path.
type Engine struct {
	source    Source
	fuzzer    Fuzzer
	scorer    Scorer
	clusterer Clusterer
	summarizer Summarizer

	loop MiniLoopConfig
	log  *zap.Logger

	mu     sync.Mutex
	jobs   map[string]*jobState
	broker *EventBroker
}

// NewEngine constructs an Engine with the service implementations
// and a logger. A nil logger is accepted (no-op); production code
// should pass a configured zerolog.Logger.
//
// Boring default: a single goroutine per job. Per-job goroutines
// are cheap in the MVP and let each job run independently under
// its own cancellable context.
func NewEngine(source Source, fuzzer Fuzzer, scorer Scorer,
	clusterer Clusterer, summarizer Summarizer, loop MiniLoopConfig, log *zap.Logger,
) *Engine {
	e := &Engine{
		source:     source,
		fuzzer:     fuzzer,
		scorer:     scorer,
		clusterer:  clusterer,
		summarizer: summarizer,
		loop:       loop,
		log:        log,
		jobs:       make(map[string]*jobState),
		broker:     NewEventBroker(),
	}
	if e.log == nil {
		e.log = zap.NewNop()
	}
	return e
}

// Broker returns the engine's event broker (for testing).
func (e *Engine) Broker() *EventBroker { return e.broker }

// RunAnalysis creates a job and starts it in a goroutine.
func (e *Engine) RunAnalysis(ctx context.Context, req AnalysisRequest) (string, error) {
	id, err := newJobID()
	if err != nil {
		return "", fmt.Errorf("new job id: %w", err)
	}

	ctx, cancel := context.WithCancel(ctx)
	js := &jobState{
		Job: Job{
			ID:        id,
			Seed:      req.Seed,
			State:     StateCreated,
			Stage:     "created",
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
			Progress:  JobProgress{},
		},
		cancel: cancel,
	}

	e.mu.Lock()
	if _, exists := e.jobs[id]; exists {
		e.mu.Unlock()
		return "", fmt.Errorf("job %q already exists", id)
	}
	e.jobs[id] = js
	e.broker.Register(id, js)
	e.mu.Unlock()

	e.log.Info("job created", zap.String("jobID", id), zap.String("seed", req.Seed))

	// Run the pipeline in its own goroutine (MVP transport).
	// We emit job_created from inside the goroutine so that
	// subscribers calling Subscribe() before the pipeline
	// runs will receive it. Boring default: this keeps the
	// event ordering simple; callers that want the exact
	// creation timestamp can use Job.CreatedAt.
	go func() {
		ev := Event{
			JobID:   id,
			Type:    EventJobCreated,
			At:      time.Now(),
			Payload: AnalysisRequestPayload{Seed: req.Seed, Max: req.Max, Immediate: req.Immediate},
		}
		e.broker.broadcast(id, ev)
		e.run(ctx, id)
	}()

	return id, nil
}

// GetJob returns a snapshot of the requested job.
func (e *Engine) GetJob(ctx context.Context, jobID string) (Job, bool) {
	return e.broker.Get(jobID)
}

// Subscribe registers a subscriber channel for the named job.
func (e *Engine) Subscribe(ctx context.Context, jobID string) (<-chan Event, error) {
	return e.broker.Subscribe(ctx, jobID)
}

// Cancel cancels the named job's context, which the pipeline
// honors at each stage boundary.
func (e *Engine) Cancel(ctx context.Context, jobID string) error {
	e.mu.Lock()
	js, ok := e.jobs[jobID]
	e.mu.Unlock()
	if !ok {
		return fmt.Errorf("job %q not found", jobID)
	}
	js.cancel()
	js.cancelFunc()
	e.log.Info("job cancelled", zap.String("jobID", jobID))
	return nil
}

func (js *jobState) cancelFunc() {
	js.mu.Lock()
	defer js.mu.Unlock()
	if js.cancel != nil {
		js.cancel()
		js.cancel = nil
	}
}

// run executes the pipeline for a single job. It honors the
// context for cancellation and emits events through the broker.
func (e *Engine) run(ctx context.Context, jobID string) {
	defer func() {
		if r := recover(); r != nil {
			js := e.jobLocked(jobID)
			if js != nil {
				js.mu.Lock()
				js.Job.State = StateFailed
				js.Job.Stage = "failed"
				js.Job.Error = fmt.Sprintf("panic: %v", r)
				js.mu.Unlock()
				js.closeSubscribers(js.Job.State)
			}
			e.mu.Lock()
			delete(e.jobs, jobID)
			e.mu.Unlock()
			return
		}
		js := e.jobLocked(jobID)
		if js == nil {
			return
		}
		js.mu.Lock()
		if !isTerminal(js.Job.State) {
			js.Job.State = StateFailed
			js.Job.Stage = "failed"
			js.Job.Error = "pipeline exited without reaching a terminal state"
		}
		js.mu.Unlock()
		js.closeSubscribers(js.Job.State)
		e.mu.Lock()
		delete(e.jobs, jobID)
		e.mu.Unlock()
	}()
	js := e.jobLocked(jobID)
	if js == nil {
		return
	}

	// Transition CREATED -> SEEDING.
	if err := js.transition(StateSeeding, "seed-check"); err != nil {
		js.fail(err)
		return
	}

	// --- 1. Seed check ---
	// Boring default: seed check uses the parent context. The
	// seedCancel variable exists so the seed lookup is cancellable
	// without losing the parent context for the rest of the pipeline.
	sctx, seedCancel := context.WithCancel(ctx)
	results, err := e.source.Fetch(sctx, Query{Domain: js.Job.Seed, Mode: "registration"})
	seedCancel()
	if err != nil {
		// If the source returned a cancellation error, honor it by
		// transitioning to CANCELLED rather than FAILED.
		if errors.Is(err, context.Canceled) {
			js.cancelJob()
		} else {
			js.fail(fmt.Errorf("seed lookup: %w", err))
		}
		return
	}
	e.log.Info("seed lookup complete", zap.String("jobID", jobID), zap.Int("results", len(results)))

	// --- 2. Fuzz ---
	if err := js.transition(StateFuzzing, "fuzz"); err != nil {
		js.fail(err)
		return
	}
	candidates, err := e.fuzzer.Generate(ctx, js.Job.Seed, js.Job.Progress.Fuzzed+js.Job.Progress.Checked+js.Job.Progress.Scored)
	if err != nil {
		js.fail(fmt.Errorf("fuzz: %w", err))
		return
	}
	js.addProgressFuzzed(len(candidates))

	// --- 3. Check (source-check candidates through throttle) ---
	// Transition to CHECKING before iterating candidates.
	if err := js.transition(StateChecking, "check"); err != nil {
		js.fail(err)
		return
	}
	// In the MVP, throttle is deferred (Slice 3). We call the
	// source directly; the throttle wrapper is injected via a
	// thin decorator in the production wiring.
	for _, c := range candidates {
		select {
		case <-ctx.Done():
			return
		default:
		}
		r, err := e.source.Fetch(ctx, Query{Domain: c.Normalized, Mode: "registration"})
		if err != nil {
			js.addQuotaExhausted(e.source.Name())
			e.log.Warn("source error on candidate", zap.String("jobID", jobID), zap.Error(err))
			continue
		}
		js.addProgressChecked(1)
		e.broker.broadcast(jobID, Event{
			JobID:   jobID,
			Type:    EventSourceChecked,
			At:      time.Now(),
			Payload: SourceCheckedPayload{Domain: c.Normalized, Results: r},
		})
	}

	// --- Mini loop (Plan §4.8) ---
	if e.loop.MaxIterations > 0 {
		for i := 0; i < e.loop.MaxIterations; i++ {
			select {
			case <-ctx.Done():
				return
			default:
			}
			// Expand around confirmed hits: re-fuzz from confirmed
			// domains and re-check. For the skeleton we re-fuzz
			// once more per iteration and re-check the new batch.
			extra, err := e.fuzzer.Generate(ctx, js.Job.Seed, js.Job.Progress.Fuzzed+js.Job.Progress.Checked+js.Job.Progress.Scored)
			if err != nil {
				js.addQuotaExhausted(e.source.Name())
				continue
			}
			for _, c := range extra {
				select {
				case <-ctx.Done():
					return
				default:
				}
				_, err := e.source.Fetch(ctx, Query{Domain: c.Normalized, Mode: "registration"})
				if err != nil {
					js.addQuotaExhausted(e.source.Name())
					continue
				}
				js.addProgressChecked(1)
			}
			e.broker.broadcast(jobID, Event{
				JobID:   jobID,
				Type:    EventFuzzed,
				At:      time.Now(),
				Payload: FuzzedPayload{Count: len(extra)},
			})
		}
	}

	// Transition CHECKING -> SCORING.
	if err := js.transition(StateScoring, "score-cluster"); err != nil {
		js.fail(err)
		return
	}

	// --- 4. Score + Cluster ---
	var findings []Finding
	for _, c := range candidates {
		select {
		case <-ctx.Done():
			return
		default:
		}
		f, err := e.scorer.Score(c, results)
		if err != nil {
			js.addQuotaExhausted("scorer")
			continue
		}
		findings = append(findings, f)
		js.addProgressScored(1)
		// Write findings to the job snapshot incrementally so
		// GetJob callers can observe them as the pipeline runs.
		js.mu.Lock()
		js.Job.Findings = append(js.Job.Findings, f)
		js.mu.Unlock()
		e.broker.broadcast(jobID, Event{
			JobID:   jobID,
			Type:    EventScored,
			At:      time.Now(),
			Payload: ScoredPayload{Finding: f},
		})
	}

	clusters, err := e.clusterer.Cluster(findings)
	if err != nil {
		js.addQuotaExhausted("clusterer")
	} else {
		js.addProgressClustered(len(clusters))
	}
	e.log.Info("scoring + clustering complete", zap.String("jobID", jobID), zap.Int("findings", len(findings)), zap.Int("clusters", len(clusters)))

	// --- 5. Summarize (optional LLM narrative) ---
	if err := js.transition(StateSummarizing, "summarize"); err != nil {
		js.fail(err)
		return
	}

	narrative, err := e.summarizer.Summarize(ctx, findings)
	if err != nil {
		e.log.Info("summarize skipped", zap.String("jobID", jobID), zap.Error(err))
	} else {
		js.mu.Lock()
		js.Job.Narrative = &narrative
		js.mu.Unlock()
		e.broker.broadcast(jobID, Event{
			JobID:   jobID,
			Type:    EventSummarized,
			At:      time.Now(),
			Payload: SummarizedPayload{Narrative: narrative},
		})
	}

	// --- Done ---
	js.mu.Lock()
	js.Job.State = StateCompleted
	js.Job.Stage = "completed"
	js.Job.UpdatedAt = time.Now()
	js.mu.Unlock()

	e.broker.broadcast(jobID, Event{
		JobID:   jobID,
		Type:    EventDone,
		At:      time.Now(),
		Payload: DonePayload{State: StateCompleted},
	})

	e.log.Info("job complete", zap.String("jobID", jobID))
}

// ---- payload types for events ----

// AnalysisRequestPayload is the payload for EventJobCreated.
type AnalysisRequestPayload struct {
	Seed      string
	Max       int
	Immediate bool
}

// ---- helpers ----

// jobLocked returns the jobState for jobID while holding e.mu.
func (e *Engine) jobLocked(jobID string) *jobState {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.jobs[jobID]
}

// progress helpers mutate the job's progress snapshot.
func (js *jobState) addProgressFuzzed(n int) {
	js.mu.Lock()
	defer js.mu.Unlock()
	js.Job.Progress.Fuzzed += n
	js.Job.UpdatedAt = time.Now()
}

func (js *jobState) addProgressChecked(n int) {
	js.mu.Lock()
	defer js.mu.Unlock()
	js.Job.Progress.Checked += n
	js.Job.UpdatedAt = time.Now()
}

func (js *jobState) addProgressScored(n int) {
	js.mu.Lock()
	defer js.mu.Unlock()
	js.Job.Progress.Scored += n
	js.Job.UpdatedAt = time.Now()
}

func (js *jobState) addProgressClustered(n int) {
	js.mu.Lock()
	defer js.mu.Unlock()
	js.Job.Progress.Clustered += n
	js.Job.UpdatedAt = time.Now()
}

func (js *jobState) addQuotaExhausted(source string) {
	js.mu.Lock()
	defer js.mu.Unlock()
	js.Job.Progress.QuotaExhausted = append(js.Job.Progress.QuotaExhausted, source)
	js.Job.UpdatedAt = time.Now()
}
