package k8s

import (
	"context"
	"fmt"
	"log"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ListPodsWithFinalizer lists all pods in the namespace that have the specified finalizer
func (c *Client) ListPodsWithFinalizer(ctx context.Context, namespace, finalizer string) ([]v1.Pod, error) {
	podList, err := c.clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list pods: %w", err)
	}

	var pods []v1.Pod
	for _, pod := range podList.Items {
		if hasFinalizer(&pod, finalizer) {
			pods = append(pods, pod)
		}
	}

	return pods, nil
}

// RemoveFinalizer removes a finalizer from a pod
func (c *Client) RemoveFinalizer(ctx context.Context, namespace, podName, finalizer string) error {
	// Get the current pod
	pod, err := c.clientset.CoreV1().Pods(namespace).Get(ctx, podName, metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			// Pod is already gone, nothing to do
			log.Printf("K8s: pod %s/%s not found when removing finalizer", namespace, podName)
			return nil
		}
		return fmt.Errorf("failed to get pod: %w", err)
	}

	// Check if the pod has the finalizer
	if !hasFinalizer(pod, finalizer) {
		log.Printf("K8s: pod %s/%s does not have finalizer %s", namespace, podName, finalizer)
		return nil
	}

	// Remove the finalizer
	pod.Finalizers = removeFinalizerFromStringSlice(pod.Finalizers, finalizer)

	// Update the pod
	_, err = c.clientset.CoreV1().Pods(namespace).Update(ctx, pod, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to update pod: %w", err)
	}

	log.Printf("K8s: removed finalizer %s from pod %s/%s", finalizer, namespace, podName)
	return nil
}

// hasFinalizer checks if a pod has a specific finalizer
func hasFinalizer(pod *v1.Pod, finalizer string) bool {
	for _, f := range pod.Finalizers {
		if f == finalizer {
			return true
		}
	}
	return false
}

// removeFinalizerFromStringSlice removes a finalizer from a slice of finalizers
func removeFinalizerFromStringSlice(finalizers []string, finalizer string) []string {
	newFinalizers := make([]string, 0, len(finalizers))
	for _, f := range finalizers {
		if f != finalizer {
			newFinalizers = append(newFinalizers, f)
		}
	}
	return newFinalizers
}
