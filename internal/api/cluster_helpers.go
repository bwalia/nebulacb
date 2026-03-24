package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/balinderwalia/nebulacb/internal/models"
)

func restHost(cfg models.ClusterConfig) string {
	host := cfg.Host
	if strings.Contains(host, "://") {
		parts := strings.SplitN(host, "://", 2)
		host = parts[1]
	}
	if !strings.Contains(host, ":") {
		host = host + ":8091"
	}
	return host
}

func restGet(ctx context.Context, host, path, user, pass string) ([]byte, error) {
	url := fmt.Sprintf("http://%s%s", host, path)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(user, pass)
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}
	return io.ReadAll(resp.Body)
}

// fetchBuckets retrieves bucket information from a Couchbase cluster.
func fetchBuckets(ctx context.Context, cfg models.ClusterConfig) ([]models.BucketInfo, error) {
	host := restHost(cfg)
	data, err := restGet(ctx, host, "/pools/default/buckets", cfg.Username, cfg.Password)
	if err != nil {
		return nil, err
	}

	var raw []struct {
		Name       string `json:"name"`
		BucketType string `json:"bucketType"`
		Quota      struct {
			RAM uint64 `json:"ram"`
		} `json:"quota"`
		BasicStats struct {
			ItemCount   uint64  `json:"itemCount"`
			OpsPerSec   float64 `json:"opsPerSec"`
			DiskUsed    uint64  `json:"diskUsed"`
			DataUsed    uint64  `json:"dataUsed"`
			MemUsed     uint64  `json:"memUsed"`
		} `json:"basicStats"`
		ReplicaNumber      int    `json:"replicaNumber"`
		EvictionPolicy     string `json:"evictionPolicy"`
		ConflictResolutionType string `json:"conflictResolutionType"`
		FlushEnabled       bool   `json:"flush"`
		CompressionMode    string `json:"compressionMode"`
		MaxTTL             int    `json:"maxTTL"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}

	var buckets []models.BucketInfo
	for _, b := range raw {
		buckets = append(buckets, models.BucketInfo{
			Name:               b.Name,
			Type:               b.BucketType,
			RamQuotaMB:         b.Quota.RAM / (1024 * 1024),
			RamUsedMB:          float64(b.BasicStats.MemUsed) / (1024 * 1024),
			DiskUsedMB:         float64(b.BasicStats.DiskUsed) / (1024 * 1024),
			ItemCount:          b.BasicStats.ItemCount,
			OpsPerSec:          b.BasicStats.OpsPerSec,
			Replicas:           b.ReplicaNumber,
			EvictionPolicy:     b.EvictionPolicy,
			ConflictResolution: b.ConflictResolutionType,
			FlushEnabled:       b.FlushEnabled,
			CompressionMode:    b.CompressionMode,
			MaxTTL:             b.MaxTTL,
		})
	}
	return buckets, nil
}

// fetchIndexes retrieves index information from a Couchbase cluster.
func fetchIndexes(ctx context.Context, cfg models.ClusterConfig) ([]models.IndexInfo, error) {
	host := restHost(cfg)
	// Try the indexer stats endpoint
	data, err := restGet(ctx, host, "/indexStatus", cfg.Username, cfg.Password)
	if err != nil {
		// Fallback to N1QL system catalog
		return fetchIndexesN1QL(ctx, cfg)
	}

	var raw struct {
		Indexes []struct {
			Name       string   `json:"index"`
			Bucket     string   `json:"bucket"`
			Scope      string   `json:"scope"`
			Collection string   `json:"collection"`
			Status     string   `json:"status"`
			Definition string   `json:"definition"`
			NumReplica int      `json:"numReplica"`
			Hosts      []string `json:"hosts"`
		} `json:"indexes"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}

	var indexes []models.IndexInfo
	for _, idx := range raw.Indexes {
		indexes = append(indexes, models.IndexInfo{
			Name:          idx.Name,
			Bucket:        idx.Bucket,
			Scope:         idx.Scope,
			Collection:    idx.Collection,
			Type:          "gsi",
			State:         idx.Status,
			KeyExpression: idx.Definition,
			Replicas:      idx.NumReplica,
			Timestamp:     time.Now(),
		})
	}
	return indexes, nil
}

func fetchIndexesN1QL(ctx context.Context, cfg models.ClusterConfig) ([]models.IndexInfo, error) {
	// Query N1QL for index info — requires query service on port 8093
	host := restHost(cfg)
	queryHost := strings.Split(host, ":")[0] + ":8093"

	query := `SELECT name, keyspace_id as bucket, state, index_key, using as type FROM system:indexes`
	payload, _ := json.Marshal(map[string]string{"statement": query})

	req, err := http.NewRequestWithContext(ctx, "POST", fmt.Sprintf("http://%s/query/service", queryHost), strings.NewReader(string(payload)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.SetBasicAuth(cfg.Username, cfg.Password)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	var result struct {
		Results []struct {
			Name   string   `json:"name"`
			Bucket string   `json:"bucket"`
			State  string   `json:"state"`
			Key    []string `json:"index_key"`
			Type   string   `json:"type"`
		} `json:"results"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, err
	}

	var indexes []models.IndexInfo
	for _, r := range result.Results {
		indexes = append(indexes, models.IndexInfo{
			Name:          r.Name,
			Bucket:        r.Bucket,
			Type:          r.Type,
			State:         r.State,
			KeyExpression: strings.Join(r.Key, ", "),
			Timestamp:     time.Now(),
		})
	}
	return indexes, nil
}

// fetchUsers retrieves user information from a Couchbase cluster.
func fetchUsers(ctx context.Context, cfg models.ClusterConfig) ([]models.CBUser, error) {
	host := restHost(cfg)
	data, err := restGet(ctx, host, "/settings/rbac/users", cfg.Username, cfg.Password)
	if err != nil {
		return nil, err
	}

	var raw []struct {
		ID     string `json:"id"`
		Name   string `json:"name"`
		Domain string `json:"domain"`
		Roles  []struct {
			Role           string `json:"role"`
			BucketName     string `json:"bucket_name"`
			ScopeName      string `json:"scope_name"`
			CollectionName string `json:"collection_name"`
		} `json:"roles"`
		Groups []string `json:"groups"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}

	var users []models.CBUser
	for _, u := range raw {
		user := models.CBUser{
			ID:     u.ID,
			Name:   u.Name,
			Domain: u.Domain,
			Groups: u.Groups,
		}
		for _, r := range u.Roles {
			user.Roles = append(user.Roles, models.CBRole{
				Role:       r.Role,
				Bucket:     r.BucketName,
				Scope:      r.ScopeName,
				Collection: r.CollectionName,
			})
		}
		users = append(users, user)
	}
	return users, nil
}

// detectEdition detects whether a cluster is Community or Enterprise edition.
func detectEdition(ctx context.Context, cfg models.ClusterConfig) (models.EditionInfo, error) {
	host := restHost(cfg)
	data, err := restGet(ctx, host, "/pools", cfg.Username, cfg.Password)
	if err != nil {
		return models.EditionInfo{}, err
	}

	var raw struct {
		ImplementationVersion string `json:"implementationVersion"`
		IsEnterprise          bool   `json:"isEnterprise"`
		ComponentsVersion     struct {
			NS_Server string `json:"ns_server"`
		} `json:"componentsVersion"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return models.EditionInfo{}, err
	}

	edition := "community"
	if raw.IsEnterprise {
		edition = "enterprise"
	}

	// Determine available features based on edition
	features := []string{"kv", "n1ql", "index", "fts", "views"}
	if raw.IsEnterprise {
		features = append(features, "analytics", "eventing", "backup", "xdcr_advanced",
			"audit", "ldap", "encryption_at_rest", "auto_failover", "rack_awareness")
	}

	info := models.EditionInfo{
		Edition:      edition,
		Version:      raw.ImplementationVersion,
		Enterprise:   raw.IsEnterprise,
		Features:     features,
		LicenseValid: true,
	}

	return info, nil
}

// fetchLogs retrieves recent logs from a Couchbase cluster.
func fetchLogs(ctx context.Context, cfg models.ClusterConfig) ([]map[string]interface{}, error) {
	host := restHost(cfg)
	data, err := restGet(ctx, host, "/logs", cfg.Username, cfg.Password)
	if err != nil {
		return nil, err
	}

	var raw struct {
		List []map[string]interface{} `json:"list"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		// Try parsing as raw array
		var logs []map[string]interface{}
		if err2 := json.Unmarshal(data, &logs); err2 != nil {
			return nil, err
		}
		return logs, nil
	}
	return raw.List, nil
}
