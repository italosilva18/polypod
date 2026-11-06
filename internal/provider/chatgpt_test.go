package provider

import (
	"encoding/json"
	"strings"
	"testing"
)

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

func TestTranslateChatGPTToolsSerializesValidObjectSchemas(t *testing.T) {
	legacy := map[string]interface{}{
		"type": "object", "properties": map[string]interface{}(nil), "required": []string(nil),
	}
	nested := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"options": map[string]interface{}{"type": "object", "required": []string(nil), "properties": map[string]interface{}(nil)},
		},
		"required": []string{"options"},
		"$defs":    map[string]interface{}{"identifier": map[string]interface{}{"type": "string"}},
	}
	encoded, err := json.Marshal(translateChatGPTTools([]Tool{
		{Name: "no_args", Parameters: legacy},
		{Name: "with_options", Parameters: nested},
	}))
	if err != nil {
		t.Fatal(err)
	}
	var wire []map[string]interface{}
	if err := json.Unmarshal(encoded, &wire); err != nil {
		t.Fatal(err)
	}
	if len(wire) != 2 {
		t.Fatalf("wire tools = %d", len(wire))
	}
	params := wire[0]["parameters"].(map[string]interface{})
	if _, ok := params["properties"].(map[string]interface{}); !ok {
		t.Fatalf("no-arg properties is not an object: %s", encoded)
	}
	if required, ok := params["required"].([]interface{}); !ok || len(required) != 0 {
		t.Fatalf("no-arg required is not an empty array: %s", encoded)
	}
	params = wire[1]["parameters"].(map[string]interface{})
	options := params["properties"].(map[string]interface{})["options"].(map[string]interface{})
	if required, ok := options["required"].([]interface{}); !ok || len(required) != 0 {
		t.Fatalf("nested required is not an empty array: %s", encoded)
	}
	if _, ok := params["$defs"].(map[string]interface{}); !ok {
		t.Fatalf("$defs was lost: %s", encoded)
	}
	if required, ok := legacy["required"].([]string); !ok || required != nil {
		t.Fatal("normalization mutated original schema")
	}
}

func TestChatGPTImageWirePayload(t *testing.T) {
	input, _ := translateChatGPTMessages([]Message{{Role: "user", Content: "screen", Parts: []ContentPart{{Type: "image_base64", MimeType: "image/png", ImageB64: "cG5n"}}}})
	data, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"type":"input_image"`) || !strings.Contains(string(data), "data:image/png;base64,cG5n") || !strings.Contains(string(data), "screen") {
		t.Fatalf("image not serialized: %s", data)
	}
}
