// Package pair provides helpers for generating and validating the pairing
// token used to authenticate the GateShell mobile app against this agent's
// REST/WebSocket API (see internal/api's bearer-token middleware).
//
// v1 is a single static long-lived token, generated once (at install time
// or via `gateshell-agent pair`) and shared with the app out-of-band (QR
// code, manual entry, AirDrop, etc. -- mirroring the existing iOS Handoff/
// config-share flow). There is no token rotation or multi-device pairing
// list yet.
package pair

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base32"
	"fmt"
	"strings"
)

// TokenByteLength is the amount of entropy (in bytes) used to generate a new
// pairing token. 20 bytes -> 32 base32 characters, comparable to a TOTP
// secret's strength.
const TokenByteLength = 20

// GenerateToken returns a new cryptographically random pairing token,
// base32-encoded (Crockford-style alphabet minus padding) for easy manual
// entry / QR-code display.
func GenerateToken() (string, error) {
	buf := make([]byte, TokenByteLength)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("pair: generating random token: %w", err)
	}

	encoded := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(buf)
	return strings.ToLower(encoded), nil
}

// Validate reports whether candidate matches expected, using a
// constant-time comparison to avoid leaking timing information about the
// token to a network attacker.
func Validate(expected, candidate string) bool {
	if expected == "" {
		// No token configured means pairing has not been set up; refuse
		// everything rather than accepting an empty token as a wildcard.
		return false
	}
	return subtle.ConstantTimeCompare([]byte(expected), []byte(candidate)) == 1
}

// TODO: support multiple concurrently-valid tokens (e.g. one per paired
// device) and revocation, once the app side needs more than a single
// shared secret. That will likely move token storage into internal/store
// alongside a small "devices" table.
