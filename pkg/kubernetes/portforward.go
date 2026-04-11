package kubernetes

import (
	"context"
	"fmt"
	"log"
	"net"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/balinderwalia/nebulacb/internal/models"
)

// PortForwardManager manages kubectl port-forward processes for k8s-hosted Couchbase clusters.
// It auto-discovers the Couchbase pod in each namespace and forwards port 8091 to a local port,
// then rewrites the cluster config host to point at localhost:<localPort>.
type PortForwardManager struct {
	kubeconfig string
	mu         sync.Mutex
	forwards   map[string]*portForward // keyed by cluster name
}

type portForward struct {
	clusterName string
	namespace   string
	podName     string
	localPort   int
	cmd         *exec.Cmd
	cancel      context.CancelFunc
}

// NewPortForwardManager creates a new manager.
func NewPortForwardManager(kubeconfig string) *PortForwardManager {
	return &PortForwardManager{
		kubeconfig: kubeconfig,
		forwards:   make(map[string]*portForward),
	}
}

// SetupClusters discovers pods and creates port-forwards for all kubernetes-platform clusters.
// It mutates the cluster configs in-place, setting Host to localhost:<localPort>.
func (m *PortForwardManager) SetupClusters(ctx context.Context, clusters map[string]models.ClusterConfig) map[string]models.ClusterConfig {
	result := make(map[string]models.ClusterConfig, len(clusters))
	for k, v := range clusters {
		result[k] = v
	}

	for name, cfg := range result {
		if cfg.Platform != "kubernetes" || cfg.Namespace == "" {
			continue
		}
		// Skip port-forwarding if KVPort is set — cluster is accessible via NodePort directly
		if cfg.KVPort > 0 {
			log.Printf("[PortForward] Skipping %s — using NodePort (host=%s kv_port=%d)", name, cfg.Host, cfg.KVPort)
			continue
		}

		podName, err := m.discoverCouchbasePod(ctx, cfg.Namespace, name)
		if err != nil {
			log.Printf("[PortForward] WARN: could not find Couchbase pod for %s in ns %s: %v", name, cfg.Namespace, err)
			continue
		}

		localPort, err := m.startPortForward(ctx, name, cfg.Namespace, podName)
		if err != nil {
			log.Printf("[PortForward] WARN: failed to port-forward %s/%s: %v", cfg.Namespace, podName, err)
			continue
		}

		cfg.Host = fmt.Sprintf("localhost:%d", localPort)
		result[name] = cfg
		log.Printf("[PortForward] %s → %s/%s → localhost:%d", name, cfg.Namespace, podName, localPort)
	}

	return result
}

// discoverCouchbasePod finds the Couchbase server pod in a namespace.
// It looks for pods with the cluster name prefix (e.g. cb-local-0000).
func (m *PortForwardManager) discoverCouchbasePod(ctx context.Context, namespace, clusterName string) (string, error) {
	args := []string{"get", "pods", "-n", namespace,
		"-o", "jsonpath={.items[*].metadata.name}",
		"--field-selector=status.phase=Running",
	}
	if m.kubeconfig != "" {
		args = append([]string{"--kubeconfig", m.kubeconfig}, args...)
	}

	cmd := exec.CommandContext(ctx, "kubectl", args...)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("kubectl get pods: %w", err)
	}

	pods := strings.Fields(string(out))
	// Find a pod that matches the cluster name pattern (e.g. "cb-local-0003")
	// Couchbase operator pods contain "operator" or "admission" — skip those.
	for _, pod := range pods {
		if strings.Contains(pod, "operator") || strings.Contains(pod, "admission") {
			continue
		}
		if strings.HasPrefix(pod, clusterName+"-") || pod == clusterName {
			return pod, nil
		}
	}

	// Fallback: return the first non-operator pod
	for _, pod := range pods {
		if strings.Contains(pod, "operator") || strings.Contains(pod, "admission") {
			continue
		}
		return pod, nil
	}

	return "", fmt.Errorf("no Couchbase server pod found in namespace %s", namespace)
}

// startPortForward starts a kubectl port-forward process and returns the local port.
func (m *PortForwardManager) startPortForward(ctx context.Context, clusterName, namespace, podName string) (int, error) {
	localPort, err := getFreePort()
	if err != nil {
		return 0, fmt.Errorf("allocate local port: %w", err)
	}

	pfCtx, cancel := context.WithCancel(ctx)

	args := []string{"port-forward", "-n", namespace,
		fmt.Sprintf("pod/%s", podName),
		fmt.Sprintf("%d:8091", localPort),
	}
	if m.kubeconfig != "" {
		args = append([]string{"--kubeconfig", m.kubeconfig}, args...)
	}

	cmd := exec.CommandContext(pfCtx, "kubectl", args...)
	cmd.Stdout = nil
	cmd.Stderr = nil

	if err := cmd.Start(); err != nil {
		cancel()
		return 0, fmt.Errorf("start port-forward: %w", err)
	}

	m.mu.Lock()
	m.forwards[clusterName] = &portForward{
		clusterName: clusterName,
		namespace:   namespace,
		podName:     podName,
		localPort:   localPort,
		cmd:         cmd,
		cancel:      cancel,
	}
	m.mu.Unlock()

	// Wait for port to become reachable
	if err := waitForPort(localPort, 10*time.Second); err != nil {
		cancel()
		return 0, fmt.Errorf("port-forward not ready: %w", err)
	}

	return localPort, nil
}

// StartHealthCheck launches a background goroutine that periodically checks all
// port-forwards and restarts any that have died. This prevents clusters from going
// offline after long-running sessions.
func (m *PortForwardManager) StartHealthCheck(ctx context.Context, clusters map[string]models.ClusterConfig, interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				m.mu.Lock()
				for name, pf := range m.forwards {
					// Check if the port-forward process is still alive
					if pf.cmd.ProcessState != nil || !isPortOpen(pf.localPort) {
						log.Printf("[PortForward] %s is dead (port %d unreachable), restarting...", name, pf.localPort)
						pf.cancel()
						_ = pf.cmd.Wait()
						delete(m.forwards, name)

						// Re-establish
						cfg, ok := clusters[name]
						if !ok {
							continue
						}
						m.mu.Unlock()
						podName, err := m.discoverCouchbasePod(ctx, cfg.Namespace, name)
						if err != nil {
							log.Printf("[PortForward] WARN: reconnect %s: %v", name, err)
							m.mu.Lock()
							continue
						}
						localPort, err := m.startPortForward(ctx, name, cfg.Namespace, podName)
						if err != nil {
							log.Printf("[PortForward] WARN: reconnect %s: %v", name, err)
							m.mu.Lock()
							continue
						}
						log.Printf("[PortForward] Reconnected %s → %s/%s → localhost:%d", name, cfg.Namespace, podName, localPort)
						m.mu.Lock()
					}
				}
				m.mu.Unlock()
			}
		}
	}()
}

// Stop terminates all port-forward processes.
func (m *PortForwardManager) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for name, pf := range m.forwards {
		log.Printf("[PortForward] Stopping %s (pid %d)", name, pf.cmd.Process.Pid)
		pf.cancel()
		_ = pf.cmd.Wait()
	}
	m.forwards = make(map[string]*portForward)
}

// isPortOpen checks if a local port is still accepting connections.
func isPortOpen(port int) bool {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 2*time.Second)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// getFreePort asks the OS for an available port.
func getFreePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	return port, nil
}

// waitForPort polls until the port is accepting connections.
func waitForPort(port int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 500*time.Millisecond)
		if err == nil {
			conn.Close()
			return nil
		}
		time.Sleep(250 * time.Millisecond)
	}
	return fmt.Errorf("port %d not reachable after %s", port, timeout)
}
