// Package crypto provides cryptographic utility functions: hashing, HMAC,
// random generation, base64 helpers, and constant-time comparison.
package crypto

import (
	"crypto/hmac"
	"crypto/md5"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"math/big"
)

// -------------------------------------------------------------------
// Hash helpers
// -------------------------------------------------------------------

// HashSHA256 returns the hex-encoded SHA-256 of data.
func HashSHA256(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

// HashSHA256String is a convenience wrapper for string input.
func HashSHA256String(s string) string {
	return HashSHA256([]byte(s))
}

// HashMD5 returns the hex-encoded MD5 of data.
// MD5 is NOT cryptographically secure; use only for compatibility / checksums.
func HashMD5(data []byte) string {
	h := md5.Sum(data)
	return hex.EncodeToString(h[:])
}

// HashMD5String is a convenience wrapper for string input.
func HashMD5String(s string) string {
	return HashMD5([]byte(s))
}

// Djb2Hash implements the DJB2 non-cryptographic string hash, returning a
// signed 32-bit integer identical to the TypeScript implementation.
func Djb2Hash(s string) int32 {
	var h int32
	for i := 0; i < len(s); i++ {
		h = ((h << 5) - h + int32(s[i]))
	}
	return h
}

// -------------------------------------------------------------------
// Random generation
// -------------------------------------------------------------------

// RandomBytes returns n cryptographically random bytes.
func RandomBytes(n int) ([]byte, error) {
	buf := make([]byte, n)
	if _, err := io.ReadFull(rand.Reader, buf); err != nil {
		return nil, fmt.Errorf("crypto/rand: %w", err)
	}
	return buf, nil
}

// RandomHex returns a hex string of n random bytes (2n hex chars).
func RandomHex(n int) (string, error) {
	b, err := RandomBytes(n)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

const alphanumChars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

// RandomString returns a cryptographically random alphanumeric string of length n.
func RandomString(n int) (string, error) {
	max := big.NewInt(int64(len(alphanumChars)))
	buf := make([]byte, n)
	for i := range buf {
		idx, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		buf[i] = alphanumChars[idx.Int64()]
	}
	return string(buf), nil
}

// -------------------------------------------------------------------
// HMAC
// -------------------------------------------------------------------

// HMACSHA256 computes the HMAC-SHA256 of message with key and returns hex.
func HMACSHA256(key, message []byte) string {
	mac := hmac.New(sha256.New, key)
	mac.Write(message)
	return hex.EncodeToString(mac.Sum(nil))
}

// VerifyHMACSHA256 checks whether hexMAC is the valid HMAC-SHA256 of message
// under key, using constant-time comparison.
func VerifyHMACSHA256(key, message []byte, hexMAC string) bool {
	expected := HMACSHA256(key, message)
	return SecureCompare(expected, hexMAC)
}

// -------------------------------------------------------------------
// Base64
// -------------------------------------------------------------------

// Base64Encode returns standard base64 encoding of data.
func Base64Encode(data []byte) string {
	return base64.StdEncoding.EncodeToString(data)
}

// Base64Decode decodes a standard base64 string.
func Base64Decode(s string) ([]byte, error) {
	return base64.StdEncoding.DecodeString(s)
}

// Base64URLEncode returns URL-safe base64 encoding (no padding).
func Base64URLEncode(data []byte) string {
	return base64.RawURLEncoding.EncodeToString(data)
}

// Base64URLDecode decodes a URL-safe base64 string (no padding).
func Base64URLDecode(s string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(s)
}

// -------------------------------------------------------------------
// Constant-time comparison
// -------------------------------------------------------------------

// SecureCompare returns true iff a == b in constant time.
// Both strings are compared as raw bytes.
func SecureCompare(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
