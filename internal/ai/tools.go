package ai

import (
	"github.com/costa/polypod/internal/skill"
	openai "github.com/sashabaranov/go-openai"
)

const maxToolIter = 10

// SkillRegistry is the interface the AI client needs from the skill system.
type SkillRegistry interface {
	ToolDefinitions(names []string) []openai.Tool
	Execute(name string, argsJSON string) (string, error)
}

// ensure skill.Registry implements SkillRegistry
var _ SkillRegistry = (*skill.Registry)(nil)
