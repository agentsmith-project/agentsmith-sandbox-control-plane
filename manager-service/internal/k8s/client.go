package k8s

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// Client wraps the Kubernetes clientset with additional functionality
type Client struct {
	clientset *kubernetes.Clientset
	config    *rest.Config
	namespace string
	qps       int
	burst     int
	timeout   time.Duration
	retry     *RetryConfig
}

// RetryConfig contains retry configuration for K8s API calls
type RetryConfig struct {
	Enabled     bool
	MaxAttempts int
	BaseBackoff time.Duration
	MaxBackoff  time.Duration
}

// ClientConfig contains configuration for creating a new K8s client
type ClientConfig struct {
	Namespace      string
	QPS            int
	Burst          int
	RequestTimeout time.Duration
	Retry          *RetryConfig
}

// NewClient creates a new Kubernetes client
func NewClient(cfg *ClientConfig) (*Client, error) {
	if cfg == nil {
		cfg = &ClientConfig{}
	}

	// Set defaults
	if cfg.QPS == 0 {
		cfg.QPS = 50
	}
	if cfg.Burst == 0 {
		cfg.Burst = 100
	}
	if cfg.RequestTimeout == 0 {
		cfg.RequestTimeout = 15 * time.Second
	}
	if cfg.Retry == nil {
		cfg.Retry = &RetryConfig{
			Enabled:     true,
			MaxAttempts: 3,
			BaseBackoff: 200 * time.Millisecond,
			MaxBackoff:  2 * time.Second,
		}
	}

	// Try in-cluster config first, fallback to kubeconfig
	config, err := rest.InClusterConfig()
	if err != nil {
		kubeconfig := os.Getenv("KUBECONFIG")
		if kubeconfig == "" {
			kubeconfig = os.Getenv("HOME") + "/.kube/config"
		}
		config, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
		if err != nil {
			return nil, fmt.Errorf("failed to get k8s config: %w", err)
		}
		log.Printf("K8s: using kubeconfig from %s", kubeconfig)
	} else {
		log.Printf("K8s: using in-cluster config")
	}

	// Configure QPS and Burst
	config.QPS = float32(cfg.QPS)
	config.Burst = cfg.Burst

	// Configure a reasonable default timeout for K8s API calls.
	// Exec streaming uses its own per-call timeouts and is not governed by this value.
	config.Timeout = cfg.RequestTimeout

	// Create clientset
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create clientset: %w", err)
	}

	namespace := cfg.Namespace
	if namespace == "" {
		// Try to get namespace from service account
		if ns, err := getInClusterNamespace(); err == nil {
			namespace = ns
		} else {
			namespace = "sandbox" // default
		}
	}

	log.Printf("K8s: initialized (namespace=%s, qps=%d, burst=%d)", namespace, cfg.QPS, cfg.Burst)

	return &Client{
		clientset: clientset,
		config:    config,
		namespace: namespace,
		qps:       cfg.QPS,
		burst:     cfg.Burst,
		timeout:   cfg.RequestTimeout,
		retry:     cfg.Retry,
	}, nil
}

// getInClusterNamespace gets the namespace from the service account
func getInClusterNamespace() (string, error) {
	data, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/namespace")
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// Clientset returns the underlying kubernetes clientset
func (c *Client) Clientset() *kubernetes.Clientset {
	return c.clientset
}

// Config returns the underlying rest config
func (c *Client) Config() *rest.Config {
	return c.config
}

// Namespace returns the configured namespace
func (c *Client) Namespace() string {
	return c.namespace
}

// CheckReady checks if the client is ready to make requests
func (c *Client) CheckReady(ctx context.Context) error {
	// Try to list pods in the configured namespace with a short timeout
	timeoutCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	_, err := c.clientset.CoreV1().Pods(c.namespace).List(timeoutCtx, metav1.ListOptions{Limit: 1})
	if err != nil {
		if errors.IsNotFound(err) {
			// Namespace doesn't exist - create it
			return fmt.Errorf("namespace %s does not exist", c.namespace)
		}
		return fmt.Errorf("failed to list pods: %w", err)
	}

	log.Printf("K8s: ready check successful")
	return nil
}

// CreateNamespace creates a namespace if it doesn't exist
func (c *Client) CreateNamespace(ctx context.Context, name string, labels map[string]string) error {
	ns := &v1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name:   name,
			Labels: labels,
		},
	}

	_, err := c.clientset.CoreV1().Namespaces().Create(ctx, ns, metav1.CreateOptions{})
	if err != nil {
		if errors.IsAlreadyExists(err) {
			log.Printf("K8s: namespace %s already exists", name)
			return nil
		}
		return fmt.Errorf("failed to create namespace: %w", err)
	}

	log.Printf("K8s: created namespace %s", name)
	return nil
}

// Retry executes a function with retry logic
func (c *Client) Retry(ctx context.Context, fn func() error) error {
	if !c.retry.Enabled {
		return fn()
	}

	var lastErr error
	backoff := c.retry.BaseBackoff

	for attempt := 0; attempt < c.retry.MaxAttempts; attempt++ {
		err := fn()
		if err == nil {
			if attempt > 0 {
				log.Printf("K8s: retry succeeded on attempt %d", attempt+1)
			}
			return nil
		}

		lastErr = err

		// Don't retry on certain errors
		if errors.IsNotFound(err) || errors.IsAlreadyExists(err) || errors.IsForbidden(err) {
			return err
		}

		// Wait before retry
		if attempt < c.retry.MaxAttempts-1 {
			log.Printf("K8s: attempt %d failed, backing off %v: %v", attempt+1, backoff, err)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff):
				// Exponential backoff
				backoff *= 2
				if backoff > c.retry.MaxBackoff {
					backoff = c.retry.MaxBackoff
				}
			}
		}
	}

	return fmt.Errorf("max retry attempts reached: %w", lastErr)
}

// ListSandboxPods lists all pods in the sandbox namespace
func (c *Client) ListSandboxPods(ctx context.Context) ([]*v1.Pod, error) {
	pods, err := c.clientset.CoreV1().Pods(c.namespace).List(ctx, metav1.ListOptions{
		LabelSelector: "app=sandbox",
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list pods: %w", err)
	}

	result := make([]*v1.Pod, 0, len(pods.Items))
	for i := range pods.Items {
		result = append(result, &pods.Items[i])
	}

	return result, nil
}

// WithTimeout creates a context with timeout for K8s operations
func (c *Client) WithTimeout(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, c.timeout)
}
