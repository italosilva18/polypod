package session

// InterruptedTurn describes the last user turn observed in a restored
// conversation. Tool records are observations only; this value must never be
// used to reissue a tool call.
type InterruptedTurn struct {
	LastUser       Message
	Unanswered     bool
	AmbiguousTools []ToolTraceRecord
}

// InspectInterruptedTurn compares the last user message with its assistant
// reply and the tool lifecycle journal. An unpaired start is ambiguous: the
// tool may have caused an external effect before the process stopped.
func InspectInterruptedTurn(messages []Message, records []ToolTraceRecord) InterruptedTurn {
	lastUser := -1
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" {
			lastUser = i
			break
		}
	}
	if lastUser < 0 {
		return InterruptedTurn{}
	}

	result := InterruptedTurn{LastUser: messages[lastUser], Unanswered: true}
	for _, msg := range messages[lastUser+1:] {
		if msg.Role == "assistant" {
			result.Unanswered = false
			break
		}
	}
	if result.LastUser.ID == "" {
		return result
	}

	// Match only records from this exact turn. A blank call ID cannot be
	// matched reliably and remains ambiguous even if a same-named terminal
	// event exists. Processing in journal order also handles repeated IDs.
	pending := make(map[string][]int)
	for _, record := range records {
		if record.TurnID != result.LastUser.ID {
			continue
		}
		if record.Phase == ToolTraceStart {
			index := len(result.AmbiguousTools)
			result.AmbiguousTools = append(result.AmbiguousTools, record)
			if record.CallID != "" {
				key := record.CallID + "\x00" + record.ToolName
				pending[key] = append(pending[key], index)
			}
			continue
		}
		if record.Phase != ToolTraceComplete && record.Phase != ToolTraceError {
			continue
		}
		key := record.CallID + "\x00" + record.ToolName
		indices := pending[key]
		if record.CallID == "" || len(indices) == 0 {
			continue
		}
		index := indices[0]
		pending[key] = indices[1:]
		result.AmbiguousTools[index].Phase = ""
	}

	visible := result.AmbiguousTools[:0]
	for _, record := range result.AmbiguousTools {
		if record.Phase == ToolTraceStart {
			visible = append(visible, record)
		}
	}
	result.AmbiguousTools = visible
	return result
}

// NeedsReview reports whether a resumed session warrants a manual check.
func (t InterruptedTurn) NeedsReview() bool {
	return t.Unanswered || len(t.AmbiguousTools) > 0
}
