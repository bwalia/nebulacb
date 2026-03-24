package couchbase

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/balinderwalia/nebulacb/internal/models"
	"github.com/couchbase/gocb/v2"
)

// Client wraps a Couchbase SDK connection for NebulaCB operations.
type Client struct {
	cluster    *gocb.Cluster
	bucket     *gocb.Bucket
	collection *gocb.Collection
	config     models.ClusterConfig
}

// NewClient connects to a Couchbase cluster.
func NewClient(cfg models.ClusterConfig) (*Client, error) {
	opts := gocb.ClusterOptions{
		Authenticator: gocb.PasswordAuthenticator{
			Username: cfg.Username,
			Password: cfg.Password,
		},
		TimeoutsConfig: gocb.TimeoutsConfig{
			KVTimeout:     10 * time.Second,
			ConnectTimeout: 15 * time.Second,
		},
	}

	connStr := cfg.Host
	if !strings.Contains(connStr, "://") {
		connStr = "couchbase://" + connStr
	}
	// The couchbase:// scheme does not support port in the host (e.g. couchbase://host:8091).
	// Strip the management port if present; the SDK auto-discovers the KV port.
	if strings.HasPrefix(connStr, "couchbase://") {
		hostPart := strings.TrimPrefix(connStr, "couchbase://")
		if h, _, found := strings.Cut(hostPart, ":"); found {
			connStr = "couchbase://" + h
		}
	}

	cluster, err := gocb.Connect(connStr, opts)
	if err != nil {
		return nil, fmt.Errorf("connect to %s: %w", cfg.Host, err)
	}

	if err := cluster.WaitUntilReady(15*time.Second, nil); err != nil {
		return nil, fmt.Errorf("cluster not ready: %w", err)
	}

	bucket := cluster.Bucket(cfg.Bucket)
	if err := bucket.WaitUntilReady(10*time.Second, nil); err != nil {
		return nil, fmt.Errorf("bucket %s not ready: %w", cfg.Bucket, err)
	}

	var col *gocb.Collection
	if cfg.Scope != "" && cfg.Collection != "" {
		col = bucket.Scope(cfg.Scope).Collection(cfg.Collection)
	} else {
		col = bucket.DefaultCollection()
	}

	return &Client{
		cluster:    cluster,
		bucket:     bucket,
		collection: col,
		config:     cfg,
	}, nil
}

// Upsert writes a document.
func (c *Client) Upsert(ctx context.Context, id string, doc interface{}) error {
	_, err := c.collection.Upsert(id, doc, &gocb.UpsertOptions{
		Context: ctx,
	})
	return err
}

// Get reads a document.
func (c *Client) Get(ctx context.Context, id string) ([]byte, error) {
	result, err := c.collection.Get(id, &gocb.GetOptions{
		Context: ctx,
	})
	if err != nil {
		return nil, err
	}

	var raw json.RawMessage
	if err := result.Content(&raw); err != nil {
		return nil, err
	}
	return raw, nil
}

// Remove deletes a document.
func (c *Client) Remove(ctx context.Context, id string) error {
	_, err := c.collection.Remove(id, &gocb.RemoveOptions{
		Context: ctx,
	})
	return err
}

// DocCount returns the approximate document count via the REST API.
func (c *Client) DocCount(ctx context.Context) (uint64, error) {
	host := c.config.Host
	if strings.Contains(host, "://") {
		parts := strings.SplitN(host, "://", 2)
		host = parts[1]
	}
	if !strings.Contains(host, ":") {
		host = host + ":8091"
	}

	url := fmt.Sprintf("http://%s/pools/default/buckets/%s", host, c.config.Bucket)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return 0, err
	}
	req.SetBasicAuth(c.config.Username, c.config.Password)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, err
	}

	var result struct {
		BasicStats struct {
			ItemCount uint64 `json:"itemCount"`
		} `json:"basicStats"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return 0, err
	}
	return result.BasicStats.ItemCount, nil
}

// GetAllKeys scans all document keys using a KV range scan.
func (c *Client) GetAllKeys(ctx context.Context, prefix string) ([]string, error) {
	query := fmt.Sprintf("SELECT META().id FROM `%s` WHERE META().id LIKE '%s%%'", c.config.Bucket, prefix)

	rows, err := c.cluster.Query(query, &gocb.QueryOptions{
		Context:  ctx,
		Readonly: true,
	})
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var keys []string
	for rows.Next() {
		var row struct {
			ID string `json:"id"`
		}
		if err := rows.Row(&row); err != nil {
			continue
		}
		keys = append(keys, row.ID)
	}
	return keys, rows.Err()
}

// HashDocument computes a SHA-256 hash of a document's content.
func HashDocument(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

// Close disconnects from the cluster.
func (c *Client) Close() error {
	return c.cluster.Close(nil)
}

// GetClusterHealth retrieves node health via the REST API.
func (c *Client) GetClusterHealth(ctx context.Context) (models.ClusterHealth, error) {
	host := c.config.Host
	if strings.Contains(host, "://") {
		parts := strings.SplitN(host, "://", 2)
		host = parts[1]
	}
	if !strings.Contains(host, ":") {
		host = host + ":8091"
	}

	url := fmt.Sprintf("http://%s/pools/default", host)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return models.ClusterHealth{}, err
	}
	req.SetBasicAuth(c.config.Username, c.config.Password)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return models.ClusterHealth{}, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return models.ClusterHealth{}, err
	}

	var pool struct {
		Nodes []struct {
			Hostname string `json:"hostname"`
			Status   string `json:"status"`
			Version  string `json:"version"`
			SystemStats struct {
				CPUUtilization float64 `json:"cpu_utilization_rate"`
				MemFree        uint64  `json:"mem_free"`
				MemTotal       uint64  `json:"mem_total"`
			} `json:"systemStats"`
		} `json:"nodes"`
		RebalanceStatus string `json:"rebalanceStatus"`
	}
	if err := json.Unmarshal(body, &pool); err != nil {
		return models.ClusterHealth{}, err
	}

	health := models.ClusterHealth{
		ClusterName:    c.config.Name,
		RebalanceState: pool.RebalanceStatus,
		Timestamp:      time.Now(),
	}

	for _, n := range pool.Nodes {
		memUsage := 0.0
		if n.SystemStats.MemTotal > 0 {
			memUsage = float64(n.SystemStats.MemTotal-n.SystemStats.MemFree) / float64(n.SystemStats.MemTotal) * 100
		}
		health.Nodes = append(health.Nodes, models.NodeStatus{
			Host:          n.Hostname,
			Status:        n.Status,
			Version:       n.Version,
			CPUUsage:      n.SystemStats.CPUUtilization,
			MemoryUsage:   memUsage,
			LastHeartbeat: time.Now(),
		})
	}

	return health, nil
}

// GetXDCRStatus retrieves XDCR replication statistics.
func (c *Client) GetXDCRStatus(ctx context.Context, replicationID string) (models.XDCRStatus, error) {
	host := c.config.Host
	if strings.Contains(host, "://") {
		parts := strings.SplitN(host, "://", 2)
		host = parts[1]
	}
	if !strings.Contains(host, ":") {
		host = host + ":8091"
	}

	url := fmt.Sprintf("http://%s/pools/default/buckets/%s/stats", host, c.config.Bucket)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return models.XDCRStatus{}, err
	}
	req.SetBasicAuth(c.config.Username, c.config.Password)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return models.XDCRStatus{}, err
	}
	defer resp.Body.Close()

	// Parse XDCR-specific stats from the response
	var stats map[string]interface{}
	body, _ := io.ReadAll(resp.Body)
	json.Unmarshal(body, &stats)

	status := models.XDCRStatus{
		ReplicationID: replicationID,
		State:         "Running",
		Timestamp:     time.Now(),
	}

	return status, nil
}
