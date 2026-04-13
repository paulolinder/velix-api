package license

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"
)

// testPrivKey is the private key matching publicKeyB64 in license.go.
const testPrivKey = "3X19gvejQu55iCjZ777cHeaXliUqtLg7oRe94Q1/7vDXIFvmDDjR3UlfkWgijJLKS6dyeukqR2z9cC9wwcLffw=="

func signJWT(claims map[string]any) string {
	privBytes, _ := base64.StdEncoding.DecodeString(testPrivKey)
	priv := ed25519.PrivateKey(privBytes)

	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"EdDSA","typ":"JWT"}`))
	payloadJSON, _ := json.Marshal(claims)
	payload := base64.RawURLEncoding.EncodeToString(payloadJSON)

	signingInput := header + "." + payload
	sig := ed25519.Sign(priv, []byte(signingInput))
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func TestValidate_ValidLicense(t *testing.T) {
	token := signJWT(map[string]any{
		"sub":   "test-user",
		"email": "test@velix.dev",
		"plan":  "pro",
		"iat":   time.Now().Unix(),
		"exp":   time.Now().Add(24 * time.Hour).Unix(),
	})

	lic, err := Validate(token)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !lic.Valid {
		t.Fatal("license should be valid")
	}
	if lic.Plan() != "pro" {
		t.Errorf("plan = %q, want %q", lic.Plan(), "pro")
	}
	if lic.MaxInstances() != 50 {
		t.Errorf("max_instances = %d, want 50", lic.MaxInstances())
	}
	if lic.Claims.Email != "test@velix.dev" {
		t.Errorf("email = %q, want %q", lic.Claims.Email, "test@velix.dev")
	}
}

func TestValidate_ExpiredLicense(t *testing.T) {
	token := signJWT(map[string]any{
		"sub":  "test",
		"plan": "pro",
		"iat":  time.Now().Add(-48 * time.Hour).Unix(),
		"exp":  time.Now().Add(-24 * time.Hour).Unix(),
	})

	lic, err := Validate(token)
	if err == nil {
		t.Fatal("expected error for expired license")
	}
	if lic.Valid {
		t.Fatal("expired license should not be valid")
	}
}

func TestValidate_EmptyKey(t *testing.T) {
	_, err := Validate("")
	if err == nil {
		t.Fatal("expected error for empty key")
	}
}

func TestValidate_TamperedPayload(t *testing.T) {
	token := signJWT(map[string]any{
		"sub":  "test",
		"plan": "free",
		"iat":  time.Now().Unix(),
		"exp":  time.Now().Add(24 * time.Hour).Unix(),
	})

	// Tamper with the payload (change "free" to "pro" in base64).
	parts := []byte(token)
	// Flip a byte in the payload section.
	parts[50] ^= 0x01
	tampered := string(parts)

	lic, err := Validate(tampered)
	if err == nil {
		t.Fatal("expected error for tampered token")
	}
	if lic.Valid {
		t.Fatal("tampered license should not be valid")
	}
}

func TestValidate_InvalidSignature(t *testing.T) {
	// Generate a JWT with a DIFFERENT private key.
	_, otherPriv, _ := ed25519.GenerateKey(nil)

	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"EdDSA","typ":"JWT"}`))
	payloadJSON, _ := json.Marshal(map[string]any{"sub": "hacker", "plan": "enterprise", "exp": time.Now().Add(time.Hour).Unix()})
	payload := base64.RawURLEncoding.EncodeToString(payloadJSON)

	signingInput := header + "." + payload
	sig := ed25519.Sign(otherPriv, []byte(signingInput))
	token := signingInput + "." + base64.RawURLEncoding.EncodeToString(sig)

	lic, err := Validate(token)
	if err == nil {
		t.Fatal("expected error for wrong-key signature")
	}
	if lic.Valid {
		t.Fatal("wrong-key license should not be valid")
	}
}

func TestValidate_MaxInstancesOverride(t *testing.T) {
	token := signJWT(map[string]any{
		"sub":            "test",
		"plan":           "free",
		"max_instances":  100,
		"iat":            time.Now().Unix(),
		"exp":            time.Now().Add(24 * time.Hour).Unix(),
	})

	lic, err := Validate(token)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if lic.MaxInstances() != 100 {
		t.Errorf("max_instances = %d, want 100 (override)", lic.MaxInstances())
	}
}

func TestCanCreateInstance(t *testing.T) {
	token := signJWT(map[string]any{
		"sub":  "test",
		"plan": "starter",
		"iat":  time.Now().Unix(),
		"exp":  time.Now().Add(24 * time.Hour).Unix(),
	})

	lic, _ := Validate(token)

	if !lic.CanCreateInstance(5) {
		t.Error("should allow 5 instances on starter (limit 10)")
	}
	if !lic.CanCreateInstance(9) {
		t.Error("should allow 9 instances on starter (limit 10)")
	}
	if lic.CanCreateInstance(10) {
		t.Error("should NOT allow 10 instances on starter (limit 10)")
	}
}

func TestPlanDefaults(t *testing.T) {
	cases := []struct {
		plan string
		want int
	}{
		{"free", 2},
		{"starter", 10},
		{"pro", 50},
		{"business", 200},
		{"enterprise", 0},
		{"unknown_plan", 2}, // falls back to free
	}

	for _, tc := range cases {
		token := signJWT(map[string]any{
			"sub":  "test",
			"plan": tc.plan,
			"iat":  time.Now().Unix(),
			"exp":  time.Now().Add(time.Hour).Unix(),
		})
		lic, _ := Validate(token)
		if lic.MaxInstances() != tc.want {
			t.Errorf("plan %q: max_instances = %d, want %d", tc.plan, lic.MaxInstances(), tc.want)
		}
	}
}
