package awsutil

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
)

func TestIsValidAwsStsOutput(t *testing.T) {
	tests := []struct {
		name   string
		input  map[string]any
		expect bool
	}{
		{
			name:   "nil map",
			input:  nil,
			expect: false,
		},
		{
			name:   "empty map",
			input:  map[string]any{},
			expect: false,
		},
		{
			name: "missing Credentials key",
			input: map[string]any{
				"SomethingElse": "value",
			},
			expect: false,
		},
		{
			name: "Credentials not a map",
			input: map[string]any{
				"Credentials": "not-a-map",
			},
			expect: false,
		},
		{
			name: "valid credentials",
			input: map[string]any{
				"Credentials": map[string]any{
					"AccessKeyId":     "AKIAEXAMPLE",
					"SecretAccessKey": "secret123",
					"SessionToken":    "token456",
				},
			},
			expect: true,
		},
		{
			name: "valid credentials with expiration",
			input: map[string]any{
				"Credentials": map[string]any{
					"AccessKeyId":     "AKIAEXAMPLE",
					"SecretAccessKey": "secret123",
					"SessionToken":    "token456",
					"Expiration":      "2025-01-01T00:00:00Z",
				},
			},
			expect: true,
		},
		{
			name: "missing AccessKeyId",
			input: map[string]any{
				"Credentials": map[string]any{
					"SecretAccessKey": "secret123",
					"SessionToken":    "token456",
				},
			},
			expect: false,
		},
		{
			name: "empty AccessKeyId",
			input: map[string]any{
				"Credentials": map[string]any{
					"AccessKeyId":     "",
					"SecretAccessKey": "secret123",
					"SessionToken":    "token456",
				},
			},
			expect: false,
		},
		{
			name: "empty SecretAccessKey",
			input: map[string]any{
				"Credentials": map[string]any{
					"AccessKeyId":     "AKIAEXAMPLE",
					"SecretAccessKey": "",
					"SessionToken":    "token456",
				},
			},
			expect: false,
		},
		{
			name: "empty SessionToken",
			input: map[string]any{
				"Credentials": map[string]any{
					"AccessKeyId":     "AKIAEXAMPLE",
					"SecretAccessKey": "secret123",
					"SessionToken":    "",
				},
			},
			expect: false,
		},
		{
			name: "non-string AccessKeyId",
			input: map[string]any{
				"Credentials": map[string]any{
					"AccessKeyId":     12345,
					"SecretAccessKey": "secret123",
					"SessionToken":    "token456",
				},
			},
			expect: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsValidAwsStsOutput(tt.input)
			if got != tt.expect {
				t.Errorf("IsValidAwsStsOutput() = %v, want %v", got, tt.expect)
			}
		})
	}
}

func TestCredentialsProviderError_Error(t *testing.T) {
	err := &CredentialsProviderError{Msg: "something bad happened"}
	got := err.Error()
	want := "CredentialsProviderError: something bad happened"
	if got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}

	// Must satisfy the error interface.
	var _ error = err
}

func TestIsAwsCredentialsProviderError(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		expect bool
	}{
		{
			name:   "nil error",
			err:    nil,
			expect: false,
		},
		{
			name:   "CredentialsProviderError type",
			err:    &CredentialsProviderError{Msg: "chain exhausted"},
			expect: true,
		},
		{
			name:   "wrapped CredentialsProviderError",
			err:    fmt.Errorf("wrap: %w", &CredentialsProviderError{Msg: "bad"}),
			expect: true,
		},
		{
			name:   "error message contains CredentialsProviderError",
			err:    errors.New("aws sdk: CredentialsProviderError occurred"),
			expect: true,
		},
		{
			name:   "no EC2 IMDS role found",
			err:    errors.New("no EC2 IMDS role found in credential chain"),
			expect: true,
		},
		{
			name:   "failed to refresh cached credentials",
			err:    errors.New("operation error: failed to refresh cached credentials"),
			expect: true,
		},
		{
			name:   "unrelated error",
			err:    errors.New("network timeout"),
			expect: false,
		},
		{
			name:   "different sdk error",
			err:    errors.New("AccessDeniedException: not allowed"),
			expect: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsAwsCredentialsProviderError(tt.err)
			if got != tt.expect {
				t.Errorf("IsAwsCredentialsProviderError(%v) = %v, want %v", tt.err, got, tt.expect)
			}
		})
	}
}

func TestClearAwsIniCache(t *testing.T) {
	// Must not panic and must be callable repeatedly.
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("ClearAwsIniCache() panicked: %v", r)
		}
	}()
	ClearAwsIniCache()
	ClearAwsIniCache()
	ClearAwsIniCache()
}

func TestGetAwsRegion(t *testing.T) {
	tests := []struct {
		name      string
		awsRegion string
		awsDef    string
		// setEnv controls whether the variable should be explicitly set (true)
		// or explicitly unset (false). This distinguishes "" from unset.
		setRegion bool
		setDef    bool
		expect    string
	}{
		{
			name:      "AWS_REGION takes precedence",
			awsRegion: "eu-west-1",
			awsDef:    "us-west-2",
			setRegion: true,
			setDef:    true,
			expect:    "eu-west-1",
		},
		{
			name:      "fallback to AWS_DEFAULT_REGION when AWS_REGION unset",
			awsRegion: "",
			awsDef:    "ap-southeast-2",
			setRegion: false,
			setDef:    true,
			expect:    "ap-southeast-2",
		},
		{
			name:      "fallback to us-east-1 when both unset",
			awsRegion: "",
			awsDef:    "",
			setRegion: false,
			setDef:    false,
			expect:    "us-east-1",
		},
		{
			name:      "empty AWS_REGION skipped, use AWS_DEFAULT_REGION",
			awsRegion: "",
			awsDef:    "eu-central-1",
			setRegion: true,
			setDef:    true,
			expect:    "eu-central-1",
		},
		{
			name:      "AWS_REGION only",
			awsRegion: "sa-east-1",
			awsDef:    "",
			setRegion: true,
			setDef:    false,
			expect:    "sa-east-1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.setRegion {
				t.Setenv("AWS_REGION", tt.awsRegion)
			} else {
				// Ensure unset within this test via Setenv+Unsetenv trick:
				// t.Setenv("X","") still sets it. Use explicit unset.
				t.Setenv("AWS_REGION", "placeholder")
				if err := unsetEnv("AWS_REGION"); err != nil {
					t.Fatalf("unset AWS_REGION: %v", err)
				}
			}
			if tt.setDef {
				t.Setenv("AWS_DEFAULT_REGION", tt.awsDef)
			} else {
				t.Setenv("AWS_DEFAULT_REGION", "placeholder")
				if err := unsetEnv("AWS_DEFAULT_REGION"); err != nil {
					t.Fatalf("unset AWS_DEFAULT_REGION: %v", err)
				}
			}

			got := GetAwsRegion()
			if got != tt.expect {
				t.Errorf("GetAwsRegion() = %q, want %q", got, tt.expect)
			}
		})
	}
}

func TestGetBedrockEndpoint(t *testing.T) {
	tests := []struct {
		name   string
		model  string
		region string
		// envRegion is set (if non-empty) so GetAwsRegion() returns a known
		// value when region == "".
		envRegion string
		expect    string
	}{
		{
			name:   "explicit region and model",
			model:  "anthropic.claude-3-sonnet-20240229-v1:0",
			region: "us-east-1",
			expect: "https://bedrock-runtime.us-east-1.amazonaws.com/model/anthropic.claude-3-sonnet-20240229-v1:0/invoke",
		},
		{
			name:   "different region",
			model:  "anthropic.claude-3-haiku-20240307-v1:0",
			region: "eu-west-1",
			expect: "https://bedrock-runtime.eu-west-1.amazonaws.com/model/anthropic.claude-3-haiku-20240307-v1:0/invoke",
		},
		{
			name:      "empty region falls back to env",
			model:     "anthropic.claude-3-opus-20240229-v1:0",
			region:    "",
			envRegion: "ap-northeast-1",
			expect:    "https://bedrock-runtime.ap-northeast-1.amazonaws.com/model/anthropic.claude-3-opus-20240229-v1:0/invoke",
		},
		{
			name:      "empty region, no env, uses us-east-1 default",
			model:     "foo.model",
			region:    "",
			envRegion: "",
			expect:    "https://bedrock-runtime.us-east-1.amazonaws.com/model/foo.model/invoke",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.envRegion != "" {
				t.Setenv("AWS_REGION", tt.envRegion)
			} else {
				t.Setenv("AWS_REGION", "placeholder")
				if err := unsetEnv("AWS_REGION"); err != nil {
					t.Fatalf("unset AWS_REGION: %v", err)
				}
			}
			t.Setenv("AWS_DEFAULT_REGION", "placeholder")
			if err := unsetEnv("AWS_DEFAULT_REGION"); err != nil {
				t.Fatalf("unset AWS_DEFAULT_REGION: %v", err)
			}

			got := GetBedrockEndpoint(tt.model, tt.region)
			if got != tt.expect {
				t.Errorf("GetBedrockEndpoint(%q, %q) = %q, want %q",
					tt.model, tt.region, got, tt.expect)
			}

			// Sanity: URL structure checks.
			if !strings.HasPrefix(got, "https://bedrock-runtime.") {
				t.Errorf("endpoint missing https bedrock-runtime prefix: %q", got)
			}
			if !strings.HasSuffix(got, "/invoke") {
				t.Errorf("endpoint missing /invoke suffix: %q", got)
			}
			if !strings.Contains(got, "/model/"+tt.model+"/") {
				t.Errorf("endpoint missing model path segment for %q: %q", tt.model, got)
			}
		})
	}
}

func TestAwsCredentialsStruct(t *testing.T) {
	c := AwsCredentials{
		AccessKeyId:     "AKIA",
		SecretAccessKey: "secret",
		SessionToken:    "token",
		Expiration:      "2030-01-01T00:00:00Z",
	}
	if c.AccessKeyId != "AKIA" || c.SecretAccessKey != "secret" ||
		c.SessionToken != "token" || c.Expiration != "2030-01-01T00:00:00Z" {
		t.Errorf("struct fields not preserved: %+v", c)
	}

	out := AwsStsOutput{Credentials: c}
	if out.Credentials.AccessKeyId != "AKIA" {
		t.Errorf("AwsStsOutput.Credentials not preserved: %+v", out)
	}
}

// unsetEnv is a small helper because testing.T.Setenv does not offer an
// "unset" form; it always sets to the provided value. We need to explicitly
// unset after t.Setenv to restore the unset state for the test-case body
// (t.Setenv still guarantees the original value is restored on cleanup).
func unsetEnv(key string) error {
	return os.Unsetenv(key)
}
