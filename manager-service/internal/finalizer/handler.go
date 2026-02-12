package finalizer

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	v1 "k8s.io/api/core/v1"

	"github.com/sandbox/manager/internal/k8s"
	"github.com/sandbox/manager/internal/storage"
)

const (
	// SnapshotFinalizer is the finalizer added to pods for snapshot handling
	SnapshotFinalizer = "manager.mbos.io/snapshot"

	// DefaultCheckInterval is the default interval between checks for pods to process
	DefaultCheckInterval = 10 * time.Second

	// DefaultSnapshotTimeout is the default timeout for snapshot operations
	DefaultSnapshotTimeout = 5 * time.Minute

	// maxRemoveFinalizerRetries is the maximum number of retries for removing a finalizer
	maxRemoveFinalizerRetries = 3

	// removeFinalizerBaseBackoff is the base backoff duration for finalizer removal retries
	removeFinalizerBaseBackoff = 100 * time.Millisecond

	// maxSnapshotRetries is the maximum number of retries for snapshot operations per cycle
	maxSnapshotRetries = 3

	// snapshotBaseBackoff is the base backoff duration for snapshot retries
	snapshotBaseBackoff = 500 * time.Millisecond

	// maxTotalSnapshotAttempts is the total number of snapshot attempts across all
	// processing cycles before the finalizer is forcibly removed. This prevents
	// pods from being stuck in Terminating state indefinitely when snapshots
	// permanently fail (e.g., MinIO unreachable, pod containers terminated).
	maxTotalSnapshotAttempts = 10
)

// Handler handles finalizers for pods
type Handler struct {
	k8sClient       *k8s.Client
	storageClient   *storage.Client
	namespace       string
	checkInterval   time.Duration
	snapshotTimeout time.Duration
	wg              sync.WaitGroup
	stopCh          chan struct{}
	stopOnce        sync.Once

	// failCounts tracks the total number of failed snapshot attempts per pod
	// across processing cycles. Protected by failMu.
	failMu     sync.Mutex
	failCounts map[string]int
}

// HandlerConfig contains configuration for creating a new Handler
type HandlerConfig struct {
	K8sClient       *k8s.Client
	StorageClient   *storage.Client
	Namespace       string
	CheckInterval   time.Duration
	SnapshotTimeout time.Duration
}

// NewHandler creates a new finalizer handler
func NewHandler(cfg *HandlerConfig) (*Handler, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config cannot be nil")
	}
	if cfg.K8sClient == nil {
		return nil, fmt.Errorf("k8s client cannot be nil")
	}
	if cfg.StorageClient == nil {
		return nil, fmt.Errorf("storage client cannot be nil")
	}
	if cfg.Namespace == "" {
		cfg.Namespace = cfg.K8sClient.Namespace()
	}

	checkInterval := cfg.CheckInterval
	if checkInterval == 0 {
		checkInterval = DefaultCheckInterval
	}

	snapshotTimeout := cfg.SnapshotTimeout
	if snapshotTimeout == 0 {
		snapshotTimeout = DefaultSnapshotTimeout
	}

	return &Handler{
		k8sClient:       cfg.K8sClient,
		storageClient:   cfg.StorageClient,
		namespace:       cfg.Namespace,
		checkInterval:   checkInterval,
		snapshotTimeout: snapshotTimeout,
		stopCh:          make(chan struct{}),
		failCounts:      make(map[string]int),
	}, nil
}

// Start starts the finalizer handler in a background goroutine.
// Start returns immediately after launching the worker goroutine.
// Callers should NOT wrap this in another goroutine.
func (h *Handler) Start(ctx context.Context) {
	log.Printf("Finalizer: starting handler (namespace=%s, interval=%v)", h.namespace, h.checkInterval)

	h.wg.Add(1)
	go func() {
		defer h.wg.Done()
		ticker := time.NewTicker(h.checkInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				log.Printf("Finalizer: stopping handler due to context cancellation")
				return
			case <-h.stopCh:
				log.Printf("Finalizer: stopping handler due to stop signal")
				return
			case <-ticker.C:
				if err := h.processPods(ctx); err != nil {
					log.Printf("Finalizer: error processing pods: %v", err)
				}
			}
		}
	}()
}

// Shutdown gracefully stops the finalizer handler with a timeout.
// It is safe to call Shutdown multiple times.
func (h *Handler) Shutdown(ctx context.Context) error {
	// Signal the goroutine to stop (idempotent via sync.Once)
	h.stopOnce.Do(func() {
		close(h.stopCh)
	})

	// Wait for the goroutine to finish with timeout
	done := make(chan struct{})
	go func() {
		h.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		log.Printf("Finalizer: handler stopped gracefully")
		return nil
	case <-ctx.Done():
		return fmt.Errorf("finalizer shutdown timed out: %w", ctx.Err())
	}
}

// processPods processes all pods with the snapshot finalizer that are being deleted
func (h *Handler) processPods(ctx context.Context) error {
	// List all pods with the snapshot finalizer
	pods, err := h.k8sClient.ListPodsWithFinalizer(ctx, h.namespace, SnapshotFinalizer)
	if err != nil {
		return fmt.Errorf("failed to list pods with finalizer: %w", err)
	}

	// Process each pod that has a deletion timestamp
	for _, pod := range pods {
		if pod.DeletionTimestamp == nil {
			// Pod is not being deleted, skip it
			continue
		}

		// Process the pod
		if err := h.processPod(ctx, &pod); err != nil {
			log.Printf("Finalizer: error processing pod %s: %v", pod.Name, err)
			// Continue to next pod - we don't want one failure to block others
		}
	}

	return nil
}

// processPod processes a single pod with the snapshot finalizer.
//
// If the snapshot succeeds, the finalizer is removed normally.
// If the snapshot fails, a cross-cycle failure counter is incremented.
// Once the counter exceeds maxTotalSnapshotAttempts, the finalizer is
// forcibly removed to prevent pods from being stuck in Terminating state
// indefinitely (which would otherwise cause CrashLoopBackOff of the Manager).
func (h *Handler) processPod(ctx context.Context, pod *v1.Pod) error {
	podName := pod.Name
	log.Printf("Finalizer: processing pod %s", podName)

	// Check if pod containers are still running; skip snapshot if not.
	if !isPodContainersRunning(pod) {
		log.Printf("Finalizer: pod %s containers not running, skipping snapshot and removing finalizer", podName)
		return h.forceRemoveFinalizer(ctx, podName, "containers not running")
	}

	// Extract sandbox ID for snapshot storage; without it we do not store (no restorable key).
	sandboxID := h.getSandboxID(pod)
	if sandboxID == "" {
		log.Printf("Finalizer: pod %s has no sandbox/sessionId annotation, skipping snapshot and removing finalizer", podName)
		return h.forceRemoveFinalizer(ctx, podName, "no sessionId annotation")
	}

	// Create and upload snapshot with retry logic
	if err := h.snapshotWorkspaceWithRetry(ctx, podName, sandboxID); err != nil {
		log.Printf("Finalizer: snapshot failed for pod %s: %v", podName, err)

		// Increment cross-cycle failure counter
		h.failMu.Lock()
		h.failCounts[podName]++
		totalAttempts := h.failCounts[podName]
		h.failMu.Unlock()

		if totalAttempts >= maxTotalSnapshotAttempts {
			log.Printf("Finalizer: WARNING: pod %s exceeded %d total snapshot attempts, FORCIBLY removing finalizer (DATA LOSS)", podName, maxTotalSnapshotAttempts)
			return h.forceRemoveFinalizer(ctx, podName, "max attempts exceeded")
		}

		return fmt.Errorf("snapshot failed (attempt %d/%d total): %w", totalAttempts, maxTotalSnapshotAttempts, err)
	}

	// Snapshot succeeded — remove the finalizer normally
	if err := h.removeFinalizerWithRetry(ctx, podName); err != nil {
		return fmt.Errorf("failed to remove finalizer: %w", err)
	}

	// Clean up the failure counter on success
	h.clearFailCount(podName)

	log.Printf("Finalizer: completed processing pod %s", podName)
	return nil
}

// forceRemoveFinalizer forcibly removes the finalizer from a pod,
// accepting potential data loss. Used when snapshot permanently fails.
func (h *Handler) forceRemoveFinalizer(ctx context.Context, podName, reason string) error {
	if err := h.removeFinalizerWithRetry(ctx, podName); err != nil {
		return fmt.Errorf("failed to force-remove finalizer for pod %s (%s): %w", podName, reason, err)
	}
	h.clearFailCount(podName)
	log.Printf("Finalizer: forcibly removed finalizer for pod %s (reason: %s)", podName, reason)
	return nil
}

// clearFailCount removes the failure counter for a pod.
func (h *Handler) clearFailCount(podName string) {
	h.failMu.Lock()
	delete(h.failCounts, podName)
	h.failMu.Unlock()
}

// isPodContainersRunning checks if at least one container in the pod is running.
// If all containers have terminated, snapshot will fail, so we skip it.
func isPodContainersRunning(pod *v1.Pod) bool {
	if pod.Status.Phase == v1.PodFailed || pod.Status.Phase == v1.PodSucceeded {
		return false
	}
	for _, cs := range pod.Status.ContainerStatuses {
		if cs.State.Running != nil {
			return true
		}
	}
	// No running containers found — check if there are any container statuses at all
	// (a pod may not have statuses yet if containers haven't started)
	return len(pod.Status.ContainerStatuses) == 0
}

// removeFinalizerWithRetry removes the finalizer with retry logic
func (h *Handler) removeFinalizerWithRetry(ctx context.Context, podName string) error {
	var lastErr error
	backoff := removeFinalizerBaseBackoff

	for attempt := 1; attempt <= maxRemoveFinalizerRetries; attempt++ {
		err := h.k8sClient.RemoveFinalizer(ctx, h.namespace, podName, SnapshotFinalizer)
		if err == nil {
			// Success
			if attempt > 1 {
				log.Printf("Finalizer: successfully removed finalizer for pod %s on attempt %d", podName, attempt)
			}
			return nil
		}

		lastErr = err

		// Log retry attempt
		if attempt < maxRemoveFinalizerRetries {
			log.Printf("Finalizer: attempt %d failed to remove finalizer for pod %s: %v, retrying in %v", attempt, podName, err, backoff)
			// Wait with exponential backoff, checking for context cancellation
			select {
			case <-ctx.Done():
				return fmt.Errorf("context cancelled during retry backoff: %w", ctx.Err())
			case <-time.After(backoff):
				// Continue to next retry
			}
			backoff *= 2
		}
	}

	// All retries failed
	return fmt.Errorf("failed to remove finalizer after %d attempts: %w", maxRemoveFinalizerRetries, lastErr)
}

// snapshotWorkspaceWithRetry creates a snapshot with retry logic
func (h *Handler) snapshotWorkspaceWithRetry(ctx context.Context, podName, sandboxID string) error {
	var lastErr error
	backoff := snapshotBaseBackoff

	for attempt := 1; attempt <= maxSnapshotRetries; attempt++ {
		// Create a context with timeout for each snapshot attempt
		snapshotCtx, cancel := context.WithTimeout(ctx, h.snapshotTimeout)

		err := h.snapshotWorkspace(snapshotCtx, podName, sandboxID)
		cancel() // Always cancel the context

		if err == nil {
			// Success
			if attempt > 1 {
				log.Printf("Finalizer: successfully created snapshot for pod %s on attempt %d", podName, attempt)
			}
			return nil
		}

		lastErr = err

		// Log retry attempt
		if attempt < maxSnapshotRetries {
			log.Printf("Finalizer: attempt %d failed to snapshot workspace for pod %s: %v, retrying in %v", attempt, podName, err, backoff)
			// Wait with exponential backoff, checking for context cancellation
			select {
			case <-ctx.Done():
				return fmt.Errorf("context cancelled during snapshot retry backoff: %w", ctx.Err())
			case <-time.After(backoff):
				// Continue to next retry
			}
			backoff *= 2
		}
	}

	// All retries failed
	return fmt.Errorf("failed to create snapshot after %d attempts: %w", maxSnapshotRetries, lastErr)
}

// snapshotWorkspace creates a snapshot of the workspace and uploads it to storage
func (h *Handler) snapshotWorkspace(ctx context.Context, podName, sandboxID string) error {
	// Create a snapshot of the workspace
	snapshot, err := h.k8sClient.SnapshotWorkspace(ctx, h.namespace, podName)
	if err != nil {
		return fmt.Errorf("failed to create snapshot: %w", err)
	}
	defer snapshot.Close()

	// Generate the storage key: snapshots/{sandboxID}/{timestamp}.tar.gz
	key, err := h.storageClient.GenerateSnapshotKey(sandboxID)
	if err != nil {
		return fmt.Errorf("failed to generate snapshot key: %w", err)
	}
	log.Printf("Finalizer: uploading snapshot for pod %s (sandbox=%s) to %s", podName, sandboxID, key)

	// Note: We don't know the size ahead of time, so we pass -1
	// The storage client should handle this appropriately
	if err := h.storageClient.UploadSnapshot(ctx, key, snapshot, -1); err != nil {
		return fmt.Errorf("failed to upload snapshot: %w", err)
	}

	log.Printf("Finalizer: successfully uploaded snapshot for pod %s", podName)
	return nil
}

// getSandboxID extracts the sandbox ID from a pod.
// Reads from the sandbox/sessionId annotation set during pod creation.
// Returns empty string if the annotation is missing; in that case we do not store a snapshot
// (no fallback to pod name, since that key would not be restorable by session ID).
func (h *Handler) getSandboxID(pod *v1.Pod) string {
	if pod.Annotations != nil {
		if id, ok := pod.Annotations["sandbox/sessionId"]; ok && id != "" {
			return id
		}
	}
	return ""
}
