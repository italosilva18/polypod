package tool

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/costa/polypod/internal/skill"
	"github.com/costa/polypod/internal/types"
)

// SkillAdapter wraps an existing skill.Skill to implement types.Tool.
// This allows gradual migration from the old Skill interface to the new Tool interface.
type SkillAdapter struct {
	skill     *skill.Skill
	aliasName string // optional override; when set, Name() returns this instead of skill.Name
}

// NewSkillAdapter wraps a skill.Skill as a types.Tool.
func NewSkillAdapter(s *skill.Skill) types.Tool {
	return &SkillAdapter{skill: s}
}

// NewSkillAdapterAs wraps a skill.Skill as a types.Tool exposed under a custom name.
// Used to avoid name collisions with builtin tools (e.g. expose skill "git_status"
// as tool "skill_git_status").
func NewSkillAdapterAs(s *skill.Skill, alias string) types.Tool {
	return &SkillAdapter{skill: s, aliasName: alias}
}

func (a *SkillAdapter) Name() string {
	if a.aliasName != "" {
		return a.aliasName
	}
	return a.skill.Name
}
func (a *SkillAdapter) Description() string { return a.skill.Description }

func (a *SkillAdapter) InputSchema() map[string]any {
	if a.skill.InputSchema != nil {
		return normalizedSkillSchema(a.skill.InputSchema)
	}
	// Marshal the definition to preserve nested schema details. Building the
	// map manually serialized a nil []string as JSON null, which the Responses
	// API rejects for functions without required arguments.
	raw, err := json.Marshal(a.skill.Parameters)
	if err == nil {
		var schema map[string]any
		if json.Unmarshal(raw, &schema) == nil {
			return normalizedSkillSchema(schema)
		}
	}
	return map[string]any{"type": "object", "properties": map[string]any{}, "required": []string{}}
}

func normalizedSkillSchema(input map[string]any) map[string]any {
	schema := make(map[string]any, len(input)+3)
	for key, value := range input {
		schema[key] = value
	}
	if schema["type"] == nil {
		schema["type"] = "object"
	}
	if schema["type"] == "object" {
		if schema["properties"] == nil {
			schema["properties"] = map[string]any{}
		}
		if schema["required"] == nil {
			schema["required"] = []string{}
		}
	}
	return schema
}

func (a *SkillAdapter) Execute(ctx types.ToolContext, args map[string]any) (types.ToolResult, error) {
	if a.skill.ExecuteResult != nil {
		callCtx := ctx.Context
		if callCtx == nil {
			callCtx = context.Background()
		}
		return a.skill.ExecuteResult(callCtx, args)
	}
	// Convert map[string]any to map[string]string for the old interface
	strArgs := make(map[string]string, len(args))
	for k, v := range args {
		if s, ok := v.(string); ok {
			strArgs[k] = s
		} else {
			strArgs[k] = fmt.Sprintf("%v", v)
		}
	}

	result, err := a.skill.Execute(strArgs)
	if err != nil {
		return types.ToolResult{Content: result, IsError: true}, nil
	}
	return types.ToolResult{Content: result}, nil
}

func (a *SkillAdapter) ValidateInput(args map[string]any) types.ValidationResult {
	// Check required fields
	for _, req := range a.skill.Parameters.Required {
		if _, ok := args[req]; !ok {
			return types.ValidationResult{Valid: false, Message: "campo obrigatorio: " + req}
		}
	}
	return types.ValidationResult{Valid: true}
}

func (a *SkillAdapter) CheckPermissions(args map[string]any, ctx types.ToolContext) types.PermissionResult {
	return types.PermissionResult{Behavior: types.PermissionAllow}
}

func (a *SkillAdapter) IsConcurrencySafe(args map[string]any) bool {
	// Read-only skills are generally concurrency safe
	return false
}

func (a *SkillAdapter) IsReadOnly(args map[string]any) bool {
	return false
}

func (a *SkillAdapter) IsDestructive(args map[string]any) bool {
	return false
}

func (a *SkillAdapter) IsEnabled() bool {
	return true
}

// hasRealToolEquivalent reports whether a skill name collides with a real
// builtin tool that should always be preferred (the safer/more capable
// implementation lives in tool/builtin.go).
func hasRealToolEquivalent(skillName string) bool {
	realTools := map[string]bool{
		"read_file":      true, // builtin: tool/readfile.go
		"list_directory": true, // builtin: tool/builtin.go (newListDirTool)
		"run_command":    true, // builtin: tool/bashtool.go (bash) is the safe one
		"search_files":   true, // builtin: glob + grep are the real ones
	}
	return realTools[skillName]
}

// RegisterAllSkills copies all skills from a skill.Registry into a tool.Registry,
// exposing them under a "skill_" prefix to avoid colliding with safer builtin
// tools that share the same names. Skills that have a real builtin equivalent
// (read_file, list_directory, run_command, search_files) are skipped entirely
// so the AI never sees the unsafe duplicate.
//
// This function is idempotent on its prefixed entries — calling it multiple
// times after more skills have been loaded will simply (re-)register them.
func RegisterAllSkills(toolReg *Registry, skillReg *skill.Registry) {
	for _, name := range skillReg.List() {
		if hasRealToolEquivalent(name) {
			continue
		}
		s, ok := skillReg.Get(name)
		if !ok {
			continue
		}
		toolReg.Register(NewSkillAdapterAs(s, "skill_"+name))
	}
}
