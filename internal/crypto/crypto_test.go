package crypto

import (
	"encoding/hex"
	"strings"
	"testing"
)

// -------------------------------------------------------------------
// Hash functions
// -------------------------------------------------------------------

func TestHashSHA256_KnownVector(t *testing.T) {
	// Known SHA-256 of "abc"
	const want = "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"
	got := HashSHA256([]byte("abc"))
	if got != want {
		t.Errorf("HashSHA256(\"abc\") = %q, want %q", got, want)
	}
	if HashSHA256String("abc") != want {
		t.Errorf("HashSHA256String mismatch with HashSHA256")
	}
}

func TestHashSHA256_EmptyAndDeterministic(t *testing.T) {
	const emptyHash = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	if got := HashSHA256(nil); got != emptyHash {
		t.Errorf("HashSHA256(nil) = %q, want %q", got, emptyHash)
	}
	// Deterministic
	a := HashSHA256([]byte("polypod"))
	b := HashSHA256([]byte("polypod"))
	if a != b {
		t.Errorf("SHA256 not deterministic: %q vs %q", a, b)
	}
	// Different input → different hash
	if HashSHA256([]byte("polypod")) == HashSHA256([]byte("polypoD")) {
		t.Error("SHA256 collision on trivial input")
	}
}

func TestHashMD5_KnownVector(t *testing.T) {
	// Known MD5 of "abc"
	const want = "900150983cd24fb0d6963f7d28e17f72"
	if got := HashMD5([]byte("abc")); got != want {
		t.Errorf("HashMD5(\"abc\") = %q, want %q", got, want)
	}
	if HashMD5String("abc") != want {
		t.Errorf("HashMD5String mismatch with HashMD5")
	}
}

func TestDjb2Hash(t *testing.T) {
	// Empty string → 0
	if got := Djb2Hash(""); got != 0 {
		t.Errorf("Djb2Hash(\"\") = %d, want 0", got)
	}
	// Deterministic
	if Djb2Hash("hello") != Djb2Hash("hello") {
		t.Error("Djb2Hash not deterministic")
	}
	// Different input → different hash (highly likely)
	if Djb2Hash("hello") == Djb2Hash("world") {
		t.Error("Djb2Hash collision on trivial input")
	}
	// Manual computation for "a": h=0; h = (0<<5)-0 + 'a' = 97
	if got := Djb2Hash("a"); got != 97 {
		t.Errorf("Djb2Hash(\"a\") = %d, want 97", got)
	}
}

// -------------------------------------------------------------------
// Random generation
// -------------------------------------------------------------------

func TestRandomBytes(t *testing.T) {
	for _, n := range []int{0, 1, 16, 32, 256} {
		b, err := RandomBytes(n)
		if err != nil {
			t.Fatalf("RandomBytes(%d) error: %v", n, err)
		}
		if len(b) != n {
			t.Errorf("RandomBytes(%d) returned %d bytes", n, len(b))
		}
	}
	// Two calls should not produce identical output (probabilistic).
	a, _ := RandomBytes(32)
	b, _ := RandomBytes(32)
	if string(a) == string(b) {
		t.Error("RandomBytes returned identical 32-byte buffers in succession")
	}
}

func TestRandomHex(t *testing.T) {
	s, err := RandomHex(16)
	if err != nil {
		t.Fatalf("RandomHex error: %v", err)
	}
	if len(s) != 32 {
		t.Errorf("RandomHex(16) length = %d, want 32", len(s))
	}
	if _, err := hex.DecodeString(s); err != nil {
		t.Errorf("RandomHex returned non-hex string: %v", err)
	}
}

func TestRandomString(t *testing.T) {
	s, err := RandomString(64)
	if err != nil {
		t.Fatalf("RandomString error: %v", err)
	}
	if len(s) != 64 {
		t.Errorf("RandomString(64) length = %d, want 64", len(s))
	}
	const allowed = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	for i, c := range s {
		if !strings.ContainsRune(allowed, c) {
			t.Errorf("RandomString char %d (%q) outside alphanumeric set", i, c)
		}
	}
}

// -------------------------------------------------------------------
// HMAC
// -------------------------------------------------------------------

func TestHMACSHA256_KnownVector(t *testing.T) {
	// RFC 4231 test case 1: key=0x0b*20, data="Hi There"
	key := []byte{0x0b, 0x0b, 0x0b, 0x0b, 0x0b, 0x0b, 0x0b, 0x0b, 0x0b, 0x0b,
		0x0b, 0x0b, 0x0b, 0x0b, 0x0b, 0x0b, 0x0b, 0x0b, 0x0b, 0x0b}
	const want = "b0344c61d8db38535ca8afceaf0bf12b881dc200c9833da726e9376c2e32cff7"
	if got := HMACSHA256(key, []byte("Hi There")); got != want {
		t.Errorf("HMACSHA256 = %q, want %q", got, want)
	}
}

func TestVerifyHMACSHA256_RoundTrip(t *testing.T) {
	key := []byte("super-secret-key")
	msg := []byte("authenticated message")
	mac := HMACSHA256(key, msg)

	if !VerifyHMACSHA256(key, msg, mac) {
		t.Error("VerifyHMACSHA256 failed for valid MAC")
	}
	// Wrong key
	if VerifyHMACSHA256([]byte("wrong-key"), msg, mac) {
		t.Error("VerifyHMACSHA256 accepted wrong key")
	}
	// Tampered message
	if VerifyHMACSHA256(key, []byte("tampered message"), mac) {
		t.Error("VerifyHMACSHA256 accepted tampered message")
	}
	// Corrupted MAC
	corrupted := "0" + mac[1:]
	if corrupted == mac {
		corrupted = "1" + mac[1:]
	}
	if VerifyHMACSHA256(key, msg, corrupted) {
		t.Error("VerifyHMACSHA256 accepted corrupted MAC")
	}
	// Empty MAC
	if VerifyHMACSHA256(key, msg, "") {
		t.Error("VerifyHMACSHA256 accepted empty MAC")
	}
}

// -------------------------------------------------------------------
// Base64 round-trip / error cases
// -------------------------------------------------------------------

func TestBase64StdRoundTrip(t *testing.T) {
	cases := [][]byte{
		nil,
		[]byte(""),
		[]byte("a"),
		[]byte("hello, world"),
		{0x00, 0xff, 0x10, 0x80, 0x7f},
	}
	for _, in := range cases {
		enc := Base64Encode(in)
		dec, err := Base64Decode(enc)
		if err != nil {
			t.Errorf("Base64Decode(%q) error: %v", enc, err)
			continue
		}
		if string(dec) != string(in) {
			t.Errorf("Base64 round trip mismatch: in=%v out=%v", in, dec)
		}
	}
}

func TestBase64URLRoundTrip(t *testing.T) {
	in := []byte{0xfb, 0xff, 0xbf, 0x00, 0x01, 0x02}
	enc := Base64URLEncode(in)
	// URL-safe should not contain '+' or '/' or '='
	if strings.ContainsAny(enc, "+/=") {
		t.Errorf("Base64URLEncode produced disallowed chars: %q", enc)
	}
	dec, err := Base64URLDecode(enc)
	if err != nil {
		t.Fatalf("Base64URLDecode error: %v", err)
	}
	if string(dec) != string(in) {
		t.Errorf("Base64URL round trip mismatch: in=%v out=%v", in, dec)
	}
}

func TestBase64Decode_Errors(t *testing.T) {
	// Invalid std base64
	if _, err := Base64Decode("not!valid@base64"); err == nil {
		t.Error("Base64Decode accepted invalid input")
	}
	// Invalid URL base64 (padding present in raw URL encoding is invalid)
	if _, err := Base64URLDecode("AAAA===="); err == nil {
		t.Error("Base64URLDecode accepted padded input (raw URL encoding rejects '=')")
	}
}

// -------------------------------------------------------------------
// Constant-time comparison
// -------------------------------------------------------------------

func TestSecureCompare(t *testing.T) {
	if !SecureCompare("abc", "abc") {
		t.Error("SecureCompare returned false for equal strings")
	}
	if SecureCompare("abc", "abd") {
		t.Error("SecureCompare returned true for different strings")
	}
	if SecureCompare("abc", "abcd") {
		t.Error("SecureCompare returned true for different-length strings")
	}
	if !SecureCompare("", "") {
		t.Error("SecureCompare returned false for empty strings")
	}
	if SecureCompare("", "x") {
		t.Error("SecureCompare returned true for empty vs non-empty")
	}
}

// -------------------------------------------------------------------
// Fingerprint
// -------------------------------------------------------------------

func TestGenerateFingerprint_StableAndHex(t *testing.T) {
	fp1 := GenerateFingerprint()
	fp2 := GenerateFingerprint()
	if fp1 != fp2 {
		t.Errorf("GenerateFingerprint not stable: %q vs %q", fp1, fp2)
	}
	if len(fp1) != 64 {
		t.Errorf("GenerateFingerprint length = %d, want 64", len(fp1))
	}
	if _, err := hex.DecodeString(fp1); err != nil {
		t.Errorf("GenerateFingerprint not hex: %v", err)
	}

	short := GenerateShortFingerprint()
	if len(short) != 16 {
		t.Errorf("GenerateShortFingerprint length = %d, want 16", len(short))
	}
	if !strings.HasPrefix(fp1, short) {
		t.Errorf("short fingerprint %q is not a prefix of full %q", short, fp1)
	}
}

func TestComputeMessageFingerprint(t *testing.T) {
	// Determinism
	a := ComputeMessageFingerprint("hello world this is a test message", "1.0.0")
	b := ComputeMessageFingerprint("hello world this is a test message", "1.0.0")
	if a != b {
		t.Errorf("ComputeMessageFingerprint not deterministic: %q vs %q", a, b)
	}
	// Length is always 3
	if len(a) != 3 {
		t.Errorf("ComputeMessageFingerprint length = %d, want 3", len(a))
	}
	// Short message (uses '0' padding) still produces 3 chars
	short := ComputeMessageFingerprint("hi", "1.0.0")
	if len(short) != 3 {
		t.Errorf("ComputeMessageFingerprint(short) length = %d, want 3", len(short))
	}
	// Different version → different fingerprint (extremely likely)
	v1 := ComputeMessageFingerprint("hello world this is a test message", "1.0.0")
	v2 := ComputeMessageFingerprint("hello world this is a test message", "2.0.0")
	if v1 == v2 {
		t.Error("ComputeMessageFingerprint produced same fp for different versions")
	}
}
