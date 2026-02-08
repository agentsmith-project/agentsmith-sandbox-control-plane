package reconciliation

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/sandbox/manager/internal/buffer"
	"github.com/sandbox/manager/internal/k8s"
	"github.com/sandbox/manager/internal/observability"
	"github.com/sandbox/manager/internal/session"
)

// Reconciler manages the reconciliation of pods and buffers with sessions
type Reconciler struct {
	sessionManager *session.Manager
	k8sClient      k8s.Client
	bufferManager  *buffer.Manager
	interval       time.Duration
	logger         observability.Logger
	stopChan       chan struct{}
	wg             sync.WaitGroup
	mu             sync.Mutex
	running        bool
}

// NewReconciler creates a new reconciler
func NewReconciler(sessionManager *session.Manager, k8sClient k8s.Client, bufferManager ...*buffer.Manager) *Reconciler {
	r := &Reconciler{
		sessionManager: sessionManager,
		k8sClient:      k8sClient,
		interval:       5 * time.Minute,
		logger:         observability.GetLogger(),
		stopChan:       make(chan struct{}),
	}

	if len(bufferManager) > 0 {
		r.bufferManager = bufferManager[0]
	}

	return r
}

// Start begins the reconciliation process
func (r *Reconciler) Start(ctx context.Context) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.running {
		return
	}

	r.running = true
	r.logger.Info("Starting reconciler")

	r.wg.Add(1)
	go r.run(ctx)
}

// Stop stops the reconciliation process
func (r *Reconciler) Stop() {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.running {
		return
	}

	r.running = false
	close(r.stopChan)
	r.wg.Wait()
	r.logger.Info("Reconciler stopped")
}

// run is the main reconciliation loop
func (r *Reconciler) run(ctx context.Context) {
	defer r.wg.Done()

	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			// Perform reconciliation
			if err := r.reconcile(ctx); err != nil {
				r.logger.Error("Reconciliation failed: %v", err)
			}
		case <-r.stopChan:
			return
		case <-ctx.Done():
			return
		}
	}
}

// reconcile performs the full reconciliation process
func (r *Reconciler) reconcile(ctx context.Context) error {
	// Clean up orphaned pods
	if err := r.cleanupOrphanedPods(ctx); err != nil {
		return fmt.Errorf("failed to clean up orphaned pods: %w", err)
	}

	// Clean up orphaned buffers if buffer manager is available
	if r.bufferManager != nil {
		if err := r.cleanupOrphanedBuffers(ctx); err != nil {
			return fmt.Errorf("failed to clean up orphaned buffers: %w", err)
		}
	}

	r.logger.Info("Reconciliation completed successfully")
	return nil
}

// cleanupOrphanedPods deletes pods that don't have corresponding sessions
func (r *Reconciler) cleanupOrphanedPods(ctx context.Context) error {
	r.logger.Info("Starting cleanup of orphaned pods")

	// Get all pods
	pods, err := r.k8sClient.ListSandboxPods(ctx)
	if err != nil {
		return err
	}

	// Get all active sessions (this would need to be implemented on the session manager)
	// For now, we'll implement a simpler version that just logs
	sessionIDs := make(map[string]bool)
	// TODO: Add method to session manager to get all session IDs
	// For now, we'll just delete pods without session IDs

	// Check each pod
	var deletedPods []string
	for _, pod := range pods {
		// Skip pods that don't have the session ID annotation
		sessionID := k8s.GetSessionIDFromPod(pod)
		if sessionID == "" {
			r.logger.Warn("Pod %s has no session ID annotation, skipping", pod.Name)
			continue
		}

		// For now, we'll assume all pods with session IDs are valid
		// TODO: Implement session existence check when session manager exposes it
		if !sessionIDs[sessionID] {
			r.logger.Info("Deleting orphaned pod %s (session %s)", pod.Name, sessionID)
			if err := r.k8sClient.DeletePod(ctx, pod.Name, 0); err != nil {
				r.logger.Error("Failed to delete orphaned pod %s: %v", pod.Name, err)
				continue
			}
			deletedPods = append(deletedPods, pod.Name)
		}
	}

	if len(deletedPods) > 0 {
		r.logger.Info("Deleted %d orphaned pods: %v", len(deletedPods), deletedPods)
	} else {
		r.logger.Info("No orphaned pods found")
	}

	return nil
}

// cleanupOrphanedBuffers deletes buffers that don't have corresponding sessions
func (r *Reconciler) cleanupOrphanedBuffers(ctx context.Context) error {
	if r.bufferManager == nil {
		return nil
	}

	r.logger.Info("Starting cleanup of orphaned buffers")

	// Get all buffer IDs
	bufferIDs := r.bufferManager.List()

	// Get all active sessions (this would need to be implemented on the session manager)
	// For now, we'll implement a simpler version
	sessionIDs := make(map[string]bool)
	// TODO: Add method to session manager to get all session IDs
	// For now, we'll just clean up buffers that don't match session IDs

	// Check each buffer
	var deletedBuffers []string
	for _, bufferID := range bufferIDs {
		// Check if buffer corresponds to a session
		if !sessionIDs[bufferID] {
			r.logger.Info("Deleting orphaned buffer %s", bufferID)
			r.bufferManager.Delete(bufferID)
			deletedBuffers = append(deletedBuffers, bufferID)
		}
	}

	if len(deletedBuffers) > 0 {
		r.logger.Info("Deleted %d orphaned buffers: %v", len(deletedBuffers), deletedBuffers)
	} else {
		r.logger.Info("No orphaned buffers found")
	}

	return nil
}

// GetStatus returns the current status of the reconciler
func (r *Reconciler) GetStatus() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.running
}

// GetInterval returns the reconciliation interval
func (r *Reconciler) GetInterval() time.Duration {
	return r.interval
}

// SetInterval sets the reconciliation interval
func (r *Reconciler) SetInterval(interval time.Duration) {
	r.interval = interval
}