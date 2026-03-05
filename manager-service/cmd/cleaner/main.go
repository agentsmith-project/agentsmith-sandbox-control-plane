package main

import (
	"context"
	"flag"
	"fmt"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog/v2"
)

const (
	sandboxAppLabel     = "sandbox"
	workloadAppLabel    = "managed-workload"
	expiresAtAnnotation = "expires_at"
)

// Cleaner deletes pods whose expires_at annotation is in the past. Activity is driven by
// client keepalive; expired pods are deleted directly—resource lifecycle is manager-controlled.
func main() {
	namespace := flag.String("namespace", "default", "Kubernetes namespace to scan for expired pods")
	dryRun := flag.Bool("dry-run", true, "If true, only print what would be deleted without actually deleting")
	logLevel := flag.String("log-level", "info", "Log level: debug, info, warn, error")
	kubeconfig := flag.String("kubeconfig", "", "Path to kubeconfig file (defaults to in-cluster config)")
	flag.Parse()

	switch *logLevel {
	case "debug":
		flag.Set("v", "4")
	case "warn", "error":
		flag.Set("v", "0")
	default:
		flag.Set("v", "2")
	}

	klog.Infof("Starting sandbox pod cleaner")
	klog.Infof("Namespace: %s, dry-run: %v", *namespace, *dryRun)

	var config *rest.Config
	var err error
	if *kubeconfig != "" {
		config, err = clientcmd.BuildConfigFromFlags("", *kubeconfig)
	} else {
		config, err = rest.InClusterConfig()
	}
	if err != nil {
		klog.Fatalf("Error creating Kubernetes config: %v", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		klog.Fatalf("Error creating Kubernetes client: %v", err)
	}

	ctx := context.Background()

	if err := runCleaner(ctx, clientset, *namespace, *dryRun); err != nil {
		klog.Errorf("Sandbox cleaner failed: %v", err)
	}

	if err := runWorkloadCleaner(ctx, clientset, *namespace, *dryRun); err != nil {
		klog.Errorf("Workload cleaner failed: %v", err)
	}

	klog.Infof("Cleaner completed successfully")
}

func runCleaner(ctx context.Context, clientset *kubernetes.Clientset, namespace string, dryRun bool) error {
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

func runWorkloadCleaner(ctx context.Context, clientset *kubernetes.Clientset, namespace string, dryRun bool) error {
	pods, err := clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("app=%s", workloadAppLabel),
	})
	if err != nil {
		return fmt.Errorf("failed to list workload pods: %w", err)
	}

	klog.Infof("Found %d workload pods (app=%s)", len(pods.Items), workloadAppLabel)

	now := time.Now()
	expiredCount := 0

	for _, pod := range pods.Items {
		expiresAtStr, hasExpiry := pod.Annotations[expiresAtAnnotation]
		if !hasExpiry {
			continue
		}

		expiresAt, err := time.Parse(time.RFC3339, expiresAtStr)
		if err != nil {
			klog.Warningf("Workload pod %s: invalid expires_at %q: %v", pod.Name, expiresAtStr, err)
			continue
		}

		if !now.After(expiresAt) {
			continue
		}

		klog.Infof("Workload pod %s expired at %s (no keepalive)", pod.Name, expiresAt.Format(time.RFC3339))

		if dryRun {
			klog.Infof("[DRY-RUN] Would delete workload pod %s", pod.Name)
			expiredCount++
			continue
		}

		klog.Infof("Deleting workload pod %s", pod.Name)
		if err := clientset.CoreV1().Pods(pod.Namespace).Delete(ctx, pod.Name, metav1.DeleteOptions{}); err != nil {
			klog.Errorf("Failed to delete workload pod %s: %v", pod.Name, err)
			continue
		}
		klog.Infof("Successfully deleted workload pod %s", pod.Name)
		expiredCount++
	}

	klog.Infof("Workload scan complete: %d expired out of %d total", expiredCount, len(pods.Items))
	return nil
}

func init() {
	flag.Set("logtostderr", "true")
	flag.Set("alsologtostderr", "false")
}
