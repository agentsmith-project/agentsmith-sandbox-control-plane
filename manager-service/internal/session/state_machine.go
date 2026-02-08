package session

import (
	"fmt"
	"sync"
)

// StateMachine manages state transitions for a session
type StateMachine struct {
	mu    sync.Mutex
	state State
}

// validTransitions defines the allowed state transitions
// Format: fromState -> [toStates...]
var validTransitions = map[State][]State{
	StateCreating: {
		StateReady,       // Successfully created
		StateFailed,      // Creation failed
	},
	StateRestoring: {
		StateReady,         // Successfully restored
		StateFailed,        // Restore failed
	},
	StateReady: {
		StateOffline,       // Client disconnected
		StateTerminating,   // Being terminated
		StateSnapshotting,  // Creating snapshot
		StateFailed,        // Failed while ready
	},
	StateOffline: {
		StateReady,         // Client reconnected
		StateTerminating,   // Being terminated
	},
	StateSnapshotting: {
		StateReady,           // Snapshot completed
		StateSnapshotFailed,  // Snapshot failed
	},
	StateSnapshotFailed: {
		StateTerminating,     // Giving up and terminating
		StateReady,           // Recovering and continuing
	},
	StateTerminating: {
		StateTerminated,  // Successfully terminated
		StateFailed,      // Failed during termination
	},
	StateTerminated: {
		// No valid transitions from terminated
	},
	StateFailed: {
		StateTerminating,  // Cleanup
		StateTerminated,   // Final state
	},
}

// NewStateMachine creates a new state machine with the given initial state
func NewStateMachine(initialState State) *StateMachine {
	return &StateMachine{
		state: initialState,
	}
}

// CurrentState returns the current state
func (sm *StateMachine) CurrentState() State {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	return sm.state
}

// CanTransition checks if a transition to the target state is allowed
func (sm *StateMachine) CanTransition(target State) bool {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	validTargets, ok := validTransitions[sm.state]
	if !ok {
		return false
	}

	for _, valid := range validTargets {
		if valid == target {
			return true
		}
	}
	return false
}

// Transition attempts to transition to the target state
// Returns an error if the transition is not allowed
func (sm *StateMachine) Transition(target State) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	// Check if transitioning to same state
	if sm.state == target {
		return fmt.Errorf("invalid state transition: already in state %s", target)
	}

	validTargets, ok := validTransitions[sm.state]
	if !ok {
		return fmt.Errorf("invalid state transition: no valid transitions from %s", sm.state)
	}

	for _, valid := range validTargets {
		if valid == target {
			sm.state = target
			return nil
		}
	}

	return fmt.Errorf("invalid state transition: cannot transition from %s to %s", sm.state, target)
}

// setState directly sets the state without validation
// This is used during initialization and should be used with caution
func (sm *StateMachine) setState(state State) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.state = state
}
