package validator

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/balinderwalia/nebulacb/internal/metrics"
	"github.com/balinderwalia/nebulacb/internal/models"
	"github.com/balinderwalia/nebulacb/pkg/couchbase"
)

// Validator performs data integrity checks between source and target clusters.
type Validator struct {
	config    models.ValidatorConfig
	source    *couchbase.Client
	target    *couchbase.Client
	sourceREST *couchbase.RESTClient
	targetREST *couchbase.RESTClient
	collector *metrics.Collector
	keyPrefix string

	mu     sync.RWMutex
	result models.IntegrityResult
	cancel context.CancelFunc
}

// NewValidator creates a new Data Integrity Validator.
func NewValidator(cfg models.ValidatorConfig, source, target *couchbase.Client, collector *metrics.Collector, keyPrefix string) *Validator {
	return &Validator{
		config:    cfg,
		source:    source,
		target:    target,
		collector: collector,
		keyPrefix: keyPrefix,
		result: models.IntegrityResult{
			Status: "idle",
		},
	}
}

// SetRESTClients sets REST-based fallback clients for when SDK is unavailable.
func (v *Validator) SetRESTClients(source, target *couchbase.RESTClient) {
	v.sourceREST = source
	v.targetREST = target
}

// StartContinuous begins continuous validation.
func (v *Validator) StartContinuous(ctx context.Context) error {
	if v.source == nil || v.target == nil {
		return fmt.Errorf("no Couchbase SDK connection — continuous validation requires direct cluster access")
	}
	// Use a detached context so validation outlives the HTTP request
	ctx, v.cancel = context.WithCancel(context.Background())
	log.Printf("[Validator] Starting continuous validation (interval: %s)", v.config.ScanInterval)

	go v.continuousLoop(ctx)
	return nil
}

// Stop halts continuous validation.
func (v *Validator) Stop() {
	if v.cancel != nil {
		v.cancel()
	}
	log.Println("[Validator] Stopped")
}

// CanAudit checks whether the validator has the required connections (SDK or REST).
func (v *Validator) CanAudit() error {
	if (v.source != nil && v.target != nil) || (v.sourceREST != nil && v.targetREST != nil) {
		return nil
	}
	return fmt.Errorf("no Couchbase connection — audit requires SDK or REST cluster access")
}

// hasSDK returns true if both SDK clients are available.
func (v *Validator) hasSDK() bool {
	return v.source != nil && v.target != nil
}

// RunFullAudit performs a single full data audit (SDK or REST-based).
func (v *Validator) RunFullAudit(ctx context.Context) (models.IntegrityResult, error) {
	if !v.hasSDK() && (v.sourceREST == nil || v.targetREST == nil) {
		return models.IntegrityResult{Status: "fail"}, fmt.Errorf("no Couchbase connection — audit requires SDK or REST cluster access")
	}

	// Use REST-based audit when SDK is not available
	if !v.hasSDK() {
		return v.runRESTAudit(ctx)
	}
	log.Println("[Validator] Starting full data audit...")
	start := time.Now()

	v.mu.Lock()
	v.result = models.IntegrityResult{
		RunID:     fmt.Sprintf("audit-%d", time.Now().Unix()),
		Status:    "running",
		Timestamp: time.Now(),
	}
	v.mu.Unlock()
	v.publishResult()

	result, err := v.performAudit(ctx)
	if err != nil {
		v.mu.Lock()
		v.result.Status = "fail"
		v.mu.Unlock()
		v.publishResult()
		return result, err
	}

	result.Duration = time.Since(start).Seconds()
	result.RunID = v.result.RunID
	result.Timestamp = time.Now()

	if result.MissingCount == 0 && result.HashMismatches == 0 && len(result.SequenceGaps) == 0 {
		result.Status = "pass"
		log.Printf("[Validator] Audit PASSED: %d docs, 0 missing, 0 mismatches (%.1fs)",
			result.SourceDocs, result.Duration)
	} else {
		result.Status = "fail"
		log.Printf("[Validator] Audit FAILED: %d missing, %d mismatches (%.1fs)",
			result.MissingCount, result.HashMismatches, result.Duration)

		v.collector.AddAlert(models.Alert{
			ID:        fmt.Sprintf("integrity-fail-%d", time.Now().Unix()),
			Severity:  "critical",
			Category:  "data_loss",
			Title:     "Data Integrity Check Failed",
			Message:   fmt.Sprintf("Missing: %d, Mismatches: %d (%.2f%%)", result.MissingCount, result.HashMismatches, result.MismatchPercent),
			Source:    "validator",
			Timestamp: time.Now(),
		})
	}

	v.mu.Lock()
	v.result = result
	v.mu.Unlock()
	v.publishResult()

	return result, nil
}

// GetResult returns the latest validation result.
func (v *Validator) GetResult() models.IntegrityResult {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.result
}

func (v *Validator) continuousLoop(ctx context.Context) {
	// Run initial audit
	v.RunFullAudit(ctx)

	ticker := time.NewTicker(v.config.ScanInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			v.RunFullAudit(ctx)
		}
	}
}

func (v *Validator) performAudit(ctx context.Context) (models.IntegrityResult, error) {
	result := models.IntegrityResult{}

	// Step 1: Compare document counts
	sourceCount, err := v.source.DocCount(ctx)
	if err != nil {
		return result, fmt.Errorf("source doc count: %w", err)
	}
	targetCount, err := v.target.DocCount(ctx)
	if err != nil {
		return result, fmt.Errorf("target doc count: %w", err)
	}

	result.SourceDocs = sourceCount
	result.TargetDocs = targetCount

	// Step 2: Get all keys from source
	sourceKeys, err := v.source.GetAllKeys(ctx, v.keyPrefix)
	if err != nil {
		return result, fmt.Errorf("source key scan: %w", err)
	}

	// Step 3: Check keys in batches on target
	var missingKeys []string
	var hashMismatches uint64

	for i := 0; i < len(sourceKeys); i += v.config.BatchSize {
		end := i + v.config.BatchSize
		if end > len(sourceKeys) {
			end = len(sourceKeys)
		}
		batch := sourceKeys[i:end]

		// Process batch concurrently
		var batchMu sync.Mutex
		var wg sync.WaitGroup

		for _, key := range batch {
			select {
			case <-ctx.Done():
				return result, ctx.Err()
			default:
			}

			wg.Add(1)
			go func(k string) {
				defer wg.Done()

				// Get source document
				sourceDoc, err := v.source.Get(ctx, k)
				if err != nil {
					return
				}

				// Get target document
				targetDoc, err := v.target.Get(ctx, k)
				if err != nil {
					batchMu.Lock()
					missingKeys = append(missingKeys, k)
					batchMu.Unlock()
					return
				}

				// Hash comparison
				if v.config.HashCheck {
					sourceHash := couchbase.HashDocument(sourceDoc)
					targetHash := couchbase.HashDocument(targetDoc)
					if sourceHash != targetHash {
						batchMu.Lock()
						hashMismatches++
						batchMu.Unlock()
					}
				}
			}(key)
		}
		wg.Wait()
	}

	result.MissingKeys = missingKeys
	result.MissingCount = uint64(len(missingKeys))
	result.HashMismatches = hashMismatches

	if result.SourceDocs > 0 {
		result.MismatchPercent = float64(result.HashMismatches+result.MissingCount) / float64(result.SourceDocs) * 100
	}

	// Step 4: Sequence continuity check
	if v.config.SequenceCheck {
		result.SequenceGaps = v.checkSequenceGaps(ctx, sourceKeys)
	}

	return result, nil
}

func (v *Validator) checkSequenceGaps(ctx context.Context, keys []string) []uint64 {
	// Extract sequence IDs from keys and check for gaps
	// Keys are in format: prefix::region::sequence
	var gaps []uint64
	seqMap := make(map[uint64]bool)

	for _, key := range keys {
		var seq uint64
		// Parse sequence from key
		n, err := fmt.Sscanf(key, v.keyPrefix+"::%*[^:]:%d", &seq)
		if err != nil || n == 0 {
			continue
		}
		seqMap[seq] = true
	}

	if len(seqMap) == 0 {
		return gaps
	}

	// Find max sequence
	var maxSeq uint64
	for seq := range seqMap {
		if seq > maxSeq {
			maxSeq = seq
		}
	}

	// Check for gaps (sample first 10000 sequences to avoid excessive checks)
	limit := maxSeq
	if limit > 10000 {
		limit = 10000
	}

	for i := uint64(1); i <= limit; i++ {
		if !seqMap[i] {
			gaps = append(gaps, i)
			if len(gaps) > 100 { // Cap gap reporting
				break
			}
		}
	}

	return gaps
}

func (v *Validator) publishResult() {
	v.collector.SetIntegrityResult(v.result)
}

// runRESTAudit performs a lightweight audit using REST API (doc count comparison + sample checks).
func (v *Validator) runRESTAudit(ctx context.Context) (models.IntegrityResult, error) {
	log.Println("[Validator] Starting REST-based audit (doc count + sample checks)...")
	start := time.Now()

	v.mu.Lock()
	v.result = models.IntegrityResult{
		RunID:     fmt.Sprintf("audit-rest-%d", time.Now().Unix()),
		Status:    "running",
		Timestamp: time.Now(),
	}
	v.mu.Unlock()
	v.publishResult()

	result := models.IntegrityResult{
		RunID:     v.result.RunID,
		Timestamp: time.Now(),
	}

	// Step 1: Compare document counts via REST
	sourceCount, err := v.sourceREST.DocCount(ctx)
	if err != nil {
		result.Status = "fail"
		v.mu.Lock()
		v.result = result
		v.mu.Unlock()
		v.publishResult()
		return result, fmt.Errorf("source doc count (REST): %w", err)
	}

	targetCount, err := v.targetREST.DocCount(ctx)
	if err != nil {
		result.Status = "fail"
		v.mu.Lock()
		v.result = result
		v.mu.Unlock()
		v.publishResult()
		return result, fmt.Errorf("target doc count (REST): %w", err)
	}

	result.SourceDocs = sourceCount
	result.TargetDocs = targetCount

	if sourceCount > targetCount {
		result.MissingCount = sourceCount - targetCount
	} else if targetCount > sourceCount {
		result.ExtraCount = targetCount - sourceCount
	}

	if result.SourceDocs > 0 {
		result.MismatchPercent = float64(result.MissingCount) / float64(result.SourceDocs) * 100
	}

	result.Duration = time.Since(start).Seconds()

	// Step 2: Sample key checks (spot-check a few known keys via REST)
	sampleKeys := []string{}
	for i := uint64(1); i <= 10; i++ {
		sampleKeys = append(sampleKeys, fmt.Sprintf("%s::%s:%d", v.keyPrefix, "us-east-1", i))
	}

	var hashPassed, hashFailed uint64
	for _, key := range sampleKeys {
		srcDoc, srcErr := v.sourceREST.Get(ctx, key)
		tgtDoc, tgtErr := v.targetREST.Get(ctx, key)
		if srcErr != nil || tgtErr != nil {
			if srcErr == nil && tgtErr != nil {
				hashFailed++
			}
			continue
		}
		srcHash := couchbase.HashDocument(srcDoc)
		tgtHash := couchbase.HashDocument(tgtDoc)
		if srcHash == tgtHash {
			hashPassed++
		} else {
			hashFailed++
		}
	}

	result.HashMismatches = hashFailed

	if result.MissingCount == 0 && result.HashMismatches == 0 {
		result.Status = "pass"
		log.Printf("[Validator] REST Audit PASSED: source=%d target=%d, %d samples verified (%.1fs)",
			result.SourceDocs, result.TargetDocs, hashPassed, result.Duration)
	} else {
		result.Status = "fail"
		log.Printf("[Validator] REST Audit FAILED: missing=%d, mismatches=%d (%.1fs)",
			result.MissingCount, result.HashMismatches, result.Duration)

		v.collector.AddAlert(models.Alert{
			ID:        fmt.Sprintf("integrity-fail-%d", time.Now().Unix()),
			Severity:  "critical",
			Category:  "data_loss",
			Title:     "Data Integrity Check Failed (REST)",
			Message:   fmt.Sprintf("Doc count mismatch: source=%d target=%d, hash mismatches=%d", result.SourceDocs, result.TargetDocs, result.HashMismatches),
			Source:    "validator",
			Timestamp: time.Now(),
		})
	}

	v.mu.Lock()
	v.result = result
	v.mu.Unlock()
	v.publishResult()

	return result, nil
}
