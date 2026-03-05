package k8s

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/sandbox/manager/internal/observability"
)

// GetPod retrieves a pod by name
func (c *Client) GetPod(ctx context.Context, name string) (*v1.Pod, error) {
	pod, err := c.clientset.CoreV1().Pods(c.namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get pod: %w", err)
	}
	return pod, nil
}

// PodExists checks if a pod exists
func (c *Client) PodExists(ctx context.Context, name string) (bool, error) {
	_, err := c.clientset.CoreV1().Pods(c.namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// IsPodReady checks if a pod is ready
func IsPodReady(pod *v1.Pod) bool {
	for _, condition := range pod.Status.Conditions {
		if condition.Type == v1.PodReady && condition.Status == v1.ConditionTrue {
			return true
		}
	}
	return false
}

// WaitForPodReady waits for a pod to become ready
func (c *Client) WaitForPodReady(ctx context.Context, name string, waitTime time.Duration, pollInterval time.Duration) (bool, error) {
	poller := observability.NewPoller(pollInterval, waitTime)

	err := poller.Poll(ctx, func() (bool, error) {
		pod, err := c.clientset.CoreV1().Pods(c.namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			if errors.IsNotFound(err) {
				return false, fmt.Errorf("pod %s not found", name)
			}
			return false, fmt.Errorf("failed to get pod: %w", err)
		}

		if pod.Status.Phase == v1.PodFailed {
			return false, fmt.Errorf("pod %s failed", name)
		}

		if IsPodReady(pod) {
			log.Printf("K8s: pod %s is ready", name)
			return true, nil
		}

		return false, nil
	})

	if err != nil {
		return false, err
	}
	return true, nil
}

// DeletePod deletes a pod
func (c *Client) DeletePod(ctx context.Context, name string, gracePeriodSeconds int64) error {
	deleteOpts := metav1.DeleteOptions{}
	if gracePeriodSeconds >= 0 {
		deleteOpts.GracePeriodSeconds = &gracePeriodSeconds
	}

	err := c.clientset.CoreV1().Pods(c.namespace).Delete(ctx, name, deleteOpts)
	if err != nil && !errors.IsNotFound(err) {
		return fmt.Errorf("failed to delete pod: %w", err)
	}

	log.Printf("K8s: deleted pod %s", name)
	return nil
}

// PatchActivity updates the activity timestamp and sets the absolute expiry time.
// The caller is responsible for capping expiresAt against maxExpiresAt.
func (c *Client) PatchActivity(ctx context.Context, name string, expiresAt time.Time) error {
	now := time.Now().UTC()

	patch := map[string]interface{}{
		"metadata": map[string]interface{}{
			"annotations": map[string]string{
				"last_activity_at": now.Format(time.RFC3339),
				"expires_at":       expiresAt.Format(time.RFC3339),
			},
		},
	}

	patchBytes, _ := json.Marshal(patch)
	_, err := c.clientset.CoreV1().Pods(c.namespace).Patch(ctx, name, "application/merge-patch+json", patchBytes, metav1.PatchOptions{})
	if err != nil {
		return fmt.Errorf("failed to patch pod: %w", err)
	}

	log.Printf("K8s: patched activity for pod %s (expiresAt=%s)", name, expiresAt.Format(time.RFC3339))
	return nil
}
