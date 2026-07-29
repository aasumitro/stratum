package middleware

import (
	"crypto/hmac"
	"crypto/sha256"
)

// SecureCompare is a constant-time, length-independent equality check for
// shared secrets/tokens.
func SecureCompare(a, b string) bool {
	ah := sha256.Sum256([]byte(a))
	bh := sha256.Sum256([]byte(b))
	return hmac.Equal(ah[:], bh[:])
}
