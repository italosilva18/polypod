package subagent

import (
	"errors"
	"strings"
	"testing"
)

// TestBuildForkedMessages_DoesNotMutateParent verifies that the input slice
// is preserved byte-for-byte after BuildForkedMessages runs. This is
// load-bearing for prompt caching: the caller reuses parentMessages to
// render its own UI/logs and any mutation would break that.
func TestBuildForkedMessages_DoesNotMutateParent(t *testing.T) {
	parent := []any{
		"system: you are helpful",
		"user: list files",
		"assistant: here they are",
	}
	// Capture a reference-equal snapshot so we can detect element swaps,
	// and the length so we can detect appends-that-leaked.
	snapshot := make([]any, len(parent))
	copy(snapshot, parent)
	origLen := len(parent)

	_ = BuildForkedMessages(parent, "summarize the above")

	if len(parent) != origLen {
		t.Fatalf("parent slice length changed: got %d want %d", len(parent), origLen)
	}
	for i := range parent {
		if parent[i] != snapshot[i] {
			t.Fatalf("parent[%d] mutated: got %v want %v", i, parent[i], snapshot[i])
		}
	}
}

// TestBuildForkedMessages_AppendsDirectiveWithTag verifies that the result
// is the parent prefix followed by exactly one new message, and that the
// new message contains ForkBoilerplateTag + the directive body.
func TestBuildForkedMessages_AppendsDirectiveWithTag(t *testing.T) {
	parent := []any{
		"msg1",
		"msg2",
	}
	directive := "find all TODOs in the repo"

	result := BuildForkedMessages(parent, directive)

	if len(result) != len(parent)+1 {
		t.Fatalf("expected len %d, got %d", len(parent)+1, len(result))
	}
	// Verify prefix is byte-identical to the parent.
	for i := range parent {
		if result[i] != parent[i] {
			t.Fatalf("prefix diverged at %d: got %v want %v", i, result[i], parent[i])
		}
	}
	// Verify trailing message carries the boilerplate tag + directive.
	last, ok := result[len(result)-1].(string)
	if !ok {
		t.Fatalf("expected trailing message to be a string, got %T", result[len(result)-1])
	}
	if !strings.Contains(last, ForkBoilerplateTag) {
		t.Errorf("trailing message missing ForkBoilerplateTag: %q", last)
	}
	if !strings.Contains(last, directive) {
		t.Errorf("trailing message missing directive body %q: got %q", directive, last)
	}
}

// TestIsForkedChild_DetectsForkedConversation verifies that a conversation
// whose tail contains ForkBoilerplateTag is flagged as a fork child. This
// is the recursion guard in the loop.
func TestIsForkedChild_DetectsForkedConversation(t *testing.T) {
	parent := []any{
		"system: boot",
		"user: do a thing",
	}
	child := BuildForkedMessages(parent, "the child scope")

	if !IsForkedChild(child) {
		t.Fatalf("expected IsForkedChild to report true for a forked conversation; tail=%v", child[len(child)-1])
	}
}

// TestIsForkedChild_ReturnsFalseForUnforked verifies that a plain parent
// conversation (never passed through BuildForkedMessages) is not
// misidentified as a fork.
func TestIsForkedChild_ReturnsFalseForUnforked(t *testing.T) {
	msgs := []any{
		"system: be helpful",
		"user: hello",
		"assistant: hi there",
		"user: what's the weather",
	}
	if IsForkedChild(msgs) {
		t.Fatalf("expected IsForkedChild to be false for a non-forked conversation")
	}
}

// TestChildAndValidateForkDepth verifies three things in one pass:
//  1. Child() increments depth by exactly one
//  2. Child() deep-copies Env and ToolAllowList so parent mutations do not
//     affect the child
//  3. ValidateForkDepth returns ErrMaxForkDepth once the depth reaches the
//     cap
func TestChildAndValidateForkDepth(t *testing.T) {
	root := InheritedContext{
		CWD:             "/tmp/root",
		Env:             map[string]string{"FOO": "bar"},
		ToolAllowList:   []string{"Read", "Bash"},
		ParentSessionID: "sess-123",
		Depth:           0,
	}

	// Step 1: Child() increments depth.
	c1 := root.Child()
	if c1.Depth != 1 {
		t.Fatalf("expected child depth 1, got %d", c1.Depth)
	}
	if c1.CWD != root.CWD {
		t.Errorf("expected CWD to propagate, got %q", c1.CWD)
	}
	if c1.ParentSessionID != root.ParentSessionID {
		t.Errorf("expected ParentSessionID to propagate, got %q", c1.ParentSessionID)
	}

	// Step 2: mutate parent Env/ToolAllowList after Child() — child must be
	// unaffected.
	root.Env["FOO"] = "changed"
	root.ToolAllowList[0] = "Evil"
	if c1.Env["FOO"] != "bar" {
		t.Errorf("Child() did not deep-copy Env: got %q", c1.Env["FOO"])
	}
	if c1.ToolAllowList[0] != "Read" {
		t.Errorf("Child() did not deep-copy ToolAllowList: got %q", c1.ToolAllowList[0])
	}

	// Step 3: walk the depth chain up to MaxForkDepth and assert the
	// overflow is caught.
	cur := root
	cur.Depth = 0
	for i := 0; i < MaxForkDepth; i++ {
		if err := ValidateForkDepth(cur); err != nil {
			t.Fatalf("ValidateForkDepth returned early at depth %d: %v", cur.Depth, err)
		}
		cur = cur.Child()
	}
	// Now cur.Depth == MaxForkDepth — any further fork must be rejected.
	if err := ValidateForkDepth(cur); !errors.Is(err, ErrMaxForkDepth) {
		t.Fatalf("expected ErrMaxForkDepth at depth %d, got %v", cur.Depth, err)
	}
}

// TestChildDirectiveByteShape is an extra sanity check on the exact shape
// of the emitted directive string. It pins the format so an unintentional
// edit to ChildDirective breaks tests loudly rather than silently busting
// the prompt cache at runtime.
func TestChildDirectiveByteShape(t *testing.T) {
	s := ChildDirective("do X")
	// Must start with the opening tag.
	if !strings.HasPrefix(s, ForkBoilerplateTag) {
		t.Errorf("directive does not start with %q: %q", ForkBoilerplateTag, s)
	}
	// Must end with a closing tag matching the opener.
	closer := "</" + strings.TrimPrefix(ForkBoilerplateTag, "<")
	if !strings.HasSuffix(s, closer) {
		t.Errorf("directive does not end with %q: %q", closer, s)
	}
	// Must contain the directive body.
	if !strings.Contains(s, "do X") {
		t.Errorf("directive missing body: %q", s)
	}
}
