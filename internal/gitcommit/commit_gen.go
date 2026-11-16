// Package commit_gen — Git Commit Message Generation
package commitcommit

import "fmt"

type CommitGenerator struct{}

func NewCommitGenerator() *CommitGenerator {
	return &CommitGenerator{}
}

func (g *CommitGenerator) GenerateMessage(changes []string, convention string) string {
	if len(changes) == 0 {
		return "feat: no changes"
	}
	
	switch convention {
	case "conventional":
		return fmt.Sprintf("feat: %s", changes[0])
	case "simple":
		return changes[0]
	default:
		return fmt.Sprintf("Update: %s", changes[0])
	}
}

func (g *CommitGenerator) SuggestType(changes []string) string {
	if len(changes) == 0 {
		return "feat"
	}
	
	change := changes[0]
	if strings.Contains(change, "fix") {
		return "fix"
	}
	if strings.Contains(change, "refactor") {
		return "refactor"
	}
	if strings.Contains(change, "test") {
		return "test"
	}
	return "feat"
}
