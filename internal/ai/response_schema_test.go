package ai

import (
	"encoding/json"
	"testing"

	"github.com/costa/polypod/internal/humanloop"
	"github.com/costa/polypod/internal/skill"
	"github.com/costa/polypod/internal/tool"
	"github.com/openai/openai-go/v3/responses"
)

func TestBridgedHumanloopSkillsSerializeForResponses(t *testing.T) {
	skillRegistry := skill.NewRegistry()
	humanloop.RegisterSkills(skillRegistry, nil, nil)
	toolRegistry := tool.NewRegistry()
	for _, name := range []string{"heartbeat_status", "notify_user"} {
		s, ok := skillRegistry.Get(name)
		if !ok {
			t.Fatalf("missing skill %s", name)
		}
		toolRegistry.Register(tool.NewSkillAdapterAs(s, "skill_"+name))
	}
	definitions := NewToolRegistryAdapter(toolRegistry, nil).ToolDefinitions([]string{"heartbeat_status", "notify_user"})
	if len(definitions) != 2 {
		t.Fatalf("tool definitions = %d", len(definitions))
	}
	for _, definition := range definitions {
		fn := definition.Function
		if fn == nil {
			t.Fatal("missing function definition")
		}
		wire := responses.ToolUnionParam{OfFunction: &responses.FunctionToolParam{
			Name: fn.Name, Parameters: responseToolParameters(fn.Parameters),
		}}
		encoded, err := json.Marshal(wire)
		if err != nil {
			t.Fatal(err)
		}
		var serialized map[string]any
		if err := json.Unmarshal(encoded, &serialized); err != nil {
			t.Fatal(err)
		}
		params, ok := serialized["parameters"].(map[string]any)
		if !ok {
			t.Fatalf("%s parameters are not an object: %s", fn.Name, encoded)
		}
		if _, ok := params["properties"].(map[string]any); !ok {
			t.Fatalf("%s properties are not an object: %s", fn.Name, encoded)
		}
		required, ok := params["required"].([]any)
		if !ok {
			t.Fatalf("%s required is not an array: %s", fn.Name, encoded)
		}
		if fn.Name == "skill_heartbeat_status" && len(required) != 0 {
			t.Fatalf("heartbeat required args = %v", required)
		}
		if fn.Name == "skill_notify_user" && (len(required) != 1 || required[0] != "message") {
			t.Fatalf("notification required args = %v", required)
		}
	}
}
