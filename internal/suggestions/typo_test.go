package suggestions

import (
	"strings"
	"testing"
)

func TestSuggest_ExactMatch(t *testing.T) {
	candidates := []string{"help", "status", "commit"}
	results := Suggest("help", candidates, DefaultOptions())

	if len(results) == 0 {
		t.Fatal("no results for exact match")
	}
	if results[0].Value != "help" {
		t.Errorf("top = %q; want help", results[0].Value)
	}
	if results[0].Similarity < 0.99 {
		t.Errorf("similarity = %v; want ~1.0", results[0].Similarity)
	}
	if results[0].Distance != 0 {
		t.Errorf("distance = %d; want 0", results[0].Distance)
	}
}

func TestSuggest_Typo(t *testing.T) {
	candidates := []string{"help", "status", "commit"}
	results := Suggest("helpp", candidates, DefaultOptions())

	if len(results) == 0 {
		t.Fatal("no suggestion for typo")
	}
	if results[0].Value != "help" {
		t.Errorf("top = %q; want help", results[0].Value)
	}
	if results[0].Similarity <= 0.6 {
		t.Errorf("similarity = %v; want > 0.6", results[0].Similarity)
	}
}

func TestSuggest_VeryDifferent(t *testing.T) {
	candidates := []string{"help", "status"}
	results := Suggest("xyz", candidates, DefaultOptions())

	if len(results) > 0 {
		t.Errorf("got %d results for unrelated; want 0", len(results))
	}
}

func TestSuggest_EmptyInput(t *testing.T) {
	candidates := []string{"help"}
	results := Suggest("", candidates, DefaultOptions())
	if len(results) != 0 {
		t.Errorf("empty input should return empty; got %d", len(results))
	}
}

func TestSuggest_EmptyCandidates(t *testing.T) {
	results := Suggest("help", nil, DefaultOptions())
	if len(results) != 0 {
		t.Errorf("empty candidates should return empty; got %d", len(results))
	}

	results = Suggest("help", []string{}, DefaultOptions())
	if len(results) != 0 {
		t.Errorf("empty candidates slice should return empty; got %d", len(results))
	}
}

func TestSuggest_MaxResults(t *testing.T) {
	candidates := []string{"foo1", "foo2", "foo3", "foo4", "foo5"}
	opts := DefaultOptions()
	opts.MaxResults = 2

	results := Suggest("foo", candidates, opts)
	if len(results) > 2 {
		t.Errorf("results = %d; want <= 2", len(results))
	}
}

func TestSuggest_CaseInsensitiveByDefault(t *testing.T) {
	candidates := []string{"Help", "STATUS"}
	results := Suggest("help", candidates, DefaultOptions())

	if len(results) == 0 {
		t.Fatal("expected case-insensitive match")
	}
	if results[0].Value != "Help" {
		t.Errorf("top = %q; want Help (original case preserved)", results[0].Value)
	}
	if results[0].Similarity < 0.99 {
		t.Errorf("similarity = %v; want ~1.0 for case-insensitive exact", results[0].Similarity)
	}
}

func TestSuggestTop_SingleBest(t *testing.T) {
	candidates := []string{"help", "status"}
	top := SuggestTop("hel", candidates)
	if top != "help" {
		t.Errorf("top = %q; want help", top)
	}
}

func TestSuggestTop_NoQualifyingMatch(t *testing.T) {
	candidates := []string{"help", "status"}
	if got := SuggestTop("totally different xyz", candidates); got != "" {
		t.Errorf("unrelated should return empty; got %q", got)
	}
}

func TestDidYouMean_FormattedMessage(t *testing.T) {
	candidates := []string{"help", "status"}
	msg := DidYouMean("helpp", candidates)
	if msg == "" {
		t.Fatal("expected a suggestion")
	}
	if !strings.Contains(msg, "help") {
		t.Errorf("message = %q; want contains help", msg)
	}
	if !strings.Contains(msg, "Did you mean") {
		t.Errorf("message = %q; want contains 'Did you mean'", msg)
	}
}

func TestDidYouMean_EmptyWhenNoMatch(t *testing.T) {
	candidates := []string{"help", "status"}
	if got := DidYouMean("xyz", candidates); got != "" {
		t.Errorf("unrelated should return empty; got %q", got)
	}
}

func TestDidYouMeanMulti_Single(t *testing.T) {
	candidates := []string{"help", "xyz", "abc"}
	msg := DidYouMeanMulti("helpp", candidates, 3)
	if msg == "" {
		t.Fatal("expected a suggestion")
	}
	if !strings.Contains(msg, "Did you mean help?") {
		t.Errorf("message = %q; want 'Did you mean help?'", msg)
	}
	if strings.Contains(msg, "one of") {
		t.Errorf("single suggestion should not use 'one of'; got %q", msg)
	}
}

func TestDidYouMeanMulti_Multiple(t *testing.T) {
	candidates := []string{"foo1", "foo2", "foo3", "bar"}
	msg := DidYouMeanMulti("foo", candidates, 3)
	if msg == "" {
		t.Fatal("expected suggestions")
	}
	if !strings.Contains(msg, "one of") {
		t.Errorf("multiple suggestions should use 'one of'; got %q", msg)
	}
	if !strings.Contains(msg, ",") {
		t.Errorf("message should contain separator; got %q", msg)
	}
}

func TestDidYouMeanMulti_None(t *testing.T) {
	candidates := []string{"help", "status"}
	if got := DidYouMeanMulti("xyz", candidates, 3); got != "" {
		t.Errorf("no match should return empty; got %q", got)
	}
}

func TestCorrectCase_ExactMatch(t *testing.T) {
	candidates := []string{"MyCommand", "OtherCmd"}
	if got := CorrectCase("MyCommand", candidates); got != "MyCommand" {
		t.Errorf("exact match = %q; want MyCommand", got)
	}
}

func TestCorrectCase_CaseInsensitive(t *testing.T) {
	candidates := []string{"MyCommand", "OtherCmd"}
	if got := CorrectCase("mycommand", candidates); got != "MyCommand" {
		t.Errorf("CorrectCase = %q; want MyCommand", got)
	}
	if got := CorrectCase("OTHERCMD", candidates); got != "OtherCmd" {
		t.Errorf("CorrectCase = %q; want OtherCmd", got)
	}
}

func TestCorrectCase_NoMatch(t *testing.T) {
	candidates := []string{"MyCommand", "OtherCmd"}
	if got := CorrectCase("unknown", candidates); got != "unknown" {
		t.Errorf("no match should return input; got %q", got)
	}
}

func TestNormalizeToCamel(t *testing.T) {
	got := NormalizeToCamel("my_command")
	if got == "" {
		t.Error("NormalizeToCamel returned empty")
	}
	// Should not contain underscores
	if strings.Contains(got, "_") {
		t.Errorf("NormalizeToCamel = %q; should not contain underscore", got)
	}
}

func TestNormalizeToSnake(t *testing.T) {
	got := NormalizeToSnake("myCommand")
	if got != "my_command" {
		t.Errorf("NormalizeToSnake = %q; want my_command", got)
	}
}

func TestNormalizeToKebab(t *testing.T) {
	got := NormalizeToKebab("myCommand")
	if got != "my-command" {
		t.Errorf("NormalizeToKebab = %q; want my-command", got)
	}
}
