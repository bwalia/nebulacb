package couchbase

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/balinderwalia/nebulacb/internal/models"
)

// RESTClient provides Couchbase operations via the REST/N1QL API,
// usable when the SDK (KV) is not available (e.g. port-forwarding).
type RESTClient struct {
	config     models.ClusterConfig
	httpClient *http.Client
	host       string // host:port for REST API
}

// NewRESTClient creates a REST-based Couchbase client.
func NewRESTClient(cfg models.ClusterConfig) *RESTClient {
	host := cfg.Host
	if strings.Contains(host, "://") {
		parts := strings.SplitN(host, "://", 2)
		host = parts[1]
	}
	if !strings.Contains(host, ":") {
		host = host + ":8091"
	}

	return &RESTClient{
		config:     cfg,
		httpClient: &http.Client{Timeout: 10 * time.Second},
		host:       host,
	}
}

// Upsert writes a document via the Couchbase REST API (document endpoint).
// For REST mode, the Payload field (raw bytes) is omitted to keep docs JSON-safe.
func (c *RESTClient) Upsert(ctx context.Context, id string, doc interface{}) error {
	// If it's a Document with binary Payload, create a REST-safe copy
	jsonDoc := doc
	if d, ok := doc.(models.Document); ok {
		jsonDoc = map[string]interface{}{
			"id":          d.ID,
			"sequence_id": d.SequenceID,
			"timestamp":   d.Timestamp,
			"region":      d.Region,
			"checksum":    d.Checksum,
			"size":        d.Size,
		}
	}

	body, err := json.Marshal(jsonDoc)
	if err != nil {
		return err
	}

	endpoint := fmt.Sprintf("http://%s/pools/default/buckets/%s/docs/%s",
		c.host, c.config.Bucket, url.PathEscape(id))

	form := "value=" + url.QueryEscape(string(body))
	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, strings.NewReader(form))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(c.config.Username, c.config.Password)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	io.ReadAll(resp.Body)

	if resp.StatusCode >= 400 {
		return fmt.Errorf("REST upsert failed: HTTP %d", resp.StatusCode)
	}
	return nil
}

// Get reads a document via the Couchbase REST API.
func (c *RESTClient) Get(ctx context.Context, id string) ([]byte, error) {
	docURL := fmt.Sprintf("http://%s/pools/default/buckets/%s/docs/%s",
		c.host, c.config.Bucket, url.PathEscape(id))

	req, err := http.NewRequestWithContext(ctx, "GET", docURL, nil)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(c.config.Username, c.config.Password)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == 404 {
		return nil, fmt.Errorf("document not found: %s", id)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	// The REST API wraps the doc in {"meta":{...},"json":{...}}
	var wrapper struct {
		JSON json.RawMessage `json:"json"`
	}
	if err := json.Unmarshal(body, &wrapper); err != nil {
		return body, nil
	}
	return wrapper.JSON, nil
}

// Remove deletes a document via the Couchbase REST API.
func (c *RESTClient) Remove(ctx context.Context, id string) error {
	docURL := fmt.Sprintf("http://%s/pools/default/buckets/%s/docs/%s",
		c.host, c.config.Bucket, url.PathEscape(id))

	req, err := http.NewRequestWithContext(ctx, "DELETE", docURL, nil)
	if err != nil {
		return err
	}
	req.SetBasicAuth(c.config.Username, c.config.Password)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	io.ReadAll(resp.Body)
	return nil
}

// DocCount returns the document count via the REST API.
func (c *RESTClient) DocCount(ctx context.Context) (uint64, error) {
	url := fmt.Sprintf("http://%s/pools/default/buckets/%s", c.host, c.config.Bucket)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return 0, err
	}
	req.SetBasicAuth(c.config.Username, c.config.Password)

	resp, err := c.httpClient.Do(req)
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

// UpsertN1QL writes a document via N1QL UPSERT query as a fallback.
func (c *RESTClient) UpsertN1QL(ctx context.Context, id string, doc interface{}) error {
	body, err := json.Marshal(doc)
	if err != nil {
		return err
	}

	query := fmt.Sprintf("UPSERT INTO `%s` (KEY, VALUE) VALUES (\"%s\", %s)",
		c.config.Bucket, id, string(body))

	payload, _ := json.Marshal(map[string]string{"statement": query})

	url := fmt.Sprintf("http://%s:8093/query/service", strings.Split(c.host, ":")[0])
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.SetBasicAuth(c.config.Username, c.config.Password)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	io.ReadAll(resp.Body)

	if resp.StatusCode >= 400 {
		return fmt.Errorf("N1QL upsert failed: HTTP %d", resp.StatusCode)
	}
	return nil
}

// Close is a no-op for the REST client.
func (c *RESTClient) Close() error {
	return nil
}
