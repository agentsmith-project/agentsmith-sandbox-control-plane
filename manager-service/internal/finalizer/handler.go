package finalizer

import (
	"context"
	"fmt"
	"log"
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
)

// Handler handles finalizers for pods
type Handler struct {
	k8sClient       *k8s.Client
	storageClient   *storage.Client
	namespace       string
	checkInterval   time.Duration
	snapshotTimeout time.Duration
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
	}, nil
}

// Start starts the finalizer handler in a background goroutine
func (h *Handler) Start(ctx context.Context) {
	log.Printf("Finalizer: starting handler (namespace=%s, interval=%v)", h.namespace, h.checkInterval)

	ticker := time.NewTicker(h.checkInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Printf("Finalizer: stopping handler")
			return
		case <-ticker.C:
			if err := h.processPods(ctx); err != nil {
				log.Printf("Finalizer: error processing pods: %v", err)
			}
		}
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

// processPod processes a single pod with the snapshot finalizer
func (h *Handler) processPod(ctx context.Context, pod *v1.Pod) error {
	podName := pod.Name
	log.Printf("Finalizer: processing pod %s", podName)

	// Extract metadata for the snapshot
	workspaceID := h.getWorkspaceID(pod)
	projectID := h.getProjectID(pod)
	agentThreadID := h.getAgentThreadID(pod)

	// Create a context with timeout for the snapshot operation
	snapshotCtx, cancel := context.WithTimeout(ctx, h.snapshotTimeout)
	defer cancel()

	// Create and upload snapshot
	if err := h.snapshotWorkspace(snapshotCtx, podName, workspaceID, projectID, agentThreadID); err != nil {
		log.Printf("Finalizer: failed to snapshot workspace for pod %s: %v", podName, err)
		// Continue with finalizer removal even if snapshot fails
	}

	// Remove the finalizer so the pod can be deleted
	if err := h.removeFinalizerWithRetry(ctx, podName); err != nil {
		return fmt.Errorf("failed to remove finalizer: %w", err)
	}

	log.Printf("Finalizer: completed processing pod %s", podName)
	return nil
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

// snapshotWorkspace creates a snapshot of the workspace and uploads it to storage
func (h *Handler) snapshotWorkspace(ctx context.Context, podName, workspaceID, projectID, agentThreadID string) error {
	// Create a snapshot of the workspace
	snapshot, err := h.k8sClient.SnapshotWorkspace(ctx, h.namespace, podName)
	if err != nil {
		return fmt.Errorf("failed to create snapshot: %w", err)
	}
	defer snapshot.Close()

	// Generate the storage key for the snapshot
	key := h.storageClient.GenerateSnapshotKey(workspaceID, projectID, agentThreadID)
	log.Printf("Finalizer: uploading snapshot for pod %s to %s", podName, key)

	// Note: We don't know the size ahead of time, so we pass -1
	// The storage client should handle this appropriately
	if err := h.storageClient.UploadSnapshot(ctx, key, snapshot, -1); err != nil {
		return fmt.Errorf("failed to upload snapshot: %w", err)
	}

	log.Printf("Finalizer: successfully uploaded snapshot for pod %s", podName)
	return nil
}

// getWorkspaceID extracts the workspace ID from a pod
func (h *Handler) getWorkspaceID(pod *v1.Pod) string {
	// Try to get workspace ID from annotations first
	if pod.Annotations != nil {
		if wsID, ok := pod.Annotations["workspace_id"]; ok {
			return wsID
		}
	}

	// Try to get from labels
	if pod.Labels != nil {
		if wsID, ok := pod.Labels["workspace_id"]; ok {
			return wsID
		}
	}

	// Fall back to using the pod name
	return fmt.Sprintf("ws_%s", pod.Name)
}

// getProjectID extracts the project ID from a pod
func (h *Handler) getProjectID(pod *v1.Pod) string {
	// Try to get project ID from annotations first
	if pod.Annotations != nil {
		if projID, ok := pod.Annotations["project_id"]; ok {
			return projID
		}
	}

	// Try to get from labels
	if pod.Labels != nil {
		if projID, ok := pod.Labels["project_id"]; ok {
			return projID
		}
	}

	// Fall back to a default value
	return "proj_default"
}

// getAgentThreadID extracts the agent thread ID from a pod
func (h *Handler) getAgentThreadID(pod *v1.Pod) string {
	// Try to get from labels
	if pod.Labels != nil {
		if atID, ok := pod.Labels["agent_thread_id"]; ok {
			return atID
		}
	}

	// Fall back to using the pod name
	return fmt.Sprintf("at_%s", pod.Name)
}
