package analyze

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// EventType identifies what kind of event was emitted.
type EventType string

const (
	EventJobCreated     = "job_created"
	EventStageChanged   = "stage_changed"
	EventFuzzed         = "fuzzed"
	EventSourceChecked  = "source_checked"
	EventScored        = "scored"
	EventSummarized     = "summarized"
	EventQuotaExhausted = "quota_exhausted"
	EventDone           = "done"
	EventError          = "error"
)

// Event is the typed progress event from Plan §4.5. It is fan-out to all
// subscribers of a job via a buffered channel.
//
// Boring default: the subscriber channel is buffered (capacity 64) so a
// slow reader does not block the pipeline. If the buffer fills, we drop
// a single EventError on that subscriber's channel. A bounded ring or
// WS/GRPC streaming subscription (with back-pressure) can come in the
// API slice (Plan §5.9).
const subscriberBuf = 64

// Event is the unit of progress emitted to subscribers.
type Event struct {
	JobID   string
	Type    EventType
	At      time.Time
	Payload any
}

// ---- Payload types ----

type StageChangedPayload struct {
	From  string
	To    string
	Stage string
}

type FuzzedPayload struct {
	Count     int
	Candidates []Candidate
}

type SourceCheckedPayload struct {
	Domain  string
	Results []Result
}

type ScoredPayload struct {
	Finding Finding
}

type SummarizedPayload struct {
	Narrative Narrative
}

type QuotaExhaustedPayload struct {
	Source string
}

type DonePayload struct {
	State string
}

type ErrorPayload struct {
	Message string
}

// EventBroker manages the fan-out of events to job subscribers.
// All methods are safe for concurrent use.
type EventBroker struct {
	mu    sync.Mutex
	jobs  map[string]*jobState // keyed by jobID
	order []string              // insertion order for deterministic iteration
}

// NewEventBroker creates an empty broker.
func NewEventBroker() *EventBroker {
	return &EventBroker{jobs: make(map[string]*jobState)}
}

// Subscribe adds a new subscriber channel to the named job. It returns
// an error if the job does not exist.
//
// The channel is closed when the job reaches a terminal state.
func (b *EventBroker) Subscribe(ctx context.Context, jobID string) (<-chan Event, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	js, ok := b.jobs[jobID]
	if !ok {
		return nil, fmt.Errorf("job %q not found", jobID)
	}
	ch := make(chan Event, subscriberBuf)
	js.addSubscriber(ch)
	return ch, nil
}

// broadcast sends ev to every subscriber of jobID. It acquires the
// per-job subscriber lock only. The event channel send is non-blocking:
// if the buffer is full we drop an error event and close the dead channel.
//
// Caller must not hold b.mu.
func (b *EventBroker) broadcast(jobID string, ev Event) {
	b.mu.Lock()
	js, ok := b.jobs[jobID]
	b.mu.Unlock()
	if !ok {
		return
	}
	js.broadcast(ev)
}

// Register records a new job in the broker and returns its in-memory
// state. It returns an error if a job with the same ID already exists
// (should not happen with crypto/rand IDs).
func (b *EventBroker) Register(jobID string, js *jobState) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, exists := b.jobs[jobID]; exists {
		return fmt.Errorf("job %q already registered", jobID)
	}
	b.jobs[jobID] = js
	b.order = append(b.order, jobID)
	return nil
}

// Get returns the snapshot of a job and whether it was found.
func (b *EventBroker) Get(jobID string) (Job, bool) {
	b.mu.Lock()
	js, ok := b.jobs[jobID]
	b.mu.Unlock()
	if !ok {
		return Job{}, false
	}
	return js.snapshot(), true
}

// ---- per-job subscriber management ----

// addSubscriber registers a new channel. Caller must hold js.mu.
func (js *jobState) addSubscriber(ch chan Event) {
	js.subMu.Lock()
	defer js.subMu.Unlock()
	js.subs = append(js.subs, ch)
}

// broadcast sends ev to all subscriber channels. A dead channel (send
// would block) is removed from the list and closed. Caller must not
// hold js.mu; this takes js.subMu only.
func (js *jobState) broadcast(ev Event) {
	js.subMu.Lock()
	defer js.subMu.Unlock()
	var alive []chan Event
	for _, ch := range js.subs {
		select {
		case ch <- ev:
			alive = append(alive, ch)
		default:
			// Slow consumer: drop and close.
			close(ch)
		}
	}
	js.subs = alive
}

// closeSubscribers sends the terminal event and closes every channel.
// Called when a job reaches a terminal state. Caller must not hold js.mu.
func (js *jobState) closeSubscribers(state string) {
	js.subMu.Lock()
	defer js.subMu.Unlock()
	for _, ch := range js.subs {
		select {
		case ch <- Event{JobID: js.Job.ID, Type: EventDone, At: time.Now(), Payload: DonePayload{State: state}}:
		default:
		}
		close(ch)
	}
	js.subs = nil
}
