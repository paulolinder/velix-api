package license

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const (
	trialFileName = ".trial"
	TrialDays     = 14
)

// TrialInfo holds the trial state persisted to disk.
type TrialInfo struct {
	StartedAt time.Time `json:"started_at"`
}

// InitTrial reads or creates the trial file in dataDir.
// On first call it records the current time as trial start.
func InitTrial(dataDir string) (*TrialInfo, error) {
	path := filepath.Join(dataDir, trialFileName)

	data, err := os.ReadFile(path)
	if err == nil {
		// File exists — parse it.
		var t TrialInfo
		if jsonErr := json.Unmarshal(data, &t); jsonErr == nil && !t.StartedAt.IsZero() {
			return &t, nil
		}
	}

	// File missing or corrupt — start trial now.
	t := &TrialInfo{StartedAt: time.Now().UTC()}
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}
	raw, _ := json.Marshal(t)
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		return nil, fmt.Errorf("write trial file: %w", err)
	}
	return t, nil
}

// DaysRemaining returns how many days are left in the trial (0 if expired).
func (t *TrialInfo) DaysRemaining() int {
	if t == nil {
		return 0
	}
	elapsed := time.Since(t.StartedAt)
	remaining := TrialDays - int(elapsed.Hours()/24)
	if remaining < 0 {
		return 0
	}
	return remaining
}

// IsExpired returns true when the 14-day trial window has passed.
func (t *TrialInfo) IsExpired() bool {
	if t == nil {
		return true
	}
	return time.Since(t.StartedAt) > TrialDays*24*time.Hour
}
