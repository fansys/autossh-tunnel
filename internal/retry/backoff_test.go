package retry

import (
	"testing"
	"time"
)

func TestBackoffController(t *testing.T) {
	ctrl := NewController()
	hash := "test-hash-123"

	// Attempt 1
	shouldRetry, delay := ctrl.RecordFailure(hash, 3, 2)
	if !shouldRetry {
		t.Errorf("Attempt 1 should allow retry")
	}
	if delay < 1*time.Second || delay > 5*time.Second {
		t.Errorf("Unexpected delay on attempt 1: %v", delay)
	}

	// Attempt 2
	shouldRetry, delay2 := ctrl.RecordFailure(hash, 3, 2)
	if !shouldRetry {
		t.Errorf("Attempt 2 should allow retry")
	}
	if delay2 <= delay {
		t.Errorf("Attempt 2 delay (%v) should be greater than attempt 1 (%v)", delay2, delay)
	}

	// Attempt 3
	shouldRetry, _ = ctrl.RecordFailure(hash, 3, 2)
	if !shouldRetry {
		t.Errorf("Attempt 3 should still allow retry")
	}

	// Attempt 4 (Exceeds max 3 retries)
	shouldRetry, _ = ctrl.RecordFailure(hash, 3, 2)
	if shouldRetry {
		t.Errorf("Attempt 4 should NOT allow retry (max 3)")
	}

	// Test Success resets state
	ctrl.RecordSuccess(hash)
	state := ctrl.GetState(hash)
	if state.Count != 0 {
		t.Errorf("RecordSuccess should reset count, got %d", state.Count)
	}
}
