package kubernetes

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os/exec"
	"strings"
	"time"
)

// K8sClient provides Kubernetes and Helm operations for NebulaCB.
type K8sClient struct {
	namespace string
	kubeconfig string
}

// NewK8sClient creates a new Kubernetes client.
func NewK8sClient(namespace, kubeconfig string) *K8sClient {
	return &K8sClient{
		namespace:  namespace,
		kubeconfig: kubeconfig,
	}
}

// SetNamespace updates the namespace used for subsequent operations.
func (k *K8sClient) SetNamespace(ns string) {
	if ns != "" {
		k.namespace = ns
	}
}

// GetPods lists pods in the namespace matching a label selector.
func (k *K8sClient) GetPods(ctx context.Context, labelSelector string) ([]PodInfo, error) {
	args := []string{"get", "pods", "-n", k.namespace, "-l", labelSelector, "-o", "json"}
	if k.kubeconfig != "" {
		args = append([]string{"--kubeconfig", k.kubeconfig}, args...)
	}

	cmd := exec.CommandContext(ctx, "kubectl", args...)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("kubectl get pods: %w", err)
	}

	return parsePodList(out)
}

// DeletePod kills a specific pod (for chaos injection).
func (k *K8sClient) DeletePod(ctx context.Context, podName string) error {
	args := []string{"delete", "pod", podName, "-n", k.namespace, "--grace-period=0", "--force"}
	if k.kubeconfig != "" {
		args = append([]string{"--kubeconfig", k.kubeconfig}, args...)
	}

	cmd := exec.CommandContext(ctx, "kubectl", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("delete pod %s: %s: %w", podName, string(out), err)
	}
	log.Printf("[K8s] Deleted pod %s", podName)
	return nil
}

// HelmUpgrade executes a Helm upgrade.
func (k *K8sClient) HelmUpgrade(ctx context.Context, release, chart, valuesFile string, setValues map[string]string) error {
	args := []string{"upgrade", release, chart, "-n", k.namespace, "--wait", "--timeout", "10m"}

	if valuesFile != "" {
		args = append(args, "-f", valuesFile)
	}
	for key, val := range setValues {
		args = append(args, "--set", fmt.Sprintf("%s=%s", key, val))
	}

	cmd := exec.CommandContext(ctx, "helm", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("helm upgrade: %s: %w", string(out), err)
	}
	log.Printf("[Helm] Upgrade of %s completed", release)
	return nil
}

// HelmRollback rolls back a Helm release.
func (k *K8sClient) HelmRollback(ctx context.Context, release string) error {
	args := []string{"rollback", release, "-n", k.namespace}

	cmd := exec.CommandContext(ctx, "helm", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("helm rollback: %s: %w", string(out), err)
	}
	log.Printf("[Helm] Rollback of %s completed", release)
	return nil
}

// WaitForRollout waits for a StatefulSet rollout to complete.
func (k *K8sClient) WaitForRollout(ctx context.Context, resourceName string, timeout time.Duration) error {
	args := []string{"rollout", "status", "statefulset/" + resourceName, "-n", k.namespace,
		"--timeout", fmt.Sprintf("%ds", int(timeout.Seconds()))}
	if k.kubeconfig != "" {
		args = append([]string{"--kubeconfig", k.kubeconfig}, args...)
	}

	cmd := exec.CommandContext(ctx, "kubectl", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("wait rollout %s: %s: %w", resourceName, string(out), err)
	}
	return nil
}

// ExecInPod executes a command inside a pod.
func (k *K8sClient) ExecInPod(ctx context.Context, podName string, command []string) (string, error) {
	args := []string{"exec", podName, "-n", k.namespace, "--"}
	args = append(args, command...)
	if k.kubeconfig != "" {
		args = append([]string{"--kubeconfig", k.kubeconfig}, args...)
	}

	cmd := exec.CommandContext(ctx, "kubectl", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("exec in %s: %s: %w", podName, string(out), err)
	}
	return strings.TrimSpace(string(out)), nil
}

// PatchCouchbaseCluster patches the CouchbaseCluster CR image to trigger a rolling upgrade.
func (k *K8sClient) PatchCouchbaseCluster(ctx context.Context, clusterName, targetVersion string) error {
	patch := fmt.Sprintf(`{"spec":{"image":"couchbase/server:%s"}}`, targetVersion)
	args := []string{"patch", "couchbasecluster", clusterName, "-n", k.namespace,
		"--type", "merge", "-p", patch}
	if k.kubeconfig != "" {
		args = append([]string{"--kubeconfig", k.kubeconfig}, args...)
	}

	cmd := exec.CommandContext(ctx, "kubectl", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("patch couchbasecluster %s: %s: %w", clusterName, string(out), err)
	}
	log.Printf("[K8s] Patched CouchbaseCluster %s to version %s", clusterName, targetVersion)
	return nil
}

// PodInfo holds basic pod metadata.
type PodInfo struct {
	Name      string    `json:"name"`
	Status    string    `json:"status"`
	Ready     bool      `json:"ready"`
	Node      string    `json:"node"`
	StartTime time.Time `json:"start_time"`
	Image     string    `json:"image"`
}

// parsePodList parses kubectl JSON output into PodInfo slices.
func parsePodList(data []byte) ([]PodInfo, error) {
	// Simplified parser — in production, use k8s client-go
	var result struct {
		Items []struct {
			Metadata struct {
				Name string `json:"name"`
			} `json:"metadata"`
			Spec struct {
				NodeName   string `json:"nodeName"`
				Containers []struct {
					Image string `json:"image"`
				} `json:"containers"`
			} `json:"spec"`
			Status struct {
				Phase             string `json:"phase"`
				StartTime         string `json:"startTime"`
				ContainerStatuses []struct {
					Ready bool `json:"ready"`
				} `json:"containerStatuses"`
			} `json:"status"`
		} `json:"items"`
	}

	if err := parseJSON(data, &result); err != nil {
		return nil, err
	}

	var pods []PodInfo
	for _, item := range result.Items {
		ready := true
		for _, cs := range item.Status.ContainerStatuses {
			if !cs.Ready {
				ready = false
				break
			}
		}

		startTime, _ := time.Parse(time.RFC3339, item.Status.StartTime)
		image := ""
		if len(item.Spec.Containers) > 0 {
			image = item.Spec.Containers[0].Image
		}

		pods = append(pods, PodInfo{
			Name:      item.Metadata.Name,
			Status:    item.Status.Phase,
			Ready:     ready,
			Node:      item.Spec.NodeName,
			StartTime: startTime,
			Image:     image,
		})
	}
	return pods, nil
}

func parseJSON(data []byte, v interface{}) error {
	return json.Unmarshal(data, v)
}
