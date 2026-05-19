package k8s

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
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
}

// ClientConfig contains configuration for creating a new K8s client
type ClientConfig struct {
	Namespace      string
	QPS            int
	Burst          int
	RequestTimeout time.Duration
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
			namespace = "sandbox-workloads" // default
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
	}, nil
}

// getInClusterNamespace gets the namespace from the service account
func getInClusterNamespace() (string, error) {
	data, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/namespace")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
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
