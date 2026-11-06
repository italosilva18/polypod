package provider

import "testing"

func TestTranslateChatGPTMessagesUsesInstructionsAndFunctionOutputs(t *testing.T) {
	input, instructions := translateChatGPTMessages([]Message{
		{Role: "system", Content: "system rules"},
		{Role: "user", Content: "do it"},
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "call_1", Name: "shell", Arguments: `{"command":"pwd"}`}}},
		{Role: "tool", ToolCallID: "call_1", Content: "/tmp"},
	})
	if instructions != "system rules" {
		t.Fatalf("instructions = %q", instructions)
	}
	if len(input) != 3 {
		t.Fatalf("input items = %d, esperado 3", len(input))
	}
	if input[1].OfFunctionCall == nil || input[1].OfFunctionCall.Name != "shell" {
		t.Fatal("function call nao foi traduzida")
	}
	if input[2].OfFunctionCallOutput == nil || input[2].OfFunctionCallOutput.CallID != "call_1" {
		t.Fatal("function output nao foi traduzido")
	}
}

func TestTranslateChatGPTTools(t *testing.T) {
	tools := translateChatGPTTools([]Tool{{
		Name: "computer", Description: "operate locally",
		Parameters: map[string]interface{}{"type": "object"},
	}})
	if len(tools) != 1 || tools[0].OfFunction == nil || tools[0].OfFunction.Name != "computer" {
		t.Fatal("tool local nao foi traduzida para Responses API")
	}
}
