package migration

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/balinderwalia/nebulacb/internal/metrics"
	"github.com/balinderwalia/nebulacb/internal/models"
	"github.com/balinderwalia/nebulacb/pkg/couchbase"
)

// Engine handles data migration between Couchbase clusters.
type Engine struct {
	config    models.MigrationConfig
	pool      *couchbase.ClientPool
	collector *metrics.Collector

	mu         sync.RWMutex
	migrations []models.MigrationStatus
	active     *models.MigrationStatus
	cancel     context.CancelFunc
}

// NewEngine creates a new migration engine.
func NewEngine(cfg models.MigrationConfig, pool *couchbase.ClientPool, collector *metrics.Collector) *Engine {
	if cfg.Workers == 0 {
		cfg.Workers = 8
	}
	if cfg.BatchSize == 0 {
		cfg.BatchSize = 1000
	}
	if cfg.RetryAttempts == 0 {
		cfg.RetryAttempts = 3
	}

	return &Engine{
		config:     cfg,
		pool:       pool,
		collector:  collector,
		migrations: make([]models.MigrationStatus, 0),
	}
}

// StartMigration begins a new data migration.
func (e *Engine) StartMigration(ctx context.Context, req models.MigrationRequest) (*models.MigrationStatus, error) {
	e.mu.Lock()
	if e.active != nil && e.active.Status == "running" {
		e.mu.Unlock()
		return nil, fmt.Errorf("migration already in progress: %s", e.active.ID)
	}

	sourceClient := e.pool.Get(req.SourceCluster)
	if sourceClient == nil {
		e.mu.Unlock()
		return nil, fmt.Errorf("source cluster %s not connected", req.SourceCluster)
	}

	targetClient := e.pool.Get(req.TargetCluster)
	if targetClient == nil {
		e.mu.Unlock()
		return nil, fmt.Errorf("target cluster %s not connected", req.TargetCluster)
	}

	migration := &models.MigrationStatus{
		ID:            fmt.Sprintf("mig-%d", time.Now().UnixNano()),
		SourceCluster: req.SourceCluster,
		TargetCluster: req.TargetCluster,
		SourceBucket:  req.SourceBucket,
		TargetBucket:  req.TargetBucket,
		Status:        "running",
		StartTime:     time.Now(),
		Timestamp:     time.Now(),
	}

	e.active = migration
	e.mu.Unlock()

	var migCtx context.Context
	migCtx, e.cancel = context.WithCancel(ctx)

	go e.runMigration(migCtx, sourceClient, targetClient, migration, req)

	log.Printf("[Migration] Started: %s (%s/%s -> %s/%s)",
		migration.ID, req.SourceCluster, req.SourceBucket, req.TargetCluster, req.TargetBucket)

	return migration, nil
}

func (e *Engine) runMigration(ctx context.Context, source, target *couchbase.Client, migration *models.MigrationStatus, req models.MigrationRequest) {
	defer func() {
		e.mu.Lock()
		e.migrations = append(e.migrations, *migration)
		if len(e.migrations) > 20 {
			e.migrations = e.migrations[len(e.migrations)-20:]
		}
		e.active = nil
		e.mu.Unlock()
	}()

	// Get all keys from source
	keys, err := source.GetAllKeys(ctx, "")
	if err != nil {
		migration.Status = "failed"
		migration.Errors = append(migration.Errors, fmt.Sprintf("failed to scan source keys: %v", err))
		migration.Timestamp = time.Now()
		return
	}

	migration.TotalDocs = uint64(len(keys))
	var migratedCount atomic.Uint64
	var failedCount atomic.Uint64

	// Process in batches with worker pool
	keyChan := make(chan string, e.config.BatchSize)
	var wg sync.WaitGroup

	// Start workers
	for i := 0; i < e.config.Workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for key := range keyChan {
				if ctx.Err() != nil {
					return
				}

				err := e.migrateDoc(ctx, source, target, key, req)
				if err != nil {
					failedCount.Add(1)
					if failedCount.Load() <= 10 {
						e.mu.Lock()
						migration.Errors = append(migration.Errors, fmt.Sprintf("key %s: %v", key, err))
						e.mu.Unlock()
					}
				} else {
					migratedCount.Add(1)
				}

				// Update progress periodically
				migrated := migratedCount.Load()
				if migrated%100 == 0 {
					e.mu.Lock()
					migration.MigratedDocs = migrated
					migration.FailedDocs = failedCount.Load()
					if migration.TotalDocs > 0 {
						migration.Progress = float64(migrated) / float64(migration.TotalDocs) * 100
					}
					elapsed := time.Since(migration.StartTime).Seconds()
					if elapsed > 0 {
						migration.Rate = float64(migrated) / elapsed
					}
					if migration.Rate > 0 {
						remaining := float64(migration.TotalDocs-migrated) / migration.Rate
						migration.EstimatedEnd = time.Now().Add(time.Duration(remaining) * time.Second)
					}
					migration.Timestamp = time.Now()
					e.mu.Unlock()
				}
			}
		}()
	}

	// Feed keys to workers
	for _, key := range keys {
		if ctx.Err() != nil {
			break
		}
		keyChan <- key
	}
	close(keyChan)
	wg.Wait()

	// Final status
	e.mu.Lock()
	migration.MigratedDocs = migratedCount.Load()
	migration.FailedDocs = failedCount.Load()
	migration.Progress = 100
	migration.Rate = float64(migration.MigratedDocs) / time.Since(migration.StartTime).Seconds()
	migration.Timestamp = time.Now()

	if ctx.Err() != nil {
		migration.Status = "cancelled"
	} else if migration.FailedDocs > 0 {
		migration.Status = "completed_with_errors"
	} else {
		migration.Status = "completed"
	}
	e.mu.Unlock()

	log.Printf("[Migration] %s: %s (migrated=%d, failed=%d, rate=%.0f docs/s, duration=%.1fs)",
		migration.ID, migration.Status, migration.MigratedDocs, migration.FailedDocs,
		migration.Rate, time.Since(migration.StartTime).Seconds())

	severity := "info"
	if migration.FailedDocs > 0 {
		severity = "warning"
	}
	e.collector.AddAlert(models.Alert{
		ID:        fmt.Sprintf("migration-%s-%d", migration.Status, time.Now().Unix()),
		Severity:  severity,
		Category:  "migration",
		Title:     fmt.Sprintf("Migration %s", migration.Status),
		Message:   fmt.Sprintf("Migrated %d/%d docs from %s to %s", migration.MigratedDocs, migration.TotalDocs, migration.SourceCluster, migration.TargetCluster),
		Source:    "migration",
		Timestamp: time.Now(),
	})
}

func (e *Engine) migrateDoc(ctx context.Context, source, target *couchbase.Client, key string, req models.MigrationRequest) error {
	// Read from source
	data, err := source.Get(ctx, key)
	if err != nil {
		return fmt.Errorf("read: %w", err)
	}

	// Apply transform rules if any
	if len(req.TransformRules) > 0 {
		data, err = applyTransforms(data, req.TransformRules)
		if err != nil {
			return fmt.Errorf("transform: %w", err)
		}
	}

	// Write to target with retries
	var lastErr error
	for attempt := 0; attempt < e.config.RetryAttempts; attempt++ {
		var doc json.RawMessage = data
		if err := target.Upsert(ctx, key, doc); err != nil {
			lastErr = err
			time.Sleep(time.Duration(attempt+1) * 100 * time.Millisecond)
			continue
		}
		return nil
	}
	return lastErr
}

func applyTransforms(data []byte, rules []models.TransformRule) ([]byte, error) {
	var doc map[string]interface{}
	if err := json.Unmarshal(data, &doc); err != nil {
		return data, nil // If not JSON, return as-is
	}

	for _, rule := range rules {
		switch rule.Type {
		case "rename_field":
			if val, ok := doc[rule.Field]; ok {
				doc[rule.NewField] = val
				delete(doc, rule.Field)
			}
		case "add_field":
			doc[rule.NewField] = rule.Value
		case "remove_field":
			delete(doc, rule.Field)
		case "convert_type":
			// Type conversions handled by the target
		}
	}

	return json.Marshal(doc)
}

// PauseMigration pauses the active migration.
func (e *Engine) PauseMigration() error {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.active == nil {
		return fmt.Errorf("no active migration")
	}
	if e.cancel != nil {
		e.cancel()
	}
	e.active.Status = "paused"
	return nil
}

// GetStatus returns the active migration status.
func (e *Engine) GetStatus() *models.MigrationStatus {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.active == nil {
		return nil
	}
	status := *e.active
	return &status
}

// GetHistory returns past migration records.
func (e *Engine) GetHistory() []models.MigrationStatus {
	e.mu.RLock()
	defer e.mu.RUnlock()
	result := make([]models.MigrationStatus, len(e.migrations))
	copy(result, e.migrations)
	return result
}
