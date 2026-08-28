package analyze

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"time"
)

// JobState is the orchestrator's job/run state machine (Plan §4.4).
//
// The CHECKING ⇄ FUZZING edges are the mini loop (Plan §4.8).
const (
	StateCreated      = "CREATED"
	StateSeeding      = "SEEDING"
	StateFuzzing      = "FUZZING"
	StateChecking     = "CHECKING"
	StateScoring      = "SCORING"
	StateSummarizing  = "SUMMARIZING"
	StateCompleted    = "COMPLETED"
	StateFailed       = "FAILED"
	StateCancelled    = "CANCELLED"
)

// validTransitions encodes the state machine from Plan §4.4. A nil map
// means "any state may transition to FAILED or CANCELLED" (handled
// separately so a single error path covers all of them).
var validTransitions = map[string]map[string]struct{}{
	StateCreated: {
		StateSeeding:  {},
		StateFailed:   {},
		StateCancelled: {},
	},
	StateSeeding: {
		StateFuzzing:   {},
		StateFailed:    {},
		StateCancelled: {},
	},
	StateFuzzing: {
		StateChecking:  {},
		StateFailed:    {},
		StateCancelled: {},
	},
	StateChecking: {
		// Mini loop edges.
		StateFuzzing: {},
		StateSeeding: {},
		// Normal forward edge.
		StateScoring:   {},
		StateFailed:    {},
		StateCancelled: {},
	},
	StateScoring: {
		StateSummarizing: {},
		StateFailed:      {},
		StateCancelled:   {},
	},
	StateSummarizing: {
		StateCompleted:  {},
		StateFailed:     {},
		StateCancelled:  {},
	},
}

// jobState is the in-memory record of a running or completed job. It
// is guarded by mu and is read via Job snapshots returned by GetJob.
type jobState struct {
	mu sync.Mutex
	Job
	cancel  context.CancelFunc
	subMu   sync.Mutex
	subs    []chan Event
}

// snapshot returns a copy of the Job fields safe to hand to callers
// without holding the lock.
func (j *jobState) snapshot() Job {
	j.mu.Lock()
	defer j.mu.Unlock()
	cp := j.Job
	cp.Findings = append([]Finding(nil), j.Job.Findings...)
	cp.Progress.Fuzzed = j.Job.Progress.Fuzzed
	cp.Progress.Checked = j.Job.Progress.Checked
	cp.Progress.Scored = j.Job.Progress.Scored
	cp.Progress.Clustered = j.Job.Progress.Clustered
	cp.Progress.QuotaExhausted = append([]string(nil), j.Job.Progress.QuotaExhausted...)
	return cp
}

// transition changes the job's state and stage atomically, emitting
// an EventStageChanged event. It returns an error if the transition
// is not allowed by the state machine. The caller is responsible for
// any additional payload on the event.
//
// Boring default: terminal states (COMPLETED/FAILED/CANCELLED) are
// reachable only via the engine; we never re-open a finished job.
func (j *jobState) transition(to, stage string) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if isTerminal(j.Job.State) && to != j.Job.State {
		return fmt.Errorf("job %s already in terminal state %s, cannot transition to %s",
			j.Job.ID, j.Job.State, to)
	}
	allowed, ok := validTransitions[j.Job.State]
	if !ok {
		return fmt.Errorf("job %s in unknown state %s", j.Job.ID, j.Job.State)
	}
	if _, ok := allowed[to]; !ok {
		return fmt.Errorf("job %s: illegal transition %s -> %s",
			j.Job.ID, j.Job.State, to)
	}
	from := j.Job.State
	j.Job.State = to
	j.Job.Stage = stage
	j.Job.UpdatedAt = time.Now()
	ev := Event{
		JobID: j.Job.ID,
		Type:  EventStageChanged,
		At:    j.Job.UpdatedAt,
		Payload: StageChangedPayload{
			From:  from,
			To:    to,
			Stage: stage,
		},
	}
	// Call broadcast directly (synchronously) while holding js.mu.
	// This ensures the event is delivered before transition() returns,
	// so subscribers added immediately after transition() will see it.
	// Lock order: js.mu -> js.subMu (same as EventBroker.broadcast).
	j.broadcast(ev)
	return nil
}

// fail moves the job to FAILED with the given error message.
func (j *jobState) fail(err error) {
	j.mu.Lock()
	if isTerminal(j.Job.State) {
		j.mu.Unlock()
		return
	}
	j.Job.State = StateFailed
	j.Job.Stage = "failed"
	j.Job.UpdatedAt = time.Now()
	if err != nil {
		j.Job.Error = err.Error()
	}
	ev := Event{
		JobID:   j.Job.ID,
		Type:    EventError,
		At:      j.Job.UpdatedAt,
		Payload: ErrorPayload{Message: j.Job.Error},
	}
	j.mu.Unlock()
	j.broadcast(ev)
}

// cancel moves the job to CANCELLED.
func (j *jobState) cancelJob() {
	j.mu.Lock()
	if isTerminal(j.Job.State) {
		j.mu.Unlock()
		return
	}
	from := j.Job.State
	j.Job.State = StateCancelled
	j.Job.Stage = "cancelled"
	j.Job.UpdatedAt = time.Now()
	ev := Event{
		JobID: j.Job.ID,
		Type:  EventStageChanged,
		At:    j.Job.UpdatedAt,
		Payload: StageChangedPayload{
			From:  from,
			To:    StateCancelled,
			Stage: "cancelled",
		},
	}
	j.mu.Unlock()
	j.broadcast(ev)
}

func isTerminal(state string) bool {
	switch state {
	case StateCompleted, StateFailed, StateCancelled:
		return true
	}
	return false
}

// newJobID returns a 16-byte hex job ID.
//
// Boring default: crypto/rand, no extra deps, collision-free at
// reasonable scale. A monotonic counter is an easy follow-up if we
// ever need ordered IDs.
func newJobID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("rand: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}
