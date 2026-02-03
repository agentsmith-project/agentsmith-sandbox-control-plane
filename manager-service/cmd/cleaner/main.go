package main

import (
	"context"
	"flag"
	"fmt"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/klog/v2"
)

const (
	sandboxAppLabel    = "sandbox"
	expiresAtAnnotation = "expires_at"
)

func main() {
	// Parse command-line flags
	namespace := flag.String("namespace", "default", "Kubernetes namespace to scan for expired pods")
	dryRun := flag.Bool("dry-run", true, "If true, only print what would be deleted without actually deleting")
	flag.Parse()

	klog.Infof("Starting sandbox pod cleaner")
	klog.Infof("Namespace: %s", *namespace)
	klog.Infof("Dry-run: %v", *dryRun)

	// Create Kubernetes client
	config, err := rest.InClusterConfig()
	if err != nil {
		klog.Fatalf("Error creating in-cluster config: %v", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		klog.Fatalf("Error creating Kubernetes client: %v", err)
	}

	// Run the cleaner
	ctx := context.Background()
	if err := runCleaner(ctx, clientset, *namespace, *dryRun); err != nil {
		klog.Fatalf("Cleaner failed: %v", err)
	}

	klog.Infof("Cleaner completed successfully")
}

func runCleaner(ctx context.Context, clientset *kubernetes.Clientset, namespace string, dryRun bool) error {
	// List all pods with the sandbox app label
	pods, err := clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("app=%s", sandboxAppLabel),
	})
	if err != nil {
		return fmt.Errorf("failed to list pods: %w", err)
	}

	klog.Infof("Found %d pods with label app=%s", len(pods.Items), sandboxAppLabel)

	now := time.Now()
	expiredCount := 0
	skippedCount := 0

	for _, pod := range pods.Items {
		expiresAtStr, hasExpiry := pod.Annotations[expiresAtAnnotation]
		if !hasExpiry {
			klog.V(2).Infof("Pod %s/%s has no %s annotation, skipping", pod.Namespace, pod.Name, expiresAtAnnotation)
			skippedCount++
			continue
		}

		expiresAt, err := time.Parse(time.RFC3339, expiresAtStr)
		if err != nil {
			klog.Warningf("Pod %s/%s has invalid %s annotation (%q): %v, skipping",
				pod.Namespace, pod.Name, expiresAtAnnotation, expiresAtStr, err)
			skippedCount++
			continue
		}

		if now.After(expiresAt) {
			klog.Infof("Pod %s/%s expired at %s (now: %s)",
				pod.Namespace, pod.Name, expiresAt.Format(time.RFC3339), now.Format(time.RFC3339))

			if dryRun {
				klog.Infof("[DRY-RUN] Would delete pod %s/%s", pod.Namespace, pod.Name)
			} else {
				klog.Infof("Deleting pod %s/%s", pod.Namespace, pod.Name)
				if err := clientset.CoreV1().Pods(pod.Namespace).Delete(ctx, pod.Name, metav1.DeleteOptions{}); err != nil {
					klog.Errorf("Failed to delete pod %s/%s: %v", pod.Namespace, pod.Name, err)
					continue
				}
				klog.Infof("Successfully deleted pod %s/%s", pod.Namespace, pod.Name)
			}
			expiredCount++
		} else {
			klog.V(2).Infof("Pod %s/%s has not expired yet (expires: %s)",
				pod.Namespace, pod.Name, expiresAt.Format(time.RFC3339))
		}
	}

	klog.Infof("Scan complete: %d expired, %d skipped, %d total pods",
		expiredCount, skippedCount, len(pods.Items))

	return nil
}

func init() {
	// Configure klog to use stderr
	flag.Set("logtostderr", "true")
	flag.Set("alsologtostderr", "false")
}
