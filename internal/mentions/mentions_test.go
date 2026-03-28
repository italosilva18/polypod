package mentions

import "testing"

func TestMentionPattern(t *testing.T) {
	tests := []struct {
		input string
		want  int // number of matches
	}{
		{"revise @internal/router/router.go", 1},
		{"compare @go.mod com @go.sum", 2},
		{"sem mention aqui", 0},
		{"veja @src/", 1},
		{"@file.go e @other.py", 2},
	}

	for _, tt := range tests {
		matches := MentionPattern.FindAllString(tt.input, -1)
		if len(matches) != tt.want {
			t.Errorf("MentionPattern(%q) found %d matches, want %d", tt.input, len(matches), tt.want)
		}
	}
}

func TestFuzzyMatch(t *testing.T) {
	tests := []struct {
		query, target string
		want          bool
	}{
		{"rtr", "router", true},
		{"mgo", "main.go", true},
		{"xyz", "main.go", false},
		{"", "anything", true},
	}

	for _, tt := range tests {
		got := fuzzyMatch(tt.query, tt.target)
		if got != tt.want {
			t.Errorf("fuzzyMatch(%q, %q) = %v, want %v", tt.query, tt.target, got, tt.want)
		}
	}
}
