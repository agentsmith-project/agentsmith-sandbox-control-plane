package session

import (
	"testing"
)

func TestStateMachine_ValidTransitions(t *testing.T) {
	tests := []struct {
		name        string
		from        State
		to          State
		shouldAllow bool
	}{
		// Valid transitions
		{"Creating to Ready", StateCreating, StateReady, true},
		{"Creating to Failed", StateCreating, StateFailed, true},
		{"Restoring to Ready", StateRestoring, StateReady, true},
		{"Restoring to Failed", StateRestoring, StateFailed, true},
		{"Ready to Offline", StateReady, StateOffline, true},
		{"Ready to Terminating", StateReady, StateTerminating, true},
		{"Ready to Snapshotting", StateReady, StateSnapshotting, true},
		{"Ready to Failed", StateReady, StateFailed, true},
		{"Offline to Ready", StateOffline, StateReady, true},
		{"Offline to Terminating", StateOffline, StateTerminating, true},
		{"Snapshotting to Ready", StateSnapshotting, StateReady, true},
		{"Snapshotting to SnapshotFailed", StateSnapshotting, StateSnapshotFailed, true},
		{"SnapshotFailed to Terminating", StateSnapshotFailed, StateTerminating, true},
		{"SnapshotFailed to Ready", StateSnapshotFailed, StateReady, true},
		{"Terminating to Terminated", StateTerminating, StateTerminated, true},
		{"Terminating to Failed", StateTerminating, StateFailed, true},
		{"Failed to Terminating", StateFailed, StateTerminating, true},
		{"Failed to Terminated", StateFailed, StateTerminated, true},

		// Invalid transitions
		{"Ready to Creating", StateReady, StateCreating, false},
		{"Creating to Restoring", StateCreating, StateRestoring, false},
		{"Terminated to Ready", StateTerminated, StateReady, false},
		{"Failed to Creating", StateFailed, StateCreating, false},
		{"Offline to Creating", StateOffline, StateCreating, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sm := NewStateMachine(tt.from)
			err := sm.Transition(tt.to)

			if tt.shouldAllow {
				if err != nil {
					t.Errorf("expected transition from %s to %s to succeed, got error: %v", tt.from, tt.to, err)
				}
				if sm.CurrentState() != tt.to {
					t.Errorf("expected state to be %s after transition, got %s", tt.to, sm.CurrentState())
				}
			} else {
				if err == nil {
					t.Errorf("expected transition from %s to %s to fail, but it succeeded", tt.from, tt.to)
				}
				if sm.CurrentState() != tt.from {
					t.Errorf("expected state to remain %s after failed transition, got %s", tt.from, sm.CurrentState())
				}
			}
		})
	}
}

func TestStateMachine_CanTransition(t *testing.T) {
	sm := NewStateMachine(StateCreating)

	// Valid transition
	if !sm.CanTransition(StateReady) {
		t.Error("expected CanTransition to return true for Creating -> Ready")
	}

	// Invalid transition
	if sm.CanTransition(StateRestoring) {
		t.Error("expected CanTransition to return false for Creating -> Restoring")
	}
}

func TestStateMachine_CurrentState(t *testing.T) {
	initialState := StateCreating
	sm := NewStateMachine(initialState)

	if sm.CurrentState() != initialState {
		t.Errorf("expected initial state to be %s, got %s", initialState, sm.CurrentState())
	}

	sm.Transition(StateReady)
	if sm.CurrentState() != StateReady {
		t.Errorf("expected state to be %s after transition, got %s", StateReady, sm.CurrentState())
	}
}

func TestStateMachine_ConcurrentTransitions(t *testing.T) {
	sm := NewStateMachine(StateCreating)
	errors := make(chan error, 100)

	// Try multiple transitions concurrently
	for i := 0; i < 10; i++ {
		go func() {
			err := sm.Transition(StateReady)
			errors <- err
		}()
	}

	// Collect results
	successCount := 0
	for i := 0; i < 10; i++ {
		if <-errors == nil {
			successCount++
		}
	}

	// Only one transition should succeed
	if successCount != 1 {
		t.Errorf("expected exactly 1 transition to succeed, got %d", successCount)
	}

	// Final state should be Ready
	if sm.CurrentState() != StateReady {
		t.Errorf("expected final state to be %s, got %s", StateReady, sm.CurrentState())
	}
}

func TestAllStatesDefined(t *testing.T) {
	// Ensure all required states are defined
	requiredStates := []State{
		StateCreating,
		StateRestoring,
		StateReady,
		StateOffline,
		StateTerminating,
		StateTerminated,
		StateFailed,
		StateSnapshotting,
		StateSnapshotFailed,
	}

	for _, state := range requiredStates {
		if string(state) == "" {
			t.Errorf("state %v has empty string representation", state)
		}
	}
}

func TestStateMachine_TransitionToSameState(t *testing.T) {
	sm := NewStateMachine(StateReady)

	// Transitioning to the same state should fail
	err := sm.Transition(StateReady)
	if err == nil {
		t.Error("expected transition to same state to fail")
	}

	// State should remain unchanged
	if sm.CurrentState() != StateReady {
		t.Errorf("expected state to remain %s, got %s", StateReady, sm.CurrentState())
	}
}
