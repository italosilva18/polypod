// Package awsutil provides AWS-related helpers mirroring the TypeScript aws.ts
// source file. It does not import the AWS SDK (no Go AWS SDK dependency in
// go.mod); instead it implements the same validation / utility logic in pure Go.
package awsutil

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

// AwsCredentials represents short-term AWS credentials.
type AwsCredentials struct {
	AccessKeyId     string
	SecretAccessKey string
	SessionToken    string
	Expiration      string // optional
}

// AwsStsOutput is the output structure of `aws sts get-session-token` or
// `aws sts assume-role`.
type AwsStsOutput struct {
	Credentials AwsCredentials
}

// CredentialsProviderError is the sentinel error type returned when the AWS
// credential provider chain is exhausted or misconfigured.
type CredentialsProviderError struct {
	Msg string
}

func (e *CredentialsProviderError) Error() string {
	return "CredentialsProviderError: " + e.Msg
}

// IsValidAwsStsOutput validates an STS assume-role / get-session-token output
// map (as decoded from JSON). Mirrors isValidAwsStsOutput() in aws.ts.
//
// The map must have a "Credentials" key whose value is itself a map with
// non-empty string fields AccessKeyId, SecretAccessKey, and SessionToken.
func IsValidAwsStsOutput(output map[string]any) bool {
	if output == nil {
		return false
	}

	credsRaw, ok := output["Credentials"]
	if !ok {
		return false
	}

	creds, ok := credsRaw.(map[string]any)
	if !ok {
		return false
	}

	accessKeyId, _ := creds["AccessKeyId"].(string)
	secretAccessKey, _ := creds["SecretAccessKey"].(string)
	sessionToken, _ := creds["SessionToken"].(string)

	return accessKeyId != "" && secretAccessKey != "" && sessionToken != ""
}

// IsAwsCredentialsProviderError reports whether err (or any error in its chain)
// is a CredentialsProviderError or carries the "CredentialsProviderError" name
// in its message. Mirrors isAwsCredentialsProviderError() in aws.ts.
func IsAwsCredentialsProviderError(err error) bool {
	if err == nil {
		return false
	}

	var cpe *CredentialsProviderError
	if errors.As(err, &cpe) {
		return true
	}

	// Fallback: check the error message string for the canonical name.
	// This handles errors originating from the AWS SDK for Go whose type is
	// not directly accessible as CredentialsProviderError.
	msg := err.Error()
	return strings.Contains(msg, "CredentialsProviderError") ||
		strings.HasPrefix(msg, "no EC2 IMDS role found") ||
		strings.Contains(msg, "failed to refresh cached credentials")
}

// ClearAwsIniCache forces a refresh of the AWS shared-credentials file cache.
// In the TypeScript source this calls fromIni({ ignoreCache: true }) against
// the AWS SDK's in-process file cache. In Go there is no equivalent persistent
// in-process cache, so this function is a no-op that successfully signals
// "cache cleared" to the caller. Any subsequent call to the AWS SDK will
// naturally re-read ~/.aws/credentials from disk.
func ClearAwsIniCache() {
	// No-op: the Go AWS SDK (v2) reads credentials from disk on each SDK call
	// (or on each credential refresh via the DefaultAWSConfig chain), so there
	// is no persistent in-memory ini-file cache that needs to be invalidated.
	// This function exists to satisfy the API contract expected by callers.
}

// GetAwsRegion returns the effective AWS region, preferring AWS_REGION, then
// AWS_DEFAULT_REGION, and finally falling back to "us-east-1". Mirrors
// getAWSRegion() from envUtils.ts.
func GetAwsRegion() string {
	if r := os.Getenv("AWS_REGION"); r != "" {
		return r
	}
	if r := os.Getenv("AWS_DEFAULT_REGION"); r != "" {
		return r
	}
	return "us-east-1"
}

// GetBedrockEndpoint constructs the Bedrock runtime endpoint URL for the given
// model and region. The resulting URL follows the format used by the Anthropic
// Bedrock SDK.
//
//	https://bedrock-runtime.<region>.amazonaws.com/model/<modelId>/invoke
func GetBedrockEndpoint(model, region string) string {
	if region == "" {
		region = GetAwsRegion()
	}
	return fmt.Sprintf("https://bedrock-runtime.%s.amazonaws.com/model/%s/invoke", region, model)
}
