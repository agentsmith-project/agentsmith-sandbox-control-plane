package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/klog/v2"
)

const (
	sandboxAppLabel       = "llm-sandbox"
	expiresAtAnnotation   = "expires_at"
	sessionIDAnnotation  = "sandbox/sessionId"

	// defaultFallbackTTL is the fallback TTL for pods with invalid or missing expires_at annotations
	defaultFallbackTTL = 7 * 24 * time.Hour

	// defaultManagerDeleteTimeout is the timeout for calling Manager DELETE API
	defaultManagerDeleteTimeout = 2 * time.Minute
)

// allowedNamespaces is a whitelist of namespaces that the cleaner is allowed to clean.
// Only the sandbox namespace runs sandbox pods; sandbox-system and sandbox-workspaces
// do not and must not be managed by the cleaner.
var allowedNamespaces = map[string]bool{
	"sandbox": true,
}

func isNamespaceAllowed(ns string) bool {
	return allowedNamespaces[ns]
}

func getAllowedNamespaceList() []string {
	namespaces := make([]string, 0, len(allowedNamespaces))
	for ns := range allowedNamespaces {
		namespaces = append(namespaces, ns)
	}
	sort.Strings(namespaces)
	return namespaces
}

func main() {
	namespace := flag.String("namespace", "default", "Kubernetes namespace to scan for expired pods")
	dryRun := flag.Bool("dry-run", false, "If true, only print what would be deleted without actually deleting")
	managerURL := flag.String("manager-url", os.Getenv("MANAGER_URL"), "Manager service URL. Defaults to MANAGER_URL env. Required for snapshot-before-delete.")
	flag.Parse()

	klog.Infof("Starting sandbox pod cleaner")
	klog.Infof("Namespace: %s", *namespace)
	klog.Infof("Dry-run: %v", *dryRun)
	klog.Infof("Manager URL: %s", *managerURL)

	config, err := rest.InClusterConfig()
	if err != nil {
		klog.Fatalf("Error creating in-cluster config: %v", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		klog.Fatalf("Error creating Kubernetes client: %v", err)
	}

	ctx := context.Background()
	if err := runCleaner(ctx, clientset, *namespace, *dryRun, *managerURL); err != nil {
		klog.Fatalf("Cleaner failed: %v", err)
	}

	klog.Infof("Cleaner completed successfully")
}

func runCleaner(ctx context.Context, clientset *kubernetes.Clientset, namespace string, dryRun bool, managerURL string) error {
	if !isNamespaceAllowed(namespace) {
		return fmt.Errorf("namespace %q is not in the allowed whitelist. Allowed: %v", namespace, getAllowedNamespaceList())
	}

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
	invalidTTLFallbackCount := 0
	useManager := managerURL != ""

	for _, pod := range pods.Items {
		expiresAtStr, hasExpiry := pod.Annotations[expiresAtAnnotation]
		if !hasExpiry {
			klog.V(2).Infof("Pod %s/%s has no %s annotation, skipping", pod.Namespace, pod.Name, expiresAtAnnotation)
			skippedCount++
			continue
		}

		expiresAt, err := time.Parse(time.RFC3339, expiresAtStr)
		if err != nil {
			if pod.CreationTimestamp.IsZero() {
				klog.Warningf("Pod %s/%s has zero creation timestamp, skipping", pod.Namespace, pod.Name)
				skippedCount++
				continue
			}
			fallbackExpiresAt := pod.CreationTimestamp.Add(defaultFallbackTTL)
			if now.After(fallbackExpiresAt) {
				klog.Infof("Pod %s/%s exceeded fallback TTL (invalid %s)", pod.Namespace, pod.Name, expiresAtAnnotation)
				if !dryRun {
					if err := deleteExpiredPod(ctx, clientset, &pod, useManager, managerURL, ""); err != nil {
						klog.Errorf("Failed to delete pod %s/%s: %v", pod.Namespace, pod.Name, err)
					}
				}
				invalidTTLFallbackCount++
			} else {
				skippedCount++
			}
			continue
		}

		if !now.After(expiresAt) {
			klog.V(2).Infof("Pod %s/%s has not expired yet (expires: %s)", pod.Namespace, pod.Name, expiresAt.Format(time.RFC3339))
			continue
		}

		klog.Infof("Pod %s/%s expired at %s (now: %s)", pod.Namespace, pod.Name, expiresAt.Format(time.RFC3339), now.Format(time.RFC3339))

		if dryRun {
			klog.Infof("[DRY-RUN] Would delete pod %s/%s", pod.Namespace, pod.Name)
			expiredCount++
			continue
		}

		sessionID := getSessionID(&pod)
		if err := deleteExpiredPod(ctx, clientset, &pod, useManager, managerURL, sessionID); err != nil {
			klog.Errorf("Failed to delete pod %s/%s: %v", pod.Namespace, pod.Name, err)
			continue
		}
		klog.Infof("Successfully deleted pod %s/%s", pod.Namespace, pod.Name)
		expiredCount++
	}

	klog.Infof("Scan complete: %d expired, %d invalid TTL (deleted via fallback), %d skipped, %d total pods",
		expiredCount, invalidTTLFallbackCount, skippedCount, len(pods.Items))

	return nil
}

func getSessionID(pod *v1.Pod) string {
	if pod.Annotations != nil {
		if id, ok := pod.Annotations[sessionIDAnnotation]; ok && id != "" {
			return id
		}
	}
	return ""
}

// deleteExpiredPod deletes an expired pod. When useManager is true and managerURL is set and sessionID is non-empty,
// it calls the Manager DELETE API so that a snapshot is taken before the pod is deleted. Otherwise it deletes the pod
// directly via the Kubernetes API (no snapshot).
func deleteExpiredPod(ctx context.Context, clientset *kubernetes.Clientset, pod *v1.Pod, useManager bool, managerURL, sessionID string) error {
	if useManager && managerURL != "" && sessionID != "" {
		ok, err := callManagerDelete(ctx, managerURL, sessionID)
		if err != nil {
			return fmt.Errorf("manager delete failed: %w", err)
		}
		if ok {
			return nil
		}
		// Manager returned 404 or other non-success; fall back to direct delete so pod is not left stuck
		klog.Warningf("Manager DELETE did not succeed for pod %s/%s (sessionId=%s), falling back to direct K8s delete (no snapshot)", pod.Namespace, pod.Name, sessionID)
	}

	// Direct K8s delete (no snapshot when sessionID missing or manager not configured)
	return clientset.CoreV1().Pods(pod.Namespace).Delete(ctx, pod.Name, metav1.DeleteOptions{})
}

// callManagerDelete calls DELETE /v1/sandboxes/{sessionId} on the Manager. Returns (true, nil) on 204, (false, nil) on 404, (false, err) on other errors.
func callManagerDelete(ctx context.Context, baseURL, sessionID string) (bool, error) {
	reqURL := strings.TrimSuffix(baseURL, "/") + "/v1/sandboxes/" + url.PathEscape(sessionID)
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, reqURL, nil)
	if err != nil {
		return false, err
	}

	client := &http.Client{Timeout: defaultManagerDeleteTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusNoContent:
		return true, nil
	case http.StatusNotFound:
		return false, nil
	default:
		return false, fmt.Errorf("unexpected status %d", resp.StatusCode)
	}
}

func init() {
	flag.Set("logtostderr", "true")
	flag.Set("alsologtostderr", "false")
}
