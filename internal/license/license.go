// Package license handles license key validation for Velix API.
//
// License keys are Ed25519-signed JWTs containing plan limits.
// Validation is local (no network required). A periodic phone-home
// checks for revocation and reports usage.
package license

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"velix/internal/logger"

	"github.com/rs/zerolog"
)

// Ed25519 public key used to verify license JWTs.
// The private key lives ONLY on the license server.
const publicKeyB64 = "w+ksc78M6R6G/HLo4HPUUJT7rkNwCLle/KPALefX+Tc="

// License server base URL for phone-home.
const phoneHomeURL = "https://license.velix.dev/api/v1/license/validate"

// Plans defines the limits for each plan tier.
var Plans = map[string]PlanLimits{
	"free":       {MaxInstances: 2},
	"starter":    {MaxInstances: 10},
	"pro":        {MaxInstances: 50},
	"business":   {MaxInstances: 200},
	"enterprise": {MaxInstances: 0}, // 0 = unlimited
}

// PlanLimits holds the resource limits for a plan.
type PlanLimits struct {
	MaxInstances int
}

// Claims are the fields inside the license JWT.
type Claims struct {
	Sub           string `json:"sub"`            // user/workspace ID
	Email         string `json:"email"`
	Plan          string `json:"plan"`
	MaxInstances  int    `json:"max_instances"`  // overrides plan default if > 0
	IssuedAt      int64  `json:"iat"`
	ExpiresAt     int64  `json:"exp"`
}

// License holds a validated license state.
type License struct {
	Claims    Claims
	Limits    PlanLimits
	Valid     bool
	GraceDays int // days since last successful phone-home

	mu              sync.RWMutex
	lastPhoneHome   time.Time
	phoneHomeFailed bool
	log             zerolog.Logger
}

// Validate parses and verifies a license key string.
// Returns a License even if invalid (with Valid=false) so callers can inspect the error.
func Validate(key string) (*License, error) {
	l := &License{log: logger.New("license")}

	if key == "" {
		l.Valid = false
		return l, fmt.Errorf("LICENSE_KEY is not set")
	}

	claims, err := parseAndVerify(key)
	if err != nil {
		l.Valid = false
		return l, fmt.Errorf("invalid license: %w", err)
	}

	// Check expiration.
	if claims.ExpiresAt > 0 && time.Now().Unix() > claims.ExpiresAt {
		l.Claims = *claims
		l.Valid = false
		return l, fmt.Errorf("license expired on %s", time.Unix(claims.ExpiresAt, 0).Format("2006-01-02"))
	}

	l.Claims = *claims
	l.Valid = true

	// Resolve plan limits.
	if limits, ok := Plans[claims.Plan]; ok {
		l.Limits = limits
	} else {
		l.Limits = Plans["free"]
	}

	// MaxInstances override from claims takes priority.
	if claims.MaxInstances > 0 {
		l.Limits.MaxInstances = claims.MaxInstances
	}

	return l, nil
}

// MaxInstances returns the instance limit for this license.
// 0 means unlimited.
func (l *License) MaxInstances() int {
	if l == nil || !l.Valid {
		return 2 // free tier fallback
	}
	return l.Limits.MaxInstances
}

// Plan returns the plan name.
func (l *License) Plan() string {
	if l == nil || !l.Valid {
		return "free"
	}
	return l.Claims.Plan
}

// IsActive returns true if the license is valid, not expired, and within grace period.
func (l *License) IsActive() bool {
	if l == nil {
		return false
	}
	if !l.Valid {
		return false
	}
	// Check local expiration (no network needed).
	if l.Claims.ExpiresAt > 0 && time.Now().Unix() > l.Claims.ExpiresAt {
		l.mu.Lock()
		l.Valid = false
		l.mu.Unlock()
		l.log.Warn().Msg("License expired")
		return false
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	// Grace period: 30 days without phone-home → inactive.
	if l.phoneHomeFailed && l.GraceDays > 30 {
		return false
	}
	return true
}

// CanCreateInstance checks if creating one more instance is allowed.
func (l *License) CanCreateInstance(currentCount int) bool {
	max := l.MaxInstances()
	if max == 0 {
		return true // unlimited
	}
	return currentCount < max
}

// StartPhoneHome starts background goroutines that:
// 1. Check local expiration every hour (no network)
// 2. Validate against the license server every 24h (network)
func (l *License) StartPhoneHome(ctx context.Context, key string) {
	if l == nil || !l.Valid {
		return
	}
	l.mu.Lock()
	l.lastPhoneHome = time.Now()
	l.mu.Unlock()

	// Local expiration check — every hour, no network.
	go func() {
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if !l.IsActive() {
					l.log.Warn().Msg("License no longer active (expired or grace period exceeded)")
				}
			}
		}
	}()

	// Remote phone-home — every 24h.
	go func() {
		// First phone-home after 1 minute (give server time to fully start).
		timer := time.NewTimer(1 * time.Minute)
		defer timer.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
				l.doPhoneHome(ctx, key)
				timer.Reset(24 * time.Hour)
			}
		}
	}()
}

func (l *License) doPhoneHome(ctx context.Context, key string) {
	machineID := getMachineID()

	payload, _ := json.Marshal(map[string]string{
		"license_key": key,
		"machine_id":  machineID,
		"plan":        l.Claims.Plan,
	})

	reqCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, phoneHomeURL, strings.NewReader(string(payload)))
	if err != nil {
		l.markPhoneHomeFailed()
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		l.log.Debug().Err(err).Msg("Phone-home failed (network) — continuing with grace period")
		l.markPhoneHomeFailed()
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		l.mu.Lock()
		l.lastPhoneHome = time.Now()
		l.phoneHomeFailed = false
		l.GraceDays = 0
		l.mu.Unlock()
		l.log.Debug().Msg("Phone-home successful")
	} else if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusUnauthorized {
		// License revoked server-side.
		l.mu.Lock()
		l.Valid = false
		l.mu.Unlock()
		l.log.Warn().Int("status", resp.StatusCode).Msg("License revoked by server")
	} else {
		l.markPhoneHomeFailed()
	}
}

func (l *License) markPhoneHomeFailed() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.phoneHomeFailed = true
	if !l.lastPhoneHome.IsZero() {
		l.GraceDays = int(time.Since(l.lastPhoneHome).Hours() / 24)
	}
}

// --- JWT parsing (Ed25519) ---------------------------------------------------

func parseAndVerify(token string) (*Claims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("malformed JWT: expected 3 parts, got %d", len(parts))
	}

	// Decode public key.
	pubBytes, err := base64.StdEncoding.DecodeString(publicKeyB64)
	if err != nil {
		return nil, fmt.Errorf("invalid embedded public key: %w", err)
	}
	pubKey := ed25519.PublicKey(pubBytes)

	// Verify signature (header.payload against signature).
	signingInput := parts[0] + "." + parts[1]
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, fmt.Errorf("invalid signature encoding: %w", err)
	}

	if !ed25519.Verify(pubKey, []byte(signingInput), signature) {
		return nil, fmt.Errorf("invalid signature")
	}

	// Decode claims.
	claimsJSON, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("invalid payload encoding: %w", err)
	}

	var claims Claims
	if err := json.Unmarshal(claimsJSON, &claims); err != nil {
		return nil, fmt.Errorf("invalid claims: %w", err)
	}

	return &claims, nil
}

// getMachineID returns a stable hash of hostname + first MAC address.
func getMachineID() string {
	hostname, _ := os.Hostname()
	mac := ""
	if ifaces, err := net.Interfaces(); err == nil {
		for _, iface := range ifaces {
			if len(iface.HardwareAddr) > 0 {
				mac = iface.HardwareAddr.String()
				break
			}
		}
	}
	raw := hostname + "|" + mac
	hash := sha256.Sum256([]byte(raw))
	return base64.RawURLEncoding.EncodeToString(hash[:16])
}
