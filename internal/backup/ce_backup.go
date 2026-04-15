package backup

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/balinderwalia/nebulacb/internal/models"
	"github.com/balinderwalia/nebulacb/pkg/couchbase"
)

// ceBackupWorkers controls the parallel KV fetch/upsert worker count.
// Tuned for NodePort-exposed clusters; raise via env if needed.
const ceBackupWorkers = 16

// ceExportRecord is the on-disk JSONL schema.
type ceExportRecord struct {
	Key   string          `json:"key"`
	Value json.RawMessage `json:"value"`
	CAS   uint64          `json:"cas,omitempty"`
	Err   string          `json:"error,omitempty"`
}

// runSDKBackup exports every document in cfg.Bucket to JSONL using the Go SDK.
//
// Pipeline: N1QL streams keys → worker pool fans out KV Gets → single writer
// serialises lines into the output file. Progress is reflected on backup.*
// atomically so the API/UI can poll.
func (m *Manager) runSDKBackup(ctx context.Context, cfg models.ClusterConfig, backup *models.BackupInfo) error {
	if m.pool == nil {
		return fmt.Errorf("SDK pool not configured")
	}
	client := m.pool.Get(cfg.Name)
	if client == nil {
		return fmt.Errorf("SDK client for %s not connected", cfg.Name)
	}

	repo := m.config.Repository
	if repo == "" {
		repo = "/tmp/nebulacb-backups"
	}
	outDir := filepath.Join(repo, backup.ID)
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", outDir, err)
	}
	backup.Path = outDir
	backup.Mode = "ce-sdk"

	outFile := filepath.Join(outDir, cfg.Bucket+".jsonl")
	f, err := os.Create(outFile)
	if err != nil {
		return fmt.Errorf("create %s: %w", outFile, err)
	}
	defer f.Close()

	w := bufio.NewWriterSize(f, 1<<20)
	defer w.Flush()

	keys := make(chan string, ceBackupWorkers*4)
	records := make(chan ceExportRecord, ceBackupWorkers*4)

	// Key producer — N1QL stream.
	var prodErr error
	prodDone := make(chan struct{})
	go func() {
		defer close(keys)
		defer close(prodDone)
		ks, err := client.GetAllKeys(ctx, "")
		if err != nil {
			prodErr = fmt.Errorf("list keys: %w", err)
			return
		}
		for _, k := range ks {
			select {
			case <-ctx.Done():
				return
			case keys <- k:
			}
		}
	}()

	// Fetch workers.
	var wg sync.WaitGroup
	for i := 0; i < ceBackupWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for k := range keys {
				raw, err := client.Get(ctx, k)
				rec := ceExportRecord{Key: k, Value: raw}
				if err != nil {
					rec.Err = err.Error()
					rec.Value = nil
				}
				select {
				case <-ctx.Done():
					return
				case records <- rec:
				}
			}
		}()
	}
	go func() { wg.Wait(); close(records) }()

	// Single writer — serialise JSONL.
	var docs, bytesOut uint64
	enc := json.NewEncoder(w)
	for rec := range records {
		if err := enc.Encode(rec); err != nil {
			return fmt.Errorf("encode %s: %w", rec.Key, err)
		}
		docs++
		bytesOut += uint64(len(rec.Value)) + uint64(len(rec.Key)) + 8
		if docs%500 == 0 {
			atomic.StoreUint64(&backup.DocsExported, docs)
			atomic.StoreUint64(&backup.BytesExported, bytesOut)
		}
	}
	<-prodDone
	if prodErr != nil {
		return prodErr
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}

	atomic.StoreUint64(&backup.DocsExported, docs)
	atomic.StoreUint64(&backup.BytesExported, bytesOut)
	backup.Size = fmt.Sprintf("%.1f MB", float64(bytesOut)/(1024*1024))

	// Metadata.
	meta := map[string]any{
		"backup_id":    backup.ID,
		"cluster_name": backup.ClusterName,
		"bucket":       cfg.Bucket,
		"mode":         "ce-sdk",
		"docs":         docs,
		"bytes":        bytesOut,
		"started_at":   backup.StartTime,
		"finished_at":  time.Now(),
		"note":         "CE backup — data only (indexes / cluster config not captured)",
	}
	metaFile := filepath.Join(outDir, "metadata.json")
	if mb, err := json.MarshalIndent(meta, "", "  "); err == nil {
		_ = os.WriteFile(metaFile, mb, 0o644)
	}
	return nil
}

// runSDKRestore reads JSONL files produced by runSDKBackup and upserts every
// document into the target cluster's configured bucket.
func (m *Manager) runSDKRestore(ctx context.Context, cfg models.ClusterConfig, restore *models.RestoreInfo) error {
	if m.pool == nil {
		return fmt.Errorf("SDK pool not configured")
	}
	client := m.pool.Get(cfg.Name)
	if client == nil {
		return fmt.Errorf("SDK client for %s not connected", cfg.Name)
	}

	repo := m.config.Repository
	if repo == "" {
		repo = "/tmp/nebulacb-backups"
	}
	dir := filepath.Join(repo, restore.BackupID)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("read backup dir %s: %w", dir, err)
	}

	restore.Mode = "ce-sdk"
	var totalDocs, totalErrs uint64

	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".jsonl" {
			continue
		}
		path := filepath.Join(dir, e.Name())
		docs, errs, err := importJSONL(ctx, client, path)
		totalDocs += docs
		totalErrs += errs
		atomic.StoreUint64(&restore.DocsRestored, totalDocs)
		atomic.StoreUint64(&restore.Errors, totalErrs)
		if err != nil {
			return fmt.Errorf("import %s: %w", e.Name(), err)
		}
	}

	restore.Progress = 100
	return nil
}

func importJSONL(ctx context.Context, client *couchbase.Client, path string) (uint64, uint64, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, 0, err
	}
	defer f.Close()

	lines := make(chan ceExportRecord, ceBackupWorkers*4)
	var wg sync.WaitGroup
	var docs, errs uint64

	for i := 0; i < ceBackupWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for rec := range lines {
				if rec.Err != "" || len(rec.Value) == 0 {
					atomic.AddUint64(&errs, 1)
					continue
				}
				var v any
				if err := json.Unmarshal(rec.Value, &v); err != nil {
					atomic.AddUint64(&errs, 1)
					continue
				}
				if err := client.Upsert(ctx, rec.Key, v); err != nil {
					atomic.AddUint64(&errs, 1)
					continue
				}
				atomic.AddUint64(&docs, 1)
			}
		}()
	}

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1<<20), 32<<20)
	for scanner.Scan() {
		if ctx.Err() != nil {
			break
		}
		var rec ceExportRecord
		if err := json.Unmarshal(scanner.Bytes(), &rec); err != nil {
			atomic.AddUint64(&errs, 1)
			continue
		}
		lines <- rec
	}
	close(lines)
	wg.Wait()

	if err := scanner.Err(); err != nil {
		return docs, errs, err
	}
	return docs, errs, nil
}
