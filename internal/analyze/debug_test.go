package analyze

import (
	"context"
	"testing"
	"time"
)

func TestDebugRunAnalysis(t *testing.T) {
	engine, _, _, _, _, _, _ := newTestEngine(t)
	ctx := context.Background()
	jobID, err := engine.RunAnalysis(ctx, AnalysisRequest{Seed: "example.com", Max: 1})
	if err != nil {
		t.Fatal(err)
	}
	// Wait
	deadline := time.After(5 * time.Second)
	for {
		job, ok := engine.GetJob(ctx, jobID)
		if !ok {
			t.Log("job removed from map")
			return
		}
		t.Logf("job state=%s stage=%s error=%q", job.State, job.Stage, job.Error)
		if isTerminal(job.State) {
			t.Logf("terminal: %s", job.State)
			return
		}
		select {
		case <-deadline:
			t.Fatal("timeout")
		case <-time.After(50 * time.Millisecond):
		}
	}
}
