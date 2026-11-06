package compact

import (
	"strings"
	"testing"

	"github.com/costa/polypod/internal/messages"
)

// buildToolRoundtrip returns a message slice with n assistant tool_use +
// user tool_result pairs, each tool_result carrying a large payload. The
// tool_use_id values are predictable so tests can assert linkage survival.
func buildToolRoundtrip(n int, payloadSize int) []messages.Message {
	var msgs []messages.Message
	payload := strings.Repeat("A", payloadSize)
	for i := 0; i < n; i++ {
		id := "tu_" + string(rune('a'+i))
		// assistant tool_use
		msgs = append(msgs, messages.Message{
			Role: messages.RoleAssistant,
			Content: []messages.ContentBlock{
				{Type: messages.ContentText, Text: "running tool"},
				{Type: messages.ContentToolUse, ID: id, Name: "bash"},
			},
		})
		// user tool_result with the same id
		msgs = append(msgs, messages.Message{
			Role: messages.RoleUser,
			Content: []messages.ContentBlock{
				{Type: messages.ContentToolResult, ToolUseID: id, Content: payload},
			},
		})
	}
	return msgs
}

func TestDefaultSnipPolicy_Defaults(t *testing.T) {
	p := DefaultSnipPolicy()
	if p.KeepLastN != 5 {
		t.Errorf("KeepLastN = %d, want 5", p.KeepLastN)
	}
	if p.MaxPerResult != 200 {
		t.Errorf("MaxPerResult = %d, want 200", p.MaxPerResult)
	}
	if p.Placeholder == "" {
		t.Error("Placeholder must not be empty")
	}
}

func TestSnipToolResults_KeepsLastNIntact(t *testing.T) {
	// 8 tool-result messages, keep last 3 verbatim, snip the first 5.
	msgs := buildToolRoundtrip(8, 500)
	policy := SnipPolicy{
		KeepLastN:    3,
		MaxPerResult: 0, // replace whole content
		Placeholder:  "[snipped]",
	}

	out := SnipToolResults(msgs, policy)

	if len(out) != len(msgs) {
		t.Fatalf("length changed: got %d want %d", len(out), len(msgs))
	}

	// Each pair is (assistant, user). Pairs 0..4 are old (snipped),
	// pairs 5..7 are kept. Indexing in the flat slice: user tool_result is
	// at odd indexes 1, 3, 5, 7, 9, 11, 13, 15.
	snippedUserIdx := []int{1, 3, 5, 7, 9}
	keptUserIdx := []int{11, 13, 15}

	for _, i := range snippedUserIdx {
		block := out[i].Content[0]
		if block.Content != "[snipped]" {
			t.Errorf("message %d: expected snipped placeholder, got %q", i, block.Content)
		}
		// Linkage must be preserved.
		if block.ToolUseID == "" {
			t.Errorf("message %d: ToolUseID was lost after snipping", i)
		}
	}

	for _, i := range keptUserIdx {
		block := out[i].Content[0]
		if len(block.Content) != 500 {
			t.Errorf("message %d: expected full 500-byte payload, got %d bytes", i, len(block.Content))
		}
	}

	// Assistant messages (even indexes) must be entirely untouched.
	for i := 0; i < len(out); i += 2 {
		if len(out[i].Content) != 2 {
			t.Errorf("assistant message %d: content blocks mutated", i)
		}
		if out[i].Content[1].Type != messages.ContentToolUse {
			t.Errorf("assistant message %d: tool_use block lost", i)
		}
	}
}

func TestSnipToolResults_TruncatesOlderWithPrefix(t *testing.T) {
	// Single old tool_result, large payload, prefix preserved.
	msgs := buildToolRoundtrip(6, 1000)
	policy := SnipPolicy{
		KeepLastN:    1,
		MaxPerResult: 50,
		Placeholder:  "[cut]",
	}

	out := SnipToolResults(msgs, policy)

	// First 5 tool_result messages are snipped (odd indexes 1, 3, 5, 7, 9).
	for _, i := range []int{1, 3, 5, 7, 9} {
		c := out[i].Content[0].Content
		if !strings.HasPrefix(c, strings.Repeat("A", 50)) {
			t.Errorf("message %d: expected 50-byte prefix, got %q", i, c[:min(len(c), 60)])
		}
		if !strings.Contains(c, "[cut]") {
			t.Errorf("message %d: placeholder missing, got %q", i, c)
		}
		if len(c) >= 1000 {
			t.Errorf("message %d: payload not truncated, still %d bytes", i, len(c))
		}
	}

	// The last (kept) one should still be full size.
	kept := out[11].Content[0].Content
	if len(kept) != 1000 {
		t.Errorf("kept message payload = %d bytes, want 1000", len(kept))
	}
}

func TestSnipToolResults_DoesNotMutateInput(t *testing.T) {
	msgs := buildToolRoundtrip(4, 300)

	// Snapshot the original content strings.
	originals := make([]string, 0, len(msgs))
	for _, m := range msgs {
		for _, b := range m.Content {
			if b.Type == messages.ContentToolResult {
				originals = append(originals, b.Content)
			}
		}
	}

	out := SnipToolResults(msgs, SnipPolicy{
		KeepLastN:    0,
		MaxPerResult: 0,
		Placeholder:  "[x]",
	})

	// Re-check originals: the input slice and its blocks must be untouched.
	idx := 0
	for _, m := range msgs {
		for _, b := range m.Content {
			if b.Type == messages.ContentToolResult {
				if b.Content != originals[idx] {
					t.Errorf("input mutated: original[%d] changed to %q", idx, b.Content)
				}
				if len(b.Content) != 300 {
					t.Errorf("input mutated: original[%d] length %d", idx, len(b.Content))
				}
				idx++
			}
		}
	}

	// And the output must be a different backing slice.
	if &msgs[0] == &out[0] {
		// Shallow slice share is fine if no mutations — the important
		// invariant is that mutating `out` wouldn't affect `msgs`. Test
		// that by touching the output's content blocks.
		out[1].Content[0].Content = "tampered"
		if msgs[1].Content[0].Content == "tampered" {
			t.Error("mutating output altered input — backing array is shared")
		}
	}

	// Every snipped output must carry the placeholder.
	for i := 1; i < len(out); i += 2 {
		if out[i].Content[0].Content != "[x]" {
			t.Errorf("out[%d] not snipped: %q", i, out[i].Content[0].Content)
		}
	}
}

func TestSnipToolResults_PreservesToolUseIDLinking(t *testing.T) {
	msgs := buildToolRoundtrip(3, 400)
	out := SnipToolResults(msgs, SnipPolicy{
		KeepLastN:    0,
		MaxPerResult: 10,
		Placeholder:  "[snip]",
	})

	// Walk pairs and verify tool_use.ID == tool_result.ToolUseID.
	for i := 0; i < len(out); i += 2 {
		useID := out[i].Content[1].ID
		resID := out[i+1].Content[0].ToolUseID
		if useID == "" || resID == "" {
			t.Errorf("pair %d: empty ids use=%q res=%q", i/2, useID, resID)
		}
		if useID != resID {
			t.Errorf("pair %d: linkage broken use=%q res=%q", i/2, useID, resID)
		}
	}
}

func TestEstimateTokenSavings(t *testing.T) {
	before := buildToolRoundtrip(4, 1000)
	after := SnipToolResults(before, SnipPolicy{
		KeepLastN:    1,
		MaxPerResult: 0,
		Placeholder:  "[snipped]",
	})

	savings := EstimateTokenSavings(before, after)
	if savings <= 0 {
		t.Errorf("expected positive savings, got %d", savings)
	}
	// Sanity: we snipped 3 results of 1000 bytes each -> ~3000 bytes -> ~750 tokens.
	if savings < 500 {
		t.Errorf("savings too small, expected >= 500, got %d", savings)
	}
}

func TestEstimateTokenSavings_NoSavingsClampedToZero(t *testing.T) {
	msgs := buildToolRoundtrip(1, 10)
	// Policy with a huge placeholder is a pathological case.
	after := SnipToolResults(msgs, SnipPolicy{
		KeepLastN:    0,
		MaxPerResult: 0,
		Placeholder:  strings.Repeat("X", 500),
	})
	savings := EstimateTokenSavings(msgs, after)
	if savings < 0 {
		t.Errorf("savings must clamp to 0, got %d", savings)
	}
}

func TestSnipToolResults_EmptyInput(t *testing.T) {
	out := SnipToolResults(nil, DefaultSnipPolicy())
	if out == nil {
		t.Error("expected non-nil empty slice, got nil")
	}
	if len(out) != 0 {
		t.Errorf("expected empty slice, got len %d", len(out))
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
