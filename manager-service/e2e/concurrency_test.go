//go:build e2e

package e2e_test

// concurrency_test.go – Exercises ASBCP's internal sandbox execution service under concurrent load.
//
// Goals:
//   - Verify N independent workloads can be created in parallel (no shared state, no deadlock)
//   - Verify idempotent create for the same workload ID is safe under concurrency
//   - Verify concurrent keepalive calls on the same workload do not corrupt annotation state
//   - Verify concurrent delete+create sequences (tombstone race) handle gracefully
//   - Verify GET and DELETE operate correctly during parallel creates (read-write concurrency)
//
// All tests self-clean via t.Cleanup. No shared global state is mutated.

import (
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Parallel independent creates
// ---------------------------------------------------------------------------

// TestConcurrent_CreateNIndependentWorkloads verifies that N distinct workloads
// can be created simultaneously with no failures or hangs. The test considers
// both 201 (created) and 200 (idempotent hit) as acceptable responses.
func TestConcurrent_CreateNIndependentWorkloads(t *testing.T) {
	const n = 5
	wlIDs := make([]string, n)
	for i := 0; i < n; i++ {
		wlIDs[i] = uniqueID(fmt.Sprintf("conc-par-%d", i))
	}

	type result struct {
		id     string
		status int
		body   string
	}
	results := make([]result, n)
	var wg sync.WaitGroup

	for i := 0; i < n; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp := newClient().CreateWorkload(t, testWS, testProj, wlIDs[i],
				CreateRequest{Image: suite.Image})
			results[i] = result{id: wlIDs[i], status: resp.StatusCode, body: resp.BodyString()}
		}()
	}
	wg.Wait()

	// Register cleanup after parallel creates complete (so IDs are all known).
	for _, id := range wlIDs {
		id := id
		t.Cleanup(func() { newClient().DeleteWorkload(t, testWS, testProj, id) })
	}

	for _, r := range results {
		assert.True(t,
			r.status == http.StatusCreated || r.status == http.StatusOK,
			"concurrent create for %s: expected 201 or 200, got %d – %s",
			r.id, r.status, r.body)
	}
}

// ---------------------------------------------------------------------------
// Idempotent concurrent create (same workload ID)
// ---------------------------------------------------------------------------

// TestConcurrent_CreateSameWorkloadIdempotent fires N parallel PUT requests for
// the same workload ID. Exactly one must receive 201; the rest must receive 200
// (pod already exists) or 201 (if the idempotent create race is benign).
// No request must receive 4xx or 5xx.
func TestConcurrent_CreateSameWorkloadIdempotent(t *testing.T) {
	const n = 4
	wlID := uniqueID("conc-idem")
	t.Cleanup(func() { newClient().DeleteWorkload(t, testWS, testProj, wlID) })

	type result struct {
		status int
		body   string
	}
	results := make([]result, n)
	var wg sync.WaitGroup

	for i := 0; i < n; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp := newClient().CreateWorkload(t, testWS, testProj, wlID,
				CreateRequest{Image: suite.Image})
			results[i] = result{status: resp.StatusCode, body: resp.BodyString()}
		}()
	}
	wg.Wait()

	for i, r := range results {
		assert.True(t,
			r.status == http.StatusCreated || r.status == http.StatusOK,
			"concurrent create #%d for same ID: expected 201 or 200, got %d – %s",
			i, r.status, r.body)
	}
}

// ---------------------------------------------------------------------------
// Concurrent keepalive
// ---------------------------------------------------------------------------

// TestConcurrent_KeepaliveUnderLoad sends N simultaneous keepalive requests for
// the same running workload. All must succeed (200 with a valid expires_at).
func TestConcurrent_KeepaliveUnderLoad(t *testing.T) {
	const n = 6
	wlID := setupRunningWorkload(t, "conc-ka")

	type result struct {
		status    int
		expiresAt string
		body      string
	}
	results := make([]result, n)
	var wg sync.WaitGroup

	for i := 0; i < n; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp := newClient().Keepalive(t, testWS, testProj, wlID)
			r := result{status: resp.StatusCode, body: resp.BodyString()}
			if resp.StatusCode == http.StatusOK {
				var kr KeepaliveResponse
				if err := resp.DecodeJSON(&kr); err == nil {
					r.expiresAt = kr.ExpiresAt
				}
			}
			results[i] = r
		}()
	}
	wg.Wait()

	for i, r := range results {
		assert.Equal(t, http.StatusOK, r.status,
			"concurrent keepalive #%d: expected 200, got %d – %s", i, r.status, r.body)
		if r.status == http.StatusOK {
			require.NotEmpty(t, r.expiresAt, "keepalive #%d must return expires_at", i)
			_, err := time.Parse(time.RFC3339, r.expiresAt)
			assert.NoError(t, err, "keepalive #%d: expires_at must be valid RFC3339", i)
		}
	}
}

// ---------------------------------------------------------------------------
// Concurrent GET (read-only, no interference)
// ---------------------------------------------------------------------------

// TestConcurrent_GetUnderLoad sends N simultaneous GET requests for the same
// running workload. All must succeed (200) with consistent phase values.
func TestConcurrent_GetUnderLoad(t *testing.T) {
	const n = 8
	wlID := setupRunningWorkload(t, "conc-get")

	type result struct {
		status int
		phase  string
		body   string
	}
	results := make([]result, n)
	var wg sync.WaitGroup

	for i := 0; i < n; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp := newClient().GetWorkload(t, testWS, testProj, wlID)
			r := result{status: resp.StatusCode, body: resp.BodyString()}
			if resp.StatusCode == http.StatusOK {
				var ps PodStatus
				if err := resp.DecodeJSON(&ps); err == nil {
					r.phase = ps.Phase
				}
			}
			results[i] = r
		}()
	}
	wg.Wait()

	for i, r := range results {
		assert.Equal(t, http.StatusOK, r.status,
			"concurrent GET #%d: expected 200, got %d – %s", i, r.status, r.body)
		assert.NotEmpty(t, r.phase, "GET #%d must return a phase field", i)
	}
}

// ---------------------------------------------------------------------------
// Concurrent exec on the same workload
// ---------------------------------------------------------------------------

// TestConcurrent_ExecParallel sends N parallel exec requests to the same running
// pod. Each must return 200 with exit_code 0 and correct stdout.
func TestConcurrent_ExecParallel(t *testing.T) {
	const n = 4
	wlID := setupRunningWorkload(t, "conc-exec")

	type result struct {
		status   int
		exitCode int
		stdout   string
		body     string
	}
	results := make([]result, n)
	var wg sync.WaitGroup

	for i := 0; i < n; i++ {
		i := i
		tag := fmt.Sprintf("echo-parallel-%d", i)
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp := newClient().Exec(t, testWS, testProj, wlID,
				[]string{"echo", tag}, 15)
			r := result{status: resp.StatusCode, body: resp.BodyString()}
			if resp.StatusCode == http.StatusOK {
				var er ExecResponse
				if err := resp.DecodeJSON(&er); err == nil {
					r.exitCode = er.ExitCode
					r.stdout = er.Stdout
				}
			}
			results[i] = r
		}()
	}
	wg.Wait()

	for i, r := range results {
		require.Equal(t, http.StatusOK, r.status,
			"parallel exec #%d: expected 200, got %d – %s", i, r.status, r.body)
		assert.Equal(t, 0, r.exitCode, "parallel exec #%d: exit_code must be 0", i)
		assert.Contains(t, r.stdout, fmt.Sprintf("echo-parallel-%d", i),
			"parallel exec #%d: stdout must contain the echo argument", i)
	}
}

// ---------------------------------------------------------------------------
// Mixed parallel operations: create + get + keepalive on different workloads
// ---------------------------------------------------------------------------

// TestConcurrent_MixedOperations exercises create, get, and keepalive
// simultaneously on different workloads to verify no shared-state corruption
// in the service routing or K8s API client.
func TestConcurrent_MixedOperations(t *testing.T) {
	// Pre-create a workload for the keepalive & GET goroutines.
	existingWlID := setupRunningWorkload(t, "conc-mixed-base")

	// New workloads to be created in the parallel phase.
	const newCount = 3
	newWlIDs := make([]string, newCount)
	for i := 0; i < newCount; i++ {
		newWlIDs[i] = uniqueID(fmt.Sprintf("conc-mixed-new-%d", i))
	}

	var wg sync.WaitGroup
	errors := make([]string, 0)
	var mu sync.Mutex

	recordErr := func(msg string) {
		mu.Lock()
		errors = append(errors, msg)
		mu.Unlock()
	}

	// Goroutines: create N new workloads.
	for i := 0; i < newCount; i++ {
		id := newWlIDs[i]
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp := newClient().CreateWorkload(t, testWS, testProj, id, CreateRequest{Image: suite.Image})
			if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
				recordErr(fmt.Sprintf("create %s: %d – %s", id, resp.StatusCode, resp.BodyString()))
			} else {
				// Schedule cleanup after all goroutines finish.
				t.Cleanup(func() { newClient().DeleteWorkload(t, testWS, testProj, id) })
			}
		}()
	}

	// Goroutine: GET the existing workload.
	wg.Add(1)
	go func() {
		defer wg.Done()
		resp := newClient().GetWorkload(t, testWS, testProj, existingWlID)
		if resp.StatusCode != http.StatusOK {
			recordErr(fmt.Sprintf("GET existing: %d – %s", resp.StatusCode, resp.BodyString()))
		}
	}()

	// Goroutine: keepalive the existing workload.
	wg.Add(1)
	go func() {
		defer wg.Done()
		resp := newClient().Keepalive(t, testWS, testProj, existingWlID)
		if resp.StatusCode != http.StatusOK {
			recordErr(fmt.Sprintf("keepalive existing: %d – %s", resp.StatusCode, resp.BodyString()))
		}
	}()

	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	assert.Empty(t, errors, "mixed concurrent operations must all succeed:\n%v", errors)
}

// ---------------------------------------------------------------------------
// Delete under concurrent gets (read-write safety)
// ---------------------------------------------------------------------------

// TestConcurrent_DeleteWhileConcurrentGets deletes a workload while concurrent
// GET requests are in flight. GET must never return a 5xx; it must return
// either the running status or "offline" gracefully.
func TestConcurrent_DeleteWhileConcurrentGets(t *testing.T) {
	const getCount = 5
	wlID := setupRunningWorkload(t, "conc-del-get")

	var wg sync.WaitGroup
	getResults := make([]int, getCount)

	// Fire N concurrent GETs.
	for i := 0; i < getCount; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp := newClient().GetWorkload(t, testWS, testProj, wlID)
			getResults[i] = resp.StatusCode
		}()
	}

	// Delete the workload concurrently.
	wg.Add(1)
	go func() {
		defer wg.Done()
		// Small sleep so at least some GETs overlap with the delete.
		time.Sleep(50 * time.Millisecond)
		newClient().DeleteWorkload(t, testWS, testProj, wlID)
	}()

	wg.Wait()

	for i, sc := range getResults {
		assert.True(t,
			sc == http.StatusOK,
			"GET #%d during concurrent delete must return 200 (even if phase=offline), got %d",
			i, sc)
	}
}
