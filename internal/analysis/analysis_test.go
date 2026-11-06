package analysis

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeFixture writes content to a .go file inside dir and returns its path.
func writeFixture(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write fixture %s: %v", name, err)
	}
	return path
}

// findFunc looks up a FuncComplexity result by function name.
func findFunc(results []FuncComplexity, name string) (FuncComplexity, bool) {
	for _, r := range results {
		if r.Func == name {
			return r, true
		}
	}
	return FuncComplexity{}, false
}

// --- Tests --------------------------------------------------------------

func TestCyclomatic_SimpleFunction(t *testing.T) {
	dir := t.TempDir()
	src := `package fixture

func Add(a, b int) int {
	return a + b
}
`
	path := writeFixture(t, dir, "simple.go", src)

	results, err := Cyclomatic(path)
	if err != nil {
		t.Fatalf("Cyclomatic returned error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 function, got %d", len(results))
	}
	got := results[0]
	if got.Func != "Add" {
		t.Errorf("expected func Add, got %q", got.Func)
	}
	if got.Score != 1 {
		t.Errorf("expected base score 1 for trivial function, got %d", got.Score)
	}
	if got.Receiver != "" {
		t.Errorf("expected empty receiver, got %q", got.Receiver)
	}
	if got.Line == 0 {
		t.Errorf("expected non-zero line number")
	}
}

func TestCyclomatic_IfElseChain(t *testing.T) {
	dir := t.TempDir()
	src := `package fixture

func Classify(n int) string {
	if n < 0 {
		return "neg"
	}
	if n == 0 {
		return "zero"
	}
	if n < 10 {
		return "small"
	}
	return "big"
}
`
	path := writeFixture(t, dir, "ifs.go", src)
	results, err := Cyclomatic(path)
	if err != nil {
		t.Fatalf("Cyclomatic err: %v", err)
	}
	r, ok := findFunc(results, "Classify")
	if !ok {
		t.Fatal("Classify not found")
	}
	// base 1 + 3 if statements = 4
	if r.Score != 4 {
		t.Errorf("expected score 4, got %d", r.Score)
	}
}

func TestCyclomatic_LoopsAndRange(t *testing.T) {
	dir := t.TempDir()
	src := `package fixture

func Sum(items []int) int {
	total := 0
	for i := 0; i < 10; i++ {
		total += i
	}
	for _, v := range items {
		total += v
	}
	return total
}
`
	path := writeFixture(t, dir, "loops.go", src)
	results, err := Cyclomatic(path)
	if err != nil {
		t.Fatalf("Cyclomatic err: %v", err)
	}
	r, ok := findFunc(results, "Sum")
	if !ok {
		t.Fatal("Sum not found")
	}
	// base 1 + for + range = 3
	if r.Score != 3 {
		t.Errorf("expected score 3, got %d", r.Score)
	}
}

func TestCyclomatic_SwitchCases(t *testing.T) {
	dir := t.TempDir()
	src := `package fixture

func Day(n int) string {
	switch n {
	case 1:
		return "mon"
	case 2:
		return "tue"
	case 3:
		return "wed"
	default:
		return "other"
	}
}
`
	path := writeFixture(t, dir, "switch.go", src)
	results, err := Cyclomatic(path)
	if err != nil {
		t.Fatalf("Cyclomatic err: %v", err)
	}
	r, ok := findFunc(results, "Day")
	if !ok {
		t.Fatal("Day not found")
	}
	// base 1 + 3 cases (default not counted) = 4
	if r.Score != 4 {
		t.Errorf("expected score 4, got %d", r.Score)
	}
}

func TestCyclomatic_LogicalOperators(t *testing.T) {
	dir := t.TempDir()
	src := `package fixture

func Valid(a, b, c int) bool {
	if a > 0 && b > 0 || c > 0 {
		return true
	}
	return false
}
`
	path := writeFixture(t, dir, "logic.go", src)
	results, err := Cyclomatic(path)
	if err != nil {
		t.Fatalf("Cyclomatic err: %v", err)
	}
	r, ok := findFunc(results, "Valid")
	if !ok {
		t.Fatal("Valid not found")
	}
	// base 1 + if + && + || = 4
	if r.Score != 4 {
		t.Errorf("expected score 4, got %d", r.Score)
	}
}

func TestCyclomatic_SelectStatement(t *testing.T) {
	dir := t.TempDir()
	src := `package fixture

func Pick(a, b chan int) int {
	select {
	case v := <-a:
		return v
	case v := <-b:
		return v
	default:
		return 0
	}
}
`
	path := writeFixture(t, dir, "selectstmt.go", src)
	results, err := Cyclomatic(path)
	if err != nil {
		t.Fatalf("Cyclomatic err: %v", err)
	}
	r, ok := findFunc(results, "Pick")
	if !ok {
		t.Fatal("Pick not found")
	}
	// base 1 + 2 comm clauses (default has nil Comm) = 3
	if r.Score != 3 {
		t.Errorf("expected score 3, got %d", r.Score)
	}
}

func TestCyclomatic_MethodReceiver(t *testing.T) {
	dir := t.TempDir()
	src := `package fixture

type Counter struct{ n int }

func (c *Counter) Inc() { c.n++ }

func (c Counter) Value() int { return c.n }
`
	path := writeFixture(t, dir, "methods.go", src)
	results, err := Cyclomatic(path)
	if err != nil {
		t.Fatalf("Cyclomatic err: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 methods, got %d", len(results))
	}
	inc, ok := findFunc(results, "Inc")
	if !ok {
		t.Fatal("Inc not found")
	}
	if inc.Receiver != "*Counter" {
		t.Errorf("expected receiver *Counter, got %q", inc.Receiver)
	}
	val, ok := findFunc(results, "Value")
	if !ok {
		t.Fatal("Value not found")
	}
	if val.Receiver != "Counter" {
		t.Errorf("expected receiver Counter, got %q", val.Receiver)
	}
}

func TestCyclomatic_NestedFuncLitNotCounted(t *testing.T) {
	dir := t.TempDir()
	src := `package fixture

func Outer() {
	f := func(x int) int {
		if x > 0 {
			return x
		}
		if x < 0 {
			return -x
		}
		return 0
	}
	_ = f
}
`
	path := writeFixture(t, dir, "funclit.go", src)
	results, err := Cyclomatic(path)
	if err != nil {
		t.Fatalf("Cyclomatic err: %v", err)
	}
	r, ok := findFunc(results, "Outer")
	if !ok {
		t.Fatal("Outer not found")
	}
	// Outer should be base 1 because nested FuncLit is skipped.
	if r.Score != 1 {
		t.Errorf("expected score 1 for Outer (nested func lit excluded), got %d", r.Score)
	}
}

func TestComplexityCategory(t *testing.T) {
	cases := []struct {
		score int
		want  string
	}{
		{1, "simple"},
		{5, "simple"},
		{6, "moderate"},
		{10, "moderate"},
		{11, "complex"},
		{20, "complex"},
		{21, "very complex"},
		{50, "very complex"},
		{51, "untestable"},
		{500, "untestable"},
	}
	for _, c := range cases {
		got := ComplexityCategory(c.score)
		if got != c.want {
			t.Errorf("ComplexityCategory(%d) = %q, want %q", c.score, got, c.want)
		}
	}
}

func TestComputeStats_Aggregation(t *testing.T) {
	complexities := []FuncComplexity{
		{Func: "a", Score: 1},
		{Func: "b", Score: 3},
		{Func: "c", Score: 7},
		{Func: "d", Score: 15},
		{Func: "e", Score: 60},
	}
	stats := ComputeStats(complexities)
	if stats.TotalFunctions != 5 {
		t.Errorf("TotalFunctions = %d, want 5", stats.TotalFunctions)
	}
	if stats.TotalScore != 86 {
		t.Errorf("TotalScore = %d, want 86", stats.TotalScore)
	}
	if stats.MinScore != 1 {
		t.Errorf("MinScore = %d, want 1", stats.MinScore)
	}
	if stats.MaxScore != 60 {
		t.Errorf("MaxScore = %d, want 60", stats.MaxScore)
	}
	wantAvg := 86.0 / 5.0
	if stats.AvgScore != wantAvg {
		t.Errorf("AvgScore = %v, want %v", stats.AvgScore, wantAvg)
	}
	if stats.Distribution["simple"] != 2 {
		t.Errorf("simple count = %d, want 2", stats.Distribution["simple"])
	}
	if stats.Distribution["moderate"] != 1 {
		t.Errorf("moderate count = %d, want 1", stats.Distribution["moderate"])
	}
	if stats.Distribution["complex"] != 1 {
		t.Errorf("complex count = %d, want 1", stats.Distribution["complex"])
	}
	if stats.Distribution["untestable"] != 1 {
		t.Errorf("untestable count = %d, want 1", stats.Distribution["untestable"])
	}
}

func TestComputeStats_Empty(t *testing.T) {
	stats := ComputeStats(nil)
	if stats.TotalFunctions != 0 {
		t.Errorf("TotalFunctions = %d, want 0", stats.TotalFunctions)
	}
	if stats.TotalScore != 0 {
		t.Errorf("TotalScore = %d, want 0", stats.TotalScore)
	}
	if stats.Distribution == nil {
		t.Error("Distribution should be non-nil for empty input")
	}
	if len(stats.Distribution) != 0 {
		t.Errorf("Distribution should be empty, got %v", stats.Distribution)
	}
}

func TestFormatReport_Empty(t *testing.T) {
	out := FormatReport(nil, 0)
	if out != "No functions found." {
		t.Errorf("expected empty report message, got %q", out)
	}
}

func TestFormatReport_RendersAndTopLimit(t *testing.T) {
	complexities := []FuncComplexity{
		{File: "/tmp/foo.go", Func: "Big", Score: 25, Line: 10},
		{File: "/tmp/foo.go", Func: "Mid", Receiver: "*T", Score: 7, Line: 30},
		{File: "/tmp/foo.go", Func: "Small", Score: 2, Line: 50},
	}
	out := FormatReport(complexities, 2)
	if !strings.Contains(out, "CYCLOMATIC COMPLEXITY REPORT") {
		t.Error("report missing header")
	}
	if !strings.Contains(out, "foo.go:10") {
		t.Error("report missing first entry location")
	}
	if !strings.Contains(out, "Big") {
		t.Error("report missing first entry name")
	}
	if !strings.Contains(out, "very complex") {
		t.Errorf("expected category 'very complex' for score 25, got: %s", out)
	}
	if !strings.Contains(out, "*T.Mid") {
		t.Error("report missing receiver-qualified method name")
	}
	if strings.Contains(out, "Small") {
		t.Error("top=2 should have excluded the third entry")
	}
}

func TestCyclomatic_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	// A package declaration with no functions.
	src := `package fixture
`
	path := writeFixture(t, dir, "empty.go", src)
	results, err := Cyclomatic(path)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 functions, got %d", len(results))
	}
}

func TestCyclomatic_OnlyComments(t *testing.T) {
	dir := t.TempDir()
	src := `// Package fixture only contains comments.
package fixture

// Just a comment.
// And another.
`
	path := writeFixture(t, dir, "comments.go", src)
	results, err := Cyclomatic(path)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 functions, got %d", len(results))
	}
}

func TestCyclomatic_SyntaxError(t *testing.T) {
	dir := t.TempDir()
	src := `package fixture

func Broken( {
	this is not valid go
}
`
	path := writeFixture(t, dir, "broken.go", src)
	_, err := Cyclomatic(path)
	if err == nil {
		t.Error("expected error from syntax-broken file, got nil")
	}
}

func TestCyclomatic_NonexistentFile(t *testing.T) {
	_, err := Cyclomatic("/nonexistent/path/does/not/exist.go")
	if err == nil {
		t.Error("expected error for nonexistent file, got nil")
	}
}

func TestCyclomaticDir_SkipsTestFilesAndSorts(t *testing.T) {
	dir := t.TempDir()

	writeFixture(t, dir, "a.go", `package fixture

func Trivial() {}
`)
	writeFixture(t, dir, "b.go", `package fixture

func Heavy(n int) int {
	if n > 0 {
		if n > 10 {
			if n > 100 {
				return 3
			}
			return 2
		}
		return 1
	}
	return 0
}
`)
	// Test file should be skipped.
	writeFixture(t, dir, "ignored_test.go", `package fixture

func TestIgnored() {
	if true {
		_ = 1
	}
}
`)

	results, err := CyclomaticDir(dir)
	if err != nil {
		t.Fatalf("CyclomaticDir err: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results (test file skipped), got %d", len(results))
	}
	// Sorted descending by score: Heavy first.
	if results[0].Func != "Heavy" {
		t.Errorf("expected first result Heavy, got %q", results[0].Func)
	}
	if results[0].Score <= results[1].Score {
		t.Errorf("expected descending order, got %d then %d", results[0].Score, results[1].Score)
	}
	for _, r := range results {
		if r.Func == "TestIgnored" {
			t.Error("test function should have been skipped")
		}
	}
}

func TestCyclomaticRecursive_WalksSubdirs(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	writeFixture(t, root, "top.go", `package fixture

func Top() int { return 1 }
`)
	writeFixture(t, sub, "nested.go", `package nested

func Nested(x int) int {
	if x > 0 {
		return x
	}
	return -x
}
`)

	results, err := CyclomaticRecursive(root)
	if err != nil {
		t.Fatalf("CyclomaticRecursive err: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 functions across tree, got %d", len(results))
	}
	if _, ok := findFunc(results, "Top"); !ok {
		t.Error("Top not found in recursive scan")
	}
	if _, ok := findFunc(results, "Nested"); !ok {
		t.Error("Nested not found in recursive scan")
	}
}

func TestFuncWithoutBody(t *testing.T) {
	dir := t.TempDir()
	// External / assembly-implemented function: no body.
	src := `package fixture

func External(a int) int
`
	path := writeFixture(t, dir, "external.go", src)
	results, err := Cyclomatic(path)
	if err != nil {
		t.Fatalf("Cyclomatic err: %v", err)
	}
	r, ok := findFunc(results, "External")
	if !ok {
		t.Fatal("External not found")
	}
	if r.Score != 1 {
		t.Errorf("expected score 1 for body-less func, got %d", r.Score)
	}
}
