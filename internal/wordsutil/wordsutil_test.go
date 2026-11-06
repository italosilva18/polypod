package wordsutil

import "testing"

func TestWords(t *testing.T) {
	tests := []struct {
		input string
		want  []string
	}{
		{"helloWorld", []string{"hello", "World"}},
		{"HelloWorld", []string{"Hello", "World"}},
		{"hello_world", []string{"hello", "world"}},
		{"hello-world", []string{"hello", "world"}},
		{"hello.world", []string{"hello", "world"}},
		{"hello world", []string{"hello", "world"}},
		{"HTTPServer", []string{"HTTP", "Server"}},
		{"XMLHttpRequest", []string{"XML", "Http", "Request"}},
		{"", nil},
		{"single", []string{"single"}},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := Words(tt.input)
			if !equalSlice(got, tt.want) {
				t.Errorf("Words(%q) = %v; want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestToCamel(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"hello world", "helloWorld"},
		{"hello_world", "helloWorld"},
		{"hello-world", "helloWorld"},
		{"HelloWorld", "helloWorld"},
		{"HTTPServer", "httpServer"},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			if got := ToCamel(tt.input); got != tt.want {
				t.Errorf("ToCamel(%q) = %q; want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestToPascal(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"hello world", "HelloWorld"},
		{"hello_world", "HelloWorld"},
		{"helloWorld", "HelloWorld"},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			if got := ToPascal(tt.input); got != tt.want {
				t.Errorf("ToPascal(%q) = %q; want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestToSnake(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"helloWorld", "hello_world"},
		{"HelloWorld", "hello_world"},
		{"hello-world", "hello_world"},
		{"hello world", "hello_world"},
		{"HTTPServer", "http_server"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			if got := ToSnake(tt.input); got != tt.want {
				t.Errorf("ToSnake(%q) = %q; want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestToKebab(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"helloWorld", "hello-world"},
		{"HelloWorld", "hello-world"},
		{"hello_world", "hello-world"},
		{"hello world", "hello-world"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			if got := ToKebab(tt.input); got != tt.want {
				t.Errorf("ToKebab(%q) = %q; want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestLevenshtein(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"", "", 0},
		{"abc", "abc", 0},
		{"abc", "abd", 1},
		{"kitten", "sitting", 3},
		{"", "abc", 3},
		{"abc", "", 3},
		{"hello", "olleh", 4},
	}

	for _, tt := range tests {
		t.Run(tt.a+"_"+tt.b, func(t *testing.T) {
			if got := Levenshtein(tt.a, tt.b); got != tt.want {
				t.Errorf("Levenshtein(%q, %q) = %d; want %d", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

func TestJaroWinkler(t *testing.T) {
	// JW is between 0 and 1; identical strings should return 1
	if got := JaroWinkler("identical", "identical"); got < 0.999 {
		t.Errorf("JaroWinkler identical = %f; want 1.0", got)
	}
	// Similar strings should return > 0.5
	if got := JaroWinkler("martha", "marhta"); got < 0.5 {
		t.Errorf("JaroWinkler similar = %f; want > 0.5", got)
	}
	// Empty strings
	if got := JaroWinkler("", ""); got < 0.999 {
		t.Errorf("JaroWinkler empty = %f; want 1.0", got)
	}
}

func TestSimilarity(t *testing.T) {
	// Identical
	if got := Similarity("hello", "hello"); got < 0.999 {
		t.Errorf("Similarity identical = %f; want 1.0", got)
	}
	// Totally different
	got := Similarity("abc", "xyz")
	if got > 0.5 {
		t.Errorf("Similarity different = %f; want < 0.5", got)
	}
}

func TestCapitalize(t *testing.T) {
	tests := []struct {
		input, want string
	}{
		{"hello", "Hello"},
		{"HELLO", "HELLO"},
		{"", ""},
		{"a", "A"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			if got := Capitalize(tt.input); got != tt.want {
				t.Errorf("Capitalize(%q) = %q; want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestPluralize(t *testing.T) {
	tests := []struct {
		word  string
		count int
		want  string
	}{
		{"cat", 1, "cat"},
		{"cat", 2, "cats"},
		{"box", 2, "boxes"},
		{"cry", 2, "cries"},
		{"boy", 2, "boys"}, // y after vowel = just add s
		{"bus", 2, "buses"},
		{"wish", 2, "wishes"},
		{"man", 2, "men"},
		{"woman", 2, "women"},
		{"child", 2, "children"},
	}
	for _, tt := range tests {
		t.Run(tt.word, func(t *testing.T) {
			if got := Pluralize(tt.word, tt.count); got != tt.want {
				t.Errorf("Pluralize(%q, %d) = %q; want %q", tt.word, tt.count, got, tt.want)
			}
		})
	}
}

func TestTruncate(t *testing.T) {
	tests := []struct {
		input    string
		maxLen   int
		ellipsis string
		want     string
	}{
		{"hello world", 20, "...", "hello world"},
		{"hello world", 8, "...", "hello..."},
		{"hello", 3, "", "hel"},
		{"", 5, "...", ""},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			if got := Truncate(tt.input, tt.maxLen, tt.ellipsis); got != tt.want {
				t.Errorf("Truncate(%q, %d, %q) = %q; want %q",
					tt.input, tt.maxLen, tt.ellipsis, got, tt.want)
			}
		})
	}
}

func equalSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i, v := range a {
		if v != b[i] {
			return false
		}
	}
	return true
}
