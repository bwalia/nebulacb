package scenarios

import (
	"time"

	"github.com/balinderwalia/nebulacb/internal/models"
)

// BuiltInScenarios returns the predefined test scenarios.
func BuiltInScenarios() []models.Scenario {
	return []models.Scenario{
		HighLoadUpgrade(),
		XDCRInterruption(),
		NodeFailureDuringUpgrade(),
		MultiRegionSync(),
	}
}

// HighLoadUpgrade: continuous writes at scale + upgrade in parallel + validate zero data loss.
func HighLoadUpgrade() models.Scenario {
	return models.Scenario{
		Name:        "high_load_upgrade",
		Description: "Continuous writes at scale during rolling upgrade — validates zero data loss",
		Storm: models.StormConfig{
			WritesPerSecond:  5000,
			ReadsPerSecond:   2000,
			DocSizeMin:       1024,
			DocSizeMax:       65536,
			Workers:          32,
			Region:           "us-east-1",
			BurstEnabled:     true,
			BurstMultiplier:  5.0,
			BurstInterval:    60 * time.Second,
			BurstDuration:    15 * time.Second,
			HotKeyPercentage: 0.3,
			DeletePercentage: 0.02,
			UpdatePercentage: 0.40,
			KeyPrefix:        "nebula-hlu",
		},
		Upgrade: models.UpgradeConfig{
			SourceVersion:  "7.2.2",
			TargetVersion:  "7.6.0",
			RollingDelay:   30 * time.Second,
			MaxUnavailable: 1,
		},
		Validator: models.ValidatorConfig{
			BatchSize:     5000,
			ScanInterval:  15 * time.Second,
			HashCheck:     true,
			SequenceCheck: true,
			KeySampling:   1.0,
		},
		Duration: 30 * time.Minute,
	}
}

// XDCRInterruption: simulate topology change and validate 5-minute GOXDCR delay handling.
func XDCRInterruption() models.Scenario {
	return models.Scenario{
		Name:        "xdcr_interruption",
		Description: "Simulate topology change, validate GOXDCR 5-min delay handling and self-healing",
		Storm: models.StormConfig{
			WritesPerSecond:  2000,
			ReadsPerSecond:   1000,
			DocSizeMin:       512,
			DocSizeMax:       8192,
			Workers:          16,
			Region:           "eu-west-1",
			BurstEnabled:     false,
			HotKeyPercentage: 0.2,
			DeletePercentage: 0.01,
			UpdatePercentage: 0.25,
			KeyPrefix:        "nebula-xdcr",
		},
		XDCR: models.XDCRConfig{
			PollInterval:     3 * time.Second,
			GoxdcrDelayAlert: 5 * time.Minute,
		},
		Validator: models.ValidatorConfig{
			BatchSize:     5000,
			ScanInterval:  10 * time.Second,
			HashCheck:     true,
			SequenceCheck: true,
			KeySampling:   1.0,
		},
		Failures: []models.FailureSpec{
			{
				Type:     "network_delay",
				Target:   "xdcr-pipeline",
				Delay:    5 * time.Minute,
				Duration: 6 * time.Minute,
			},
		},
		Duration: 20 * time.Minute,
	}
}

// NodeFailureDuringUpgrade: kill an upgrading pod and validate recovery.
func NodeFailureDuringUpgrade() models.Scenario {
	return models.Scenario{
		Name:        "node_failure_during_upgrade",
		Description: "Kill upgrading pod during rolling upgrade — validate automatic recovery",
		Storm: models.StormConfig{
			WritesPerSecond:  3000,
			ReadsPerSecond:   1500,
			DocSizeMin:       1024,
			DocSizeMax:       32768,
			Workers:          24,
			Region:           "us-west-2",
			BurstEnabled:     false,
			HotKeyPercentage: 0.25,
			DeletePercentage: 0.03,
			UpdatePercentage: 0.35,
			KeyPrefix:        "nebula-nf",
		},
		Upgrade: models.UpgradeConfig{
			SourceVersion:  "7.2.2",
			TargetVersion:  "7.6.0",
			RollingDelay:   45 * time.Second,
			MaxUnavailable: 1,
		},
		Validator: models.ValidatorConfig{
			BatchSize:     5000,
			ScanInterval:  20 * time.Second,
			HashCheck:     true,
			SequenceCheck: true,
			KeySampling:   1.0,
		},
		Failures: []models.FailureSpec{
			{
				Type:     "kill_pod",
				Target:   "cb-cluster-0002",
				Delay:    5 * time.Minute,
				Duration: 0,
			},
		},
		Duration: 25 * time.Minute,
	}
}

// MultiRegionSync: write in region A, validate replication to region B during upgrade.
func MultiRegionSync() models.Scenario {
	return models.Scenario{
		Name:        "multi_region_sync",
		Description: "Write in region A, validate real-time replication to region B during upgrade",
		Storm: models.StormConfig{
			WritesPerSecond:  4000,
			ReadsPerSecond:   2000,
			DocSizeMin:       2048,
			DocSizeMax:       51200,
			Workers:          24,
			Region:           "us-east-1",
			BurstEnabled:     true,
			BurstMultiplier:  3.0,
			BurstInterval:    45 * time.Second,
			BurstDuration:    10 * time.Second,
			HotKeyPercentage: 0.15,
			DeletePercentage: 0.02,
			UpdatePercentage: 0.30,
			KeyPrefix:        "nebula-mr",
		},
		Upgrade: models.UpgradeConfig{
			SourceVersion:  "7.2.2",
			TargetVersion:  "7.6.0",
			RollingDelay:   60 * time.Second,
			MaxUnavailable: 1,
		},
		XDCR: models.XDCRConfig{
			PollInterval:     5 * time.Second,
			GoxdcrDelayAlert: 5 * time.Minute,
		},
		Validator: models.ValidatorConfig{
			BatchSize:     10000,
			ScanInterval:  30 * time.Second,
			HashCheck:     true,
			SequenceCheck: true,
			KeySampling:   1.0,
		},
		Duration: 30 * time.Minute,
	}
}
