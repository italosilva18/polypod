// Package skills — Auto-Skill Learning System extracted from Claude Code
// Sources:
// - src/skills/bundled/skillify.ts
// - src/skills/bundled/learn.ts

package skills

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/costa/polypod/internal/memory"
	"github.com/costa/polypod/internal/session"
)

// Skill represents a learned skill
type Skill struct {
	Name           string   `json:"name"`
	Description    string   `json:"description"`
	AllowedTools   []string `json:"allowed_tools,omitempty"`
	WhenToUse      string   `json:"when_to_use"`
	ArgumentHint   string   `json:"argument_hint,omitempty"`
	Arguments      []string `json:"arguments,omitempty"`
	Context        string   `json:"context,omitempty"` // inline or fork
	Goal           string   `json:"goal"`
	Steps          []Step   `json:"steps"`
	SavedAt        string   `json:"saved_at"`
	SourceSession  string   `json:"source_session,omitempty"`
}

// Step represents a skill step
type Step struct {
	Name           string   `json:"name"`
	Description    string   `json:"description"`
	SuccessCriteria []string `json:"success_criteria"`
	Execution      string   `json:"execution,omitempty"` // Direct, Task, Teammate, [human]
	Artifacts      []string `json:"artifacts,omitempty"`
	HumanCheckpoint bool    `json:"human_checkpoint,omitempty"`
	Rules          []string `json:"rules,omitempty"`
}

// SkillLearner learns skills from sessions
type SkillLearner struct {
	sessionDB  *session.Manager
	memoryDB   *memory.MemoryManager
	skillDir   string
	personalSkillDir string
}

// NewSkillLearner creates a new skill learner
func NewSkillLearner(sessionDB *session.Manager, memoryDB *memory.MemoryManager, skillDir, personalSkillDir string) (*SkillLearner, error) {
	return &SkillLearner{
		sessionDB:  sessionDB,
		memoryDB:   memoryDB,
		skillDir:   skillDir,
		personalSkillDir: personalSkillDir,
	}, nil
}

// LearnFromSession learns a skill from a session
func (l *SkillLearner) LearnFromSession(sessionID, userDescription string) (*Skill, error) {
	// Get session
	sess, err := l.sessionDB.Get(sessionID)
	if err != nil {
		return nil, err
	}

	// Extract user messages
	userMessages := extractUserMessages(sess.Messages)

	// Get session memory
	sessionMemory := ""
	if l.memoryDB != nil {
		memories, _ := l.memoryDB.Recall(sessionID)
		for _, mem := range memories {
			sessionMemory += mem.Content + "\n"
		}
	}

	// Analyze session to identify patterns
	patterns := l.analyzeSession(userMessages, sessionMemory, userDescription)

	// Generate skill from patterns
	skill := l.generateSkill(patterns, userDescription)

	return skill, nil
}

// extractUserMessages extracts user messages from session
func extractUserMessages(messages []session.Message) []string {
	var userMessages []string
	for _, msg := range messages {
		if msg.Role == "user" {
			userMessages = append(userMessages, msg.Content)
		}
	}
	return userMessages
}

// analyzeSession analyzes session to identify patterns
func (l *SkillLearner) analyzeSession(userMessages []string, sessionMemory, userDescription string) []Pattern {
	var patterns []Pattern

	// Identify repeated processes
	processes := identifyProcesses(userMessages)
	patterns = append(patterns, processes...)

	// Identify tool usage patterns
	tools := identifyToolUsage(userMessages)
	patterns = append(patterns, tools...)

	// Identify success criteria
	criteria := identifySuccessCriteria(userMessages, sessionMemory)
	patterns = append(patterns, criteria...)

	// Identify user preferences
	prefs := identifyUserPreferences(userMessages)
	patterns = append(patterns, prefs...)

	return patterns
}

// Pattern represents a learned pattern
type Pattern struct {
	Type        string `json:"type"` // process, tool, criteria, preference
	Name        string `json:"name"`
	Description string `json:"description"`
	Details     string `json:"details,omitempty"`
}

func identifyProcesses(messages []string) []Pattern {
	var patterns []Pattern
	
	// Look for process indicators
	processKeywords := []string{"step", "then", "next", "after", "finally"}
	
	for _, msg := range messages {
		lower := strings.ToLower(msg)
		for _, keyword := range processKeywords {
			if strings.Contains(lower, keyword) {
				patterns = append(patterns, Pattern{
					Type:        "process",
					Name:        "Process Step",
					Description: msg,
					Details:     fmt.Sprintf("Contains keyword: %s", keyword),
				})
			}
		}
	}
	
	return patterns
}

func identifyToolUsage(messages []string) []Pattern {
	var patterns []Pattern
	
	toolKeywords := []string{"bash", "read", "write", "edit", "grep", "glob"}
	
	for _, msg := range messages {
		lower := strings.ToLower(msg)
		for _, keyword := range toolKeywords {
			if strings.Contains(lower, keyword) {
				patterns = append(patterns, Pattern{
					Type:        "tool",
					Name:        "Tool Usage",
					Description: msg,
					Details:     fmt.Sprintf("Uses: %s", keyword),
				})
			}
		}
	}
	
	return patterns
}

func identifySuccessCriteria(messages []string, sessionMemory string) []Pattern {
	var patterns []Pattern
	
	criteriaKeywords := []string{"success", "done", "complete", "finished", "working", "passing"}
	
	allText := sessionMemory
	for _, msg := range messages {
		allText += " " + msg
	}
	
	lower := strings.ToLower(allText)
	for _, keyword := range criteriaKeywords {
		if strings.Contains(lower, keyword) {
			patterns = append(patterns, Pattern{
				Type:        "criteria",
				Name:        "Success Criteria",
				Description: "Success indicator found",
				Details:     fmt.Sprintf("Keyword: %s", keyword),
			})
		}
	}
	
	return patterns
}

func identifyUserPreferences(messages []string) []Pattern {
	var patterns []Pattern
	
	preferenceKeywords := []string{"prefer", "always", "never", "should", "must", "don't", "dont"}
	
	for _, msg := range messages {
		lower := strings.ToLower(msg)
		for _, keyword := range preferenceKeywords {
			if strings.Contains(lower, keyword) {
				patterns = append(patterns, Pattern{
					Type:        "preference",
					Name:        "User Preference",
					Description: msg,
					Details:     fmt.Sprintf("Keyword: %s", keyword),
				})
			}
		}
	}
	
	return patterns
}

// generateSkill generates a skill from patterns
func (l *SkillLearner) generateSkill(patterns []Pattern, userDescription string) *Skill {
	skill := &Skill{
		Name:        "learned-skill",
		Description: userDescription,
		Goal:        "Automate the process learned from session",
		SavedAt:     time.Now().Format(time.RFC3339),
	}

	// Extract steps from process patterns
	var steps []Step
	for _, pattern := range patterns {
		if pattern.Type == "process" {
			steps = append(steps, Step{
				Name:        pattern.Name,
				Description: pattern.Description,
			})
		}
	}

	// If no steps found, create a generic one
	if len(steps) == 0 {
		steps = append(steps, Step{
			Name:        "Execute Process",
			Description: userDescription,
			SuccessCriteria: []string{"Process completed successfully"},
		})
	}

	skill.Steps = steps

	// Extract allowed tools from tool patterns
	var tools []string
	for _, pattern := range patterns {
		if pattern.Type == "tool" {
			tools = append(tools, pattern.Details)
		}
	}
	skill.AllowedTools = tools

	return skill
}

// SaveSkill saves a skill to file
func (l *SkillLearner) SaveSkill(skill *Skill, destination string) error {
	var filePath string
	if destination == "repo" {
		filePath = filepath.Join(l.skillDir, skill.Name, "SKILL.md")
	} else {
		filePath = filepath.Join(l.personalSkillDir, skill.Name, "SKILL.md")
	}

	// Ensure directory exists
	dir := filepath.Dir(filePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	// Generate SKILL.md content
	content := generateSkillMarkdown(skill)

	// Write file
	return os.WriteFile(filePath, []byte(content), 0644)
}

// generateSkillMarkdown generates SKILL.md content
func generateSkillMarkdown(skill *Skill) string {
	var sb strings.Builder

	sb.WriteString("---\n")
	sb.WriteString(fmt.Sprintf("name: %s\n", skill.Name))
	sb.WriteString(fmt.Sprintf("description: %s\n", skill.Description))
	
	if len(skill.AllowedTools) > 0 {
		sb.WriteString("allowed-tools:\n")
		for _, tool := range skill.AllowedTools {
			sb.WriteString(fmt.Sprintf("  - %s\n", tool))
		}
	}
	
	sb.WriteString(fmt.Sprintf("when_to_use: %s\n", skill.WhenToUse))
	
	if skill.ArgumentHint != "" {
		sb.WriteString(fmt.Sprintf("argument-hint: \"%s\"\n", skill.ArgumentHint))
	}
	
	if len(skill.Arguments) > 0 {
		sb.WriteString("arguments:\n")
		for _, arg := range skill.Arguments {
			sb.WriteString(fmt.Sprintf("  - %s\n", arg))
		}
	}
	
	if skill.Context != "" {
		sb.WriteString(fmt.Sprintf("context: %s\n", skill.Context))
	}
	
	sb.WriteString("---\n\n")
	
	sb.WriteString(fmt.Sprintf("# %s\n\n", skill.Name))
	sb.WriteString(fmt.Sprintf("%s\n\n", skill.Description))
	
	sb.WriteString("## Goal\n\n")
	sb.WriteString(fmt.Sprintf("%s\n\n", skill.Goal))
	
	sb.WriteString("## Steps\n\n")
	for i, step := range skill.Steps {
		sb.WriteString(fmt.Sprintf("### %d. %s\n\n", i+1, step.Name))
		sb.WriteString(fmt.Sprintf("%s\n\n", step.Description))
		
		if len(step.SuccessCriteria) > 0 {
			sb.WriteString("**Success criteria**:\n")
			for _, criteria := range step.SuccessCriteria {
				sb.WriteString(fmt.Sprintf("- %s\n", criteria))
			}
			sb.WriteString("\n")
		}
	}

	return sb.String()
}

// ReviewSkill reviews a skill and proposes improvements
type SkillReview struct {
	Skill       *Skill   `json:"skill"`
	Improvements []string `json:"improvements"`
	Ready       bool     `json:"ready"`
}

func (l *SkillLearner) ReviewSkill(skill *Skill) *SkillReview {
	review := &SkillReview{
		Skill: skill,
		Ready: true,
	}

	// Check if skill has required fields
	if skill.Name == "" {
		review.Improvements = append(review.Improvements, "Add skill name")
		review.Ready = false
	}

	if skill.Description == "" {
		review.Improvements = append(review.Improvements, "Add skill description")
		review.Ready = false
	}

	if len(skill.Steps) == 0 {
		review.Improvements = append(review.Improvements, "Add at least one step")
		review.Ready = false
	}

	// Check if steps have success criteria
	for i, step := range skill.Steps {
		if len(step.SuccessCriteria) == 0 {
			review.Improvements = append(review.Improvements, 
				fmt.Sprintf("Step %d needs success criteria", i+1))
		}
	}

	return review
}

// ListSkills lists all skills
func (l *SkillLearner) ListSkills() ([]Skill, error) {
	var skills []Skill

	// Scan repo skills
	repoSkills, err := l.scanSkills(l.skillDir)
	if err == nil {
		skills = append(skills, repoSkills...)
	}

	// Scan personal skills
	personalSkills, err := l.scanSkills(l.personalSkillDir)
	if err == nil {
		skills = append(skills, personalSkills...)
	}

	return skills, nil
}

func (l *SkillLearner) scanSkills(dir string) ([]Skill, error) {
	var skills []Skill

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	for _, entry := range entries {
		if entry.IsDir() {
			skillPath := filepath.Join(dir, entry.Name(), "SKILL.md")
			content, err := os.ReadFile(skillPath)
			if err == nil {
				var skill Skill
				// Simple parsing - in production use proper YAML parser
				skill.Name = entry.Name()
				skill.Description = string(content)
				skills = append(skills, skill)
			}
		}
	}

	return skills, nil
}

// DeleteSkill deletes a skill
func (l *SkillLearner) DeleteSkill(name, destination string) error {
	var filePath string
	if destination == "repo" {
		filePath = filepath.Join(l.skillDir, name)
	} else {
		filePath = filepath.Join(l.personalSkillDir, name)
	}

	return os.RemoveAll(filePath)
}
