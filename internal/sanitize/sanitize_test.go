package sanitize

import (
	"errors"
	"strings"
	"testing"
)

func TestSanitizeUnicode(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"plain ASCII unchanged", "hello world", "hello world"},
		{"empty string", "", ""},
		{"strips zero-width space", "a\u200Bb", "ab"},
		{"strips LTR mark", "x\u200Ey", "xy"},
		{"strips RTL override", "foo\u202Ebar", "foobar"},
		{"strips directional isolate", "a\u2066b\u2069c", "abc"},
		{"strips BOM", "\uFEFFhello", "hello"},
		{"strips BMP private-use", "a\uE000b", "ab"},
		{"NFKC normalises compat form", "ﬁle", "file"}, // U+FB01 ligature -> "fi"
		{"multiple dangerous chars", "a\u200B\u200C\u200Db", "ab"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := SanitizeUnicode(tc.input)
			if got != tc.want {
				t.Errorf("SanitizeUnicode(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestSanitizeUserInput(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"plain text unchanged", "hello world", "hello world"},
		{"empty string", "", ""},
		{"preserves newline", "a\nb", "a\nb"},
		{"preserves tab", "a\tb", "a\tb"},
		{"preserves carriage return", "a\rb", "a\rb"},
		{"strips null byte", "a\x00b", "ab"},
		{"strips bell control", "a\x07b", "ab"},
		{"collapses multiple spaces", "a    b", "a b"},
		{"trims leading/trailing whitespace", "   hello   ", "hello"},
		{"strips unicode smuggling", "hi\u200Bthere", "hithere"},
		{"combined cleanup", "  \x00hello\u200B   world  ", "hello world"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := SanitizeUserInput(tc.input)
			if got != tc.want {
				t.Errorf("SanitizeUserInput(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestSanitizeFilePath(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"simple relative path", "foo/bar.txt", false},
		{"absolute path", "/etc/hosts", false},
		{"current dir reference", "./foo.txt", false},
		{"nested path", "a/b/c/d.txt", false},
		{"redundant slashes", "a//b///c.txt", false},
		{"path traversal rejected", "../etc/passwd", true},
		{"embedded traversal rejected", "foo/../../bar", true},
		{"parent dir prefix rejected", "../foo", true},
		{"empty path", "", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := SanitizeFilePath(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Errorf("SanitizeFilePath(%q) expected error, got nil (result=%q)", tc.input, got)
					return
				}
				var pte *PathTraversalError
				if !errors.As(err, &pte) {
					t.Errorf("SanitizeFilePath(%q) error type = %T, want *PathTraversalError", tc.input, err)
				}
				return
			}
			if err != nil {
				t.Errorf("SanitizeFilePath(%q) unexpected error: %v", tc.input, err)
			}
		})
	}
}

func TestSanitizeURL(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
		errKind string // "unsafe" if expected to be *UnsafeURLError
	}{
		{"http URL", "http://example.com/path", false, ""},
		{"https URL", "https://example.com/path?q=1", false, ""},
		{"trims whitespace", "  https://example.com  ", false, ""},
		{"javascript scheme rejected", "javascript:alert(1)", true, "unsafe"},
		{"data scheme rejected", "data:text/html,<script>", true, "unsafe"},
		{"vbscript scheme rejected", "vbscript:msgbox(1)", true, "unsafe"},
		{"missing host rejected", "http://", true, "unsafe"},
		{"file scheme allowed", "file:///tmp/foo", false, ""},
		{"relative path allowed (no scheme)", "/foo/bar", false, ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := SanitizeURL(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Errorf("SanitizeURL(%q) expected error, got nil (result=%q)", tc.input, got)
					return
				}
				if tc.errKind == "unsafe" {
					var ue *UnsafeURLError
					if !errors.As(err, &ue) {
						t.Errorf("SanitizeURL(%q) error type = %T, want *UnsafeURLError", tc.input, err)
					}
				}
				return
			}
			if err != nil {
				t.Errorf("SanitizeURL(%q) unexpected error: %v", tc.input, err)
			}
			if got == "" && tc.input != "" {
				t.Errorf("SanitizeURL(%q) returned empty result", tc.input)
			}
		})
	}
}

func TestSanitizeEnvVar(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"plain value unchanged", "production", "production"},
		{"empty string", "", ""},
		{"strips null byte", "val\x00ue", "value"},
		{"strips newline", "line1\nline2", "line1line2"},
		{"strips carriage return", "a\rb", "ab"},
		{"strips CRLF injection", "value\r\nInjected: evil", "valueInjected: evil"},
		{"trims whitespace", "   spaced   ", "spaced"},
		{"combined cleanup", "  foo\x00\nbar  ", "foobar"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := SanitizeEnvVar(tc.input)
			if got != tc.want {
				t.Errorf("SanitizeEnvVar(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestStripSecrets(t *testing.T) {
	tests := []struct {
		name           string
		input          string
		mustContain    []string
		mustNotContain []string
	}{
		{
			name:           "OpenAI-style key redacted",
			input:          "token: sk-abcdefghijklmnopqrstuvwxyz0123456789",
			mustContain:    []string{"[REDACTED]"},
			mustNotContain: []string{"sk-abcdefghijklmnopqrstuvwxyz0123456789"},
		},
		{
			name:           "GitHub PAT redacted",
			input:          "use ghp_abcdefghijklmnopqrstuvwxyz0123456789 please",
			mustContain:    []string{"[REDACTED]"},
			mustNotContain: []string{"ghp_abcdefghijklmnopqrstuvwxyz0123456789"},
		},
		{
			name:           "AWS access key redacted",
			input:          "key=AKIAIOSFODNN7EXAMPLE",
			mustContain:    []string{"[REDACTED]"},
			mustNotContain: []string{"AKIAIOSFODNN7EXAMPLE"},
		},
		{
			name:           "Bearer token redacted",
			input:          "Authorization: Bearer abc123XYZ.token_value-here",
			mustContain:    []string{"[REDACTED]"},
			mustNotContain: []string{"abc123XYZ.token_value-here"},
		},
		{
			name:           "password key=value redacted but key kept",
			input:          `password="supersecret123"`,
			mustContain:    []string{"password", "[REDACTED]"},
			mustNotContain: []string{"supersecret123"},
		},
		{
			name:           "Slack bot token redacted",
			input:          "slack: xoxb-1234567890-abcdef",
			mustContain:    []string{"[REDACTED]"},
			mustNotContain: []string{"xoxb-1234567890-abcdef"},
		},
		{
			name:           "clean text untouched",
			input:          "nothing sensitive in this log line",
			mustContain:    []string{"nothing sensitive in this log line"},
			mustNotContain: []string{"[REDACTED]"},
		},
		{
			name:           "empty string",
			input:          "",
			mustContain:    []string{},
			mustNotContain: []string{"[REDACTED]"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := StripSecrets(tc.input)
			for _, s := range tc.mustContain {
				if !strings.Contains(got, s) {
					t.Errorf("StripSecrets(%q) = %q, expected to contain %q", tc.input, got, s)
				}
			}
			for _, s := range tc.mustNotContain {
				if strings.Contains(got, s) {
					t.Errorf("StripSecrets(%q) = %q, expected NOT to contain %q", tc.input, got, s)
				}
			}
		})
	}
}

func TestPathTraversalError_Error(t *testing.T) {
	err := &PathTraversalError{Path: "../etc/passwd"}
	if !strings.Contains(err.Error(), "../etc/passwd") {
		t.Errorf("PathTraversalError.Error() = %q, want to contain path", err.Error())
	}
	if !strings.Contains(err.Error(), "path traversal") {
		t.Errorf("PathTraversalError.Error() = %q, want descriptive message", err.Error())
	}
}

func TestUnsafeURLError_Error(t *testing.T) {
	err := &UnsafeURLError{URL: "javascript:alert(1)", Reason: "disallowed scheme: javascript"}
	msg := err.Error()
	if !strings.Contains(msg, "javascript:alert(1)") {
		t.Errorf("UnsafeURLError.Error() = %q, want to contain URL", msg)
	}
	if !strings.Contains(msg, "disallowed scheme") {
		t.Errorf("UnsafeURLError.Error() = %q, want to contain reason", msg)
	}
}
