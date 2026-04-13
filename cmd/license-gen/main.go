// license-gen generates signed license keys for Velix API.
//
// Usage:
//
//	go run ./cmd/license-gen \
//	  -email user@example.com \
//	  -plan pro \
//	  -days 365
//
// The VELIX_LICENSE_PRIVATE_KEY env var must contain the base64-encoded Ed25519 private key.
package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/google/uuid"
)

func main() {
	email := flag.String("email", "", "Customer email (required)")
	plan := flag.String("plan", "pro", "Plan: free|starter|pro|business|enterprise")
	days := flag.Int("days", 365, "Days until expiration (0 = never expires)")
	maxInst := flag.Int("max-instances", 0, "Override max instances (0 = use plan default)")
	flag.Parse()

	if *email == "" {
		fmt.Fprintln(os.Stderr, "Error: -email is required")
		flag.Usage()
		os.Exit(1)
	}

	privB64 := os.Getenv("VELIX_LICENSE_PRIVATE_KEY")
	if privB64 == "" {
		fmt.Fprintln(os.Stderr, "Error: VELIX_LICENSE_PRIVATE_KEY env var is required")
		os.Exit(1)
	}

	privBytes, err := base64.StdEncoding.DecodeString(privB64)
	if err != nil || len(privBytes) != ed25519.PrivateKeySize {
		fmt.Fprintln(os.Stderr, "Error: invalid private key")
		os.Exit(1)
	}
	privKey := ed25519.PrivateKey(privBytes)

	now := time.Now()
	claims := map[string]any{
		"sub":   uuid.NewString(),
		"email": *email,
		"plan":  *plan,
		"iat":   now.Unix(),
	}
	if *days > 0 {
		claims["exp"] = now.AddDate(0, 0, *days).Unix()
	}
	if *maxInst > 0 {
		claims["max_instances"] = *maxInst
	}

	// Build JWT: header.payload.signature
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"EdDSA","typ":"JWT"}`))
	payloadJSON, _ := json.Marshal(claims)
	payload := base64.RawURLEncoding.EncodeToString(payloadJSON)

	signingInput := header + "." + payload
	signature := ed25519.Sign(privKey, []byte(signingInput))
	sig := base64.RawURLEncoding.EncodeToString(signature)

	token := signingInput + "." + sig

	fmt.Println(token)

	// Also print decoded claims for verification.
	fmt.Fprintln(os.Stderr, "\nClaims:")
	enc := json.NewEncoder(os.Stderr)
	enc.SetIndent("", "  ")
	_ = enc.Encode(claims)
}
