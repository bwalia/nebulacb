package couchbase

import (
	"log"
	"sync"

	"github.com/balinderwalia/nebulacb/internal/models"
)

// ClientPool manages connections to multiple Couchbase clusters.
type ClientPool struct {
	mu      sync.RWMutex
	clients map[string]*Client
	configs map[string]models.ClusterConfig
}

// NewClientPool connects to all clusters in the registry.
// Failures are logged as warnings — the pool continues with available clusters.
func NewClientPool(clusters map[string]models.ClusterConfig) *ClientPool {
	pool := &ClientPool{
		clients: make(map[string]*Client, len(clusters)),
		configs: clusters,
	}

	for name, cfg := range clusters {
		if cfg.Host == "" {
			log.Printf("[Pool] Skipping cluster %s — no host configured", name)
			continue
		}
		client, err := NewClient(cfg)
		if err != nil {
			log.Printf("[Pool] WARN: cluster %s (%s) not reachable: %v", name, cfg.Host, err)
			continue
		}
		pool.clients[name] = client
		log.Printf("[Pool] Connected to cluster %s (%s) bucket=%s role=%s",
			name, cfg.Host, cfg.Bucket, cfg.Role)
	}

	log.Printf("[Pool] %d/%d clusters connected", len(pool.clients), len(clusters))
	return pool
}

// Get returns a client by cluster name, or nil if not connected.
func (p *ClientPool) Get(name string) *Client {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.clients[name]
}

// GetByRole returns the first client matching the given role.
func (p *ClientPool) GetByRole(role string) *Client {
	p.mu.RLock()
	defer p.mu.RUnlock()
	for name, cfg := range p.configs {
		if cfg.Role == role {
			return p.clients[name]
		}
	}
	return nil
}

// GetNameByRole returns the cluster name for a given role.
func (p *ClientPool) GetNameByRole(role string) string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	for name, cfg := range p.configs {
		if cfg.Role == role {
			return name
		}
	}
	return ""
}

// Names returns all cluster names.
func (p *ClientPool) Names() []string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	names := make([]string, 0, len(p.configs))
	for name := range p.configs {
		names = append(names, name)
	}
	return names
}

// Configs returns the full cluster config map.
func (p *ClientPool) Configs() map[string]models.ClusterConfig {
	p.mu.RLock()
	defer p.mu.RUnlock()
	result := make(map[string]models.ClusterConfig, len(p.configs))
	for k, v := range p.configs {
		result[k] = v
	}
	return result
}

// Connected returns names of clusters that are actually connected.
func (p *ClientPool) Connected() []string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	names := make([]string, 0, len(p.clients))
	for name := range p.clients {
		names = append(names, name)
	}
	return names
}

// Close disconnects all clients.
func (p *ClientPool) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for name, client := range p.clients {
		if err := client.Close(); err != nil {
			log.Printf("[Pool] Error closing %s: %v", name, err)
		}
	}
	p.clients = make(map[string]*Client)
}
