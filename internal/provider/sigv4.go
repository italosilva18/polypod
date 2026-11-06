package provider

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// sigV4Credentials holds the AWS credentials used for SigV4 signing.
type sigV4Credentials struct {
	AccessKeyID     string
	SecretAccessKey string
	SessionToken    string
	Region          string
	Service         string
}

// signRequest signs an *http.Request with AWS Signature Version 4.
// The request body must be seekable (e.g. bytes.Reader) so it can be read for hashing
// and then rewound for the actual HTTP call.
// See: https://docs.aws.amazon.com/general/latest/gr/sigv4_signing.html
func signRequest(req *http.Request, creds sigV4Credentials, payloadHash string) error {
	now := time.Now().UTC()
	dateStamp := now.Format("20060102")
	amzDate := now.Format("20060102T150405Z")

	// Step 0: Set required headers.
	req.Header.Set("X-Amz-Date", amzDate)
	req.Header.Set("Host", req.Host)
	if creds.SessionToken != "" {
		req.Header.Set("X-Amz-Security-Token", creds.SessionToken)
	}

	// Step 1: Create canonical request.
	canonicalURI := canonicalizePath(req.URL.Path)
	canonicalQueryString := canonicalizeQueryString(req.URL.Query())
	signedHeaders, canonicalHeaders := canonicalizeHeaders(req.Header, req.Host)

	canonicalRequest := strings.Join([]string{
		req.Method,
		canonicalURI,
		canonicalQueryString,
		canonicalHeaders,
		signedHeaders,
		payloadHash,
	}, "\n")

	// Step 2: Create string to sign.
	credentialScope := fmt.Sprintf("%s/%s/%s/aws4_request", dateStamp, creds.Region, creds.Service)
	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256",
		amzDate,
		credentialScope,
		hashSHA256([]byte(canonicalRequest)),
	}, "\n")

	// Step 3: Calculate signature.
	signingKey := deriveSigningKey(creds.SecretAccessKey, dateStamp, creds.Region, creds.Service)
	signature := hex.EncodeToString(hmacSHA256(signingKey, []byte(stringToSign)))

	// Step 4: Add Authorization header.
	authHeader := fmt.Sprintf(
		"AWS4-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		creds.AccessKeyID, credentialScope, signedHeaders, signature,
	)
	req.Header.Set("Authorization", authHeader)

	return nil
}

// hashPayload computes the SHA-256 hex digest of a request body.
// If the body is nil, returns the hash of an empty string.
func hashPayload(body io.ReadSeeker) (string, error) {
	if body == nil {
		return hashSHA256(nil), nil
	}
	h := sha256.New()
	if _, err := io.Copy(h, body); err != nil {
		return "", fmt.Errorf("hashing payload: %w", err)
	}
	// Rewind body so it can be read again by the HTTP client.
	if _, err := body.Seek(0, io.SeekStart); err != nil {
		return "", fmt.Errorf("rewinding payload: %w", err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// deriveSigningKey produces the SigV4 signing key via a chain of HMAC-SHA256 operations.
//
//	kDate    = HMAC("AWS4" + secret, dateStamp)
//	kRegion  = HMAC(kDate, region)
//	kService = HMAC(kRegion, service)
//	kSigning = HMAC(kService, "aws4_request")
func deriveSigningKey(secret, dateStamp, region, service string) []byte {
	kDate := hmacSHA256([]byte("AWS4"+secret), []byte(dateStamp))
	kRegion := hmacSHA256(kDate, []byte(region))
	kService := hmacSHA256(kRegion, []byte(service))
	kSigning := hmacSHA256(kService, []byte("aws4_request"))
	return kSigning
}

// hmacSHA256 computes HMAC-SHA256 of data using key.
func hmacSHA256(key, data []byte) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write(data)
	return mac.Sum(nil)
}

// hashSHA256 returns the lowercase hex-encoded SHA-256 digest.
func hashSHA256(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

// canonicalizePath returns the URI-encoded path component.
// Empty paths become "/".
func canonicalizePath(path string) string {
	if path == "" {
		return "/"
	}
	// The path may already be escaped from url.URL.Path (which is unescaped).
	// We need to re-encode each segment individually per the SigV4 spec.
	segments := strings.Split(path, "/")
	for i, seg := range segments {
		segments[i] = uriEncode(seg, false)
	}
	return strings.Join(segments, "/")
}

// canonicalizeQueryString produces the canonical query string:
// parameters sorted by key name, then by value, URI-encoded.
func canonicalizeQueryString(values url.Values) string {
	if len(values) == 0 {
		return ""
	}
	type kv struct {
		key, val string
	}
	var pairs []kv
	for k, vs := range values {
		for _, v := range vs {
			pairs = append(pairs, kv{uriEncode(k, true), uriEncode(v, true)})
		}
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].key == pairs[j].key {
			return pairs[i].val < pairs[j].val
		}
		return pairs[i].key < pairs[j].key
	})
	parts := make([]string, len(pairs))
	for i, p := range pairs {
		parts[i] = p.key + "=" + p.val
	}
	return strings.Join(parts, "&")
}

// canonicalizeHeaders builds the canonical header string and the signed-headers list.
// Only headers that participate in signing are included. Per the SigV4 spec, at minimum
// the "host" header must be included. We include all headers starting with "x-amz-"
// plus "content-type" and "host".
func canonicalizeHeaders(headers http.Header, host string) (signedHeaders, canonicalHeaders string) {
	// Collect headers to sign.
	type hdr struct {
		key, val string
	}
	var hdrs []hdr

	// Always include host.
	hdrs = append(hdrs, hdr{key: "host", val: strings.TrimSpace(host)})

	for k, vs := range headers {
		lower := strings.ToLower(k)
		if lower == "host" {
			continue // already added
		}
		if lower == "content-type" || strings.HasPrefix(lower, "x-amz-") {
			// Combine multi-value headers with commas, trim whitespace.
			vals := make([]string, len(vs))
			for i, v := range vs {
				vals[i] = strings.TrimSpace(v)
			}
			hdrs = append(hdrs, hdr{key: lower, val: strings.Join(vals, ",")})
		}
	}

	sort.Slice(hdrs, func(i, j int) bool {
		return hdrs[i].key < hdrs[j].key
	})

	var headerLines []string
	var headerNames []string
	for _, h := range hdrs {
		headerLines = append(headerLines, h.key+":"+h.val)
		headerNames = append(headerNames, h.key)
	}

	// Canonical headers end with a trailing newline.
	canonicalHeaders = strings.Join(headerLines, "\n") + "\n"
	signedHeaders = strings.Join(headerNames, ";")
	return signedHeaders, canonicalHeaders
}

// uriEncode percent-encodes a string per the AWS SigV4 spec.
// If encodeSlash is true, '/' is also encoded.
func uriEncode(s string, encodeSlash bool) string {
	var buf strings.Builder
	for _, b := range []byte(s) {
		if isUnreserved(b) {
			buf.WriteByte(b)
		} else if b == '/' && !encodeSlash {
			buf.WriteByte(b)
		} else {
			fmt.Fprintf(&buf, "%%%02X", b)
		}
	}
	return buf.String()
}

// isUnreserved returns true if the byte is an unreserved character per RFC 3986.
func isUnreserved(c byte) bool {
	return (c >= 'A' && c <= 'Z') ||
		(c >= 'a' && c <= 'z') ||
		(c >= '0' && c <= '9') ||
		c == '-' || c == '_' || c == '.' || c == '~'
}
