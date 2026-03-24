package docker

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os/exec"
	"strings"
	"time"

	"github.com/balinderwalia/nebulacb/internal/models"
)

// Client provides Docker-based Couchbase cluster management.
type Client struct {
	host    string
	network string
}

// NewClient creates a new Docker client.
func NewClient(cfg models.DockerConfig) *Client {
	host := cfg.Host
	if host == "" {
		host = "unix:///var/run/docker.sock"
	}
	network := cfg.Network
	if network == "" {
		network = "nebulacb-net"
	}
	return &Client{
		host:    host,
		network: network,
	}
}

// ListContainers returns all Couchbase containers.
func (c *Client) ListContainers(ctx context.Context) ([]models.ContainerInfo, error) {
	args := []string{"ps", "-a", "--filter", "ancestor=couchbase/server", "--format", "json"}
	// Also include community edition
	out, err := c.execDocker(ctx, args...)
	if err != nil {
		// Try alternate: filter by name pattern
		args = []string{"ps", "-a", "--filter", "name=couchbase", "--format", "json"}
		out, err = c.execDocker(ctx, args...)
		if err != nil {
			return nil, fmt.Errorf("docker ps: %w", err)
		}
	}

	return c.parseContainerList(out)
}

// CreateCluster creates a new Couchbase container.
func (c *Client) CreateCluster(ctx context.Context, name, image string, ports map[string]string, env map[string]string) (*models.ContainerInfo, error) {
	if image == "" {
		image = "couchbase/server:latest"
	}

	args := []string{"run", "-d", "--name", name, "--network", c.network}

	for containerPort, hostPort := range ports {
		args = append(args, "-p", fmt.Sprintf("%s:%s", hostPort, containerPort))
	}

	for k, v := range env {
		args = append(args, "-e", fmt.Sprintf("%s=%s", k, v))
	}

	args = append(args, image)

	out, err := c.execDocker(ctx, args...)
	if err != nil {
		return nil, fmt.Errorf("docker run: %s: %w", string(out), err)
	}

	containerID := strings.TrimSpace(string(out))
	log.Printf("[Docker] Created container %s (%s) from image %s", name, containerID[:12], image)

	return &models.ContainerInfo{
		ID:     containerID,
		Name:   name,
		Image:  image,
		Status: "running",
		State:  "running",
		Ports:  ports,
	}, nil
}

// StopContainer stops a Couchbase container.
func (c *Client) StopContainer(ctx context.Context, nameOrID string) error {
	_, err := c.execDocker(ctx, "stop", nameOrID)
	if err != nil {
		return fmt.Errorf("docker stop %s: %w", nameOrID, err)
	}
	log.Printf("[Docker] Stopped container %s", nameOrID)
	return nil
}

// StartContainer starts a stopped container.
func (c *Client) StartContainer(ctx context.Context, nameOrID string) error {
	_, err := c.execDocker(ctx, "start", nameOrID)
	if err != nil {
		return fmt.Errorf("docker start %s: %w", nameOrID, err)
	}
	log.Printf("[Docker] Started container %s", nameOrID)
	return nil
}

// RemoveContainer removes a container.
func (c *Client) RemoveContainer(ctx context.Context, nameOrID string, force bool) error {
	args := []string{"rm"}
	if force {
		args = append(args, "-f")
	}
	args = append(args, nameOrID)

	_, err := c.execDocker(ctx, args...)
	if err != nil {
		return fmt.Errorf("docker rm %s: %w", nameOrID, err)
	}
	log.Printf("[Docker] Removed container %s", nameOrID)
	return nil
}

// GetContainerStats returns resource usage for a container.
func (c *Client) GetContainerStats(ctx context.Context, nameOrID string) (*models.ContainerInfo, error) {
	out, err := c.execDocker(ctx, "stats", "--no-stream", "--format", "json", nameOrID)
	if err != nil {
		return nil, fmt.Errorf("docker stats %s: %w", nameOrID, err)
	}

	var stats struct {
		Name     string `json:"Name"`
		CPUPerc  string `json:"CPUPerc"`
		MemUsage string `json:"MemUsage"`
	}
	if err := json.Unmarshal(out, &stats); err != nil {
		return nil, err
	}

	return &models.ContainerInfo{
		Name: stats.Name,
	}, nil
}

// UpgradeContainer performs a rolling upgrade of a Couchbase container.
func (c *Client) UpgradeContainer(ctx context.Context, nameOrID, newImage string) error {
	// 1. Get current container config
	inspectOut, err := c.execDocker(ctx, "inspect", nameOrID)
	if err != nil {
		return fmt.Errorf("inspect %s: %w", nameOrID, err)
	}

	var inspect []struct {
		Name   string `json:"Name"`
		Config struct {
			Env   []string          `json:"Env"`
			Image string            `json:"Image"`
		} `json:"Config"`
		HostConfig struct {
			PortBindings map[string][]struct {
				HostPort string `json:"HostPort"`
			} `json:"PortBindings"`
		} `json:"HostConfig"`
		Mounts []struct {
			Source      string `json:"Source"`
			Destination string `json:"Destination"`
		} `json:"Mounts"`
	}
	if err := json.Unmarshal(inspectOut, &inspect); err != nil {
		return fmt.Errorf("parse inspect: %w", err)
	}

	if len(inspect) == 0 {
		return fmt.Errorf("container %s not found", nameOrID)
	}

	// 2. Pull new image
	log.Printf("[Docker] Pulling image %s", newImage)
	if _, err := c.execDocker(ctx, "pull", newImage); err != nil {
		return fmt.Errorf("pull %s: %w", newImage, err)
	}

	// 3. Stop old container
	log.Printf("[Docker] Stopping container %s for upgrade", nameOrID)
	if err := c.StopContainer(ctx, nameOrID); err != nil {
		return err
	}

	// 4. Rename old container
	backupName := nameOrID + "-backup-" + fmt.Sprintf("%d", time.Now().Unix())
	if _, err := c.execDocker(ctx, "rename", nameOrID, backupName); err != nil {
		return fmt.Errorf("rename %s: %w", nameOrID, err)
	}

	// 5. Create new container with same config
	args := []string{"run", "-d", "--name", nameOrID, "--network", c.network}

	// Copy port bindings
	for port, bindings := range inspect[0].HostConfig.PortBindings {
		for _, b := range bindings {
			args = append(args, "-p", fmt.Sprintf("%s:%s", b.HostPort, port))
		}
	}

	// Copy volumes
	for _, mount := range inspect[0].Mounts {
		args = append(args, "-v", fmt.Sprintf("%s:%s", mount.Source, mount.Destination))
	}

	// Copy env vars
	for _, env := range inspect[0].Config.Env {
		args = append(args, "-e", env)
	}

	args = append(args, newImage)

	out, err := c.execDocker(ctx, args...)
	if err != nil {
		// Rollback: rename backup container back
		c.execDocker(ctx, "rename", backupName, nameOrID)
		c.StartContainer(ctx, nameOrID)
		return fmt.Errorf("create upgraded container: %s: %w", string(out), err)
	}

	log.Printf("[Docker] Upgraded container %s to %s", nameOrID, newImage)

	// 6. Remove backup after successful start
	c.RemoveContainer(ctx, backupName, true)

	return nil
}

// GetContainerLogs retrieves logs from a container.
func (c *Client) GetContainerLogs(ctx context.Context, nameOrID string, lines int) (string, error) {
	if lines == 0 {
		lines = 100
	}
	out, err := c.execDocker(ctx, "logs", "--tail", fmt.Sprintf("%d", lines), nameOrID)
	if err != nil {
		return "", fmt.Errorf("docker logs %s: %w", nameOrID, err)
	}
	return string(out), nil
}

// CreateNetwork creates a Docker network for Couchbase clusters.
func (c *Client) CreateNetwork(ctx context.Context, name string) error {
	_, err := c.execDocker(ctx, "network", "create", name)
	if err != nil {
		// Network may already exist
		if strings.Contains(err.Error(), "already exists") {
			return nil
		}
		return fmt.Errorf("create network %s: %w", name, err)
	}
	log.Printf("[Docker] Created network %s", name)
	return nil
}

// ComposeUp starts services from a docker-compose file.
func (c *Client) ComposeUp(ctx context.Context, composeFile string) error {
	args := []string{"compose"}
	if composeFile != "" {
		args = append(args, "-f", composeFile)
	}
	args = append(args, "up", "-d")

	out, err := c.execDockerCmd(ctx, "docker", args...)
	if err != nil {
		return fmt.Errorf("compose up: %s: %w", string(out), err)
	}
	log.Printf("[Docker] Compose up completed")
	return nil
}

// ComposeDown stops and removes services.
func (c *Client) ComposeDown(ctx context.Context, composeFile string) error {
	args := []string{"compose"}
	if composeFile != "" {
		args = append(args, "-f", composeFile)
	}
	args = append(args, "down")

	out, err := c.execDockerCmd(ctx, "docker", args...)
	if err != nil {
		return fmt.Errorf("compose down: %s: %w", string(out), err)
	}
	log.Printf("[Docker] Compose down completed")
	return nil
}

func (c *Client) execDocker(ctx context.Context, args ...string) ([]byte, error) {
	return c.execDockerCmd(ctx, "docker", args...)
}

func (c *Client) execDockerCmd(ctx context.Context, command string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, command, args...)
	return cmd.CombinedOutput()
}

func (c *Client) parseContainerList(data []byte) ([]models.ContainerInfo, error) {
	var containers []models.ContainerInfo

	// Docker outputs one JSON object per line
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	for _, line := range lines {
		if line == "" {
			continue
		}
		var raw struct {
			ID      string `json:"ID"`
			Names   string `json:"Names"`
			Image   string `json:"Image"`
			Status  string `json:"Status"`
			State   string `json:"State"`
			Ports   string `json:"Ports"`
			Created string `json:"CreatedAt"`
		}
		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			continue
		}

		containers = append(containers, models.ContainerInfo{
			ID:     raw.ID,
			Name:   raw.Names,
			Image:  raw.Image,
			Status: raw.Status,
			State:  raw.State,
		})
	}

	return containers, nil
}
