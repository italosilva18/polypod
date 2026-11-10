package ai

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	openai "github.com/sashabaranov/go-openai"
)

// Chat Completions streams fragments, not a per-call "arguments.done" event.
// A valid JSON object may still receive more fragments, and the stream can
// fail after it appears complete. We parse early but execute nothing until
// Recv returns EOF; this also preserves write barriers in the final call order.
type streamToolPreparation struct {
	byIndex map[int]*streamPreparedCall
}

type streamPreparedCall struct {
	call      openai.ToolCall
	parsedRaw string
	parsed    map[string]any
}

func newStreamToolPreparation() *streamToolPreparation {
	return &streamToolPreparation{byIndex: make(map[int]*streamPreparedCall)}
}

func (p *streamToolPreparation) add(delta openai.ToolCall) {
	index := 0
	if delta.Index != nil {
		index = *delta.Index
	}
	state := p.byIndex[index]
	if state == nil {
		state = &streamPreparedCall{}
		p.byIndex[index] = state
	}
	if delta.ID != "" {
		state.call.ID = delta.ID
	}
	if delta.Type != "" {
		state.call.Type = delta.Type
	}
	state.call.Function.Name += delta.Function.Name
	if delta.Function.Arguments == "" {
		return
	}
	state.call.Function.Arguments += delta.Function.Arguments
	state.parsed = nil
	state.parsedRaw = ""
	raw := state.call.Function.Arguments
	// Most deltas end in the middle of JSON. Avoid repeatedly parsing those
	// fragments; finalization always parses if no complete object was cached.
	if len(raw) > 256<<10 || !strings.HasSuffix(strings.TrimSpace(raw), "}") {
		return
	}
	var parsed map[string]any
	if json.Unmarshal([]byte(raw), &parsed) == nil && parsed != nil {
		state.parsed = parsed
		state.parsedRaw = raw
	}
}

// finalize validates every complete call before the caller can execute any
// tool. The response order is the provider's numeric tool-call index.
func (p *streamToolPreparation) finalize() ([]openai.ToolCall, []ToolCallInfo, error) {
	indices := make([]int, 0, len(p.byIndex))
	for index := range p.byIndex {
		if index < 0 {
			return nil, nil, fmt.Errorf("indice de ferramenta negativo no stream")
		}
		indices = append(indices, index)
	}
	sort.Ints(indices)
	calls := make([]openai.ToolCall, 0, len(indices))
	prepared := make([]ToolCallInfo, 0, len(indices))
	seenIDs := make(map[string]struct{}, len(indices))
	for _, index := range indices {
		state := p.byIndex[index]
		call := state.call
		if call.ID == "" || call.Function.Name == "" {
			return nil, nil, fmt.Errorf("chamada de ferramenta incompleta no indice %d", index)
		}
		if _, duplicate := seenIDs[call.ID]; duplicate {
			return nil, nil, fmt.Errorf("call ID de ferramenta repetido no stream")
		}
		seenIDs[call.ID] = struct{}{}
		parsed := state.parsed
		if state.parsedRaw != call.Function.Arguments || parsed == nil {
			if err := json.Unmarshal([]byte(call.Function.Arguments), &parsed); err != nil || parsed == nil {
				return nil, nil, fmt.Errorf("argumentos JSON invalidos para ferramenta %q", call.Function.Name)
			}
		}
		if call.Type == "" {
			call.Type = openai.ToolTypeFunction
		}
		calls = append(calls, call)
		prepared = append(prepared, ToolCallInfo{
			ID: call.ID, Name: call.Function.Name, Arguments: call.Function.Arguments,
			preparedArgs: parsed,
		})
	}
	return calls, prepared, nil
}

// Event callbacks and tool validators receive separate copies so neither can
// change the prepared arguments later consumed by another call stage.
func clonePreparedArgs(args map[string]any) map[string]any {
	copy := make(map[string]any, len(args))
	for key, value := range args {
		copy[key] = clonePreparedValue(value)
	}
	return copy
}

func clonePreparedValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return clonePreparedArgs(typed)
	case []any:
		copy := make([]any, len(typed))
		for i, item := range typed {
			copy[i] = clonePreparedValue(item)
		}
		return copy
	default:
		return value
	}
}
