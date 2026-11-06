package hooks

import (
	"fmt"
	"log/slog"
	"strings"

	"gopkg.in/yaml.v3"
)

// FrontmatterHooks represents hooks parsed from CLAUDE.md YAML frontmatter.
// The YAML frontmatter is delimited by --- markers at the top of the file.
type FrontmatterHooks struct {
	Hooks HooksSettings `yaml:"hooks"`
}

// ParseFrontmatterHooks extracts hook configuration from YAML frontmatter
// in a CLAUDE.md (or skill) file. Returns nil if no frontmatter or no hooks.
//
// Frontmatter format:
//
//	---
//	hooks:
//	  PreToolUse:
//	    - matcher: "Write"
//	      hooks:
//	        - type: command
//	          command: "echo checking..."
//	  Stop:
//	    - hooks:
//	        - type: command
//	          command: "echo done"
//	---
func ParseFrontmatterHooks(content string) (*FrontmatterHooks, error) {
	frontmatter := extractFrontmatter(content)
	if frontmatter == "" {
		return nil, nil
	}

	var fm FrontmatterHooks
	if err := yaml.Unmarshal([]byte(frontmatter), &fm); err != nil {
		return nil, fmt.Errorf("failed to parse frontmatter YAML: %w", err)
	}

	if len(fm.Hooks) == 0 {
		return nil, nil
	}

	// Validate all hooks.
	if err := fm.Hooks.Validate(); err != nil {
		return nil, fmt.Errorf("invalid hook in frontmatter: %w", err)
	}

	return &fm, nil
}

// extractFrontmatter extracts the YAML frontmatter block from file content.
// Returns empty string if no frontmatter found.
func extractFrontmatter(content string) string {
	content = strings.TrimSpace(content)
	if !strings.HasPrefix(content, "---") {
		return ""
	}
	// Find the closing ---
	rest := content[3:]
	// Skip optional newline after opening ---
	if len(rest) > 0 && rest[0] == '\n' {
		rest = rest[1:]
	} else if len(rest) > 1 && rest[0] == '\r' && rest[1] == '\n' {
		rest = rest[2:]
	}

	endIdx := strings.Index(rest, "\n---")
	if endIdx < 0 {
		// Also try \r\n---
		endIdx = strings.Index(rest, "\r\n---")
		if endIdx < 0 {
			return ""
		}
	}
	return rest[:endIdx]
}

// RegisterFrontmatterHooks registers hooks from frontmatter (agent or skill)
// into the session hook registry. These hooks are active for the session duration
// and cleaned up when the session ends.
//
// If isAgent is true, Stop hooks are converted to SubagentStop since subagents
// trigger SubagentStop, not Stop.
func RegisterFrontmatterHooks(
	registry *SessionHookRegistry,
	sessionID string,
	hooks HooksSettings,
	sourceName string,
	isAgent bool,
	logger *slog.Logger,
) int {
	if len(hooks) == 0 {
		return 0
	}

	hookCount := 0
	for _, event := range AllHookEvents {
		matchers := hooks[event]
		if len(matchers) == 0 {
			continue
		}

		// For agents, convert Stop hooks to SubagentStop since that's what
		// fires when an agent completes.
		targetEvent := event
		if isAgent && event == EventStop {
			targetEvent = EventSubagentStop
			logger.Debug("converting Stop hook to SubagentStop for agent",
				"source", sourceName,
			)
		}

		for _, matcherConfig := range matchers {
			matcher := matcherConfig.Matcher
			for _, hook := range matcherConfig.Hooks {
				registry.AddSessionHook(sessionID, targetEvent, matcher, hook, nil, "")
				hookCount++
			}
		}
	}

	if hookCount > 0 {
		logger.Debug("registered frontmatter hooks",
			"count", hookCount,
			"source", sourceName,
			"session_id", sessionID,
		)
	}
	return hookCount
}

// RegisterSkillHooks registers hooks from a skill's frontmatter as session hooks.
// Hooks with once: true are automatically removed after first successful execution.
func RegisterSkillHooks(
	registry *SessionHookRegistry,
	sessionID string,
	hooks HooksSettings,
	skillName string,
	skillRoot string,
	logger *slog.Logger,
) int {
	registeredCount := 0

	for _, eventName := range AllHookEvents {
		matchers := hooks[eventName]
		if len(matchers) == 0 {
			continue
		}

		for _, matcher := range matchers {
			for _, hook := range matcher.Hooks {
				hookCopy := hook // capture for closure

				// For once: true hooks, use onHookSuccess callback to remove after execution.
				var onSuccess OnHookSuccess
				if hook.Once {
					onSuccess = func(_ interface{}, _ *AggregatedHookResult) {
						logger.Debug("removing one-shot hook",
							"event", eventName,
							"skill", skillName,
						)
						registry.RemoveSessionHook(sessionID, eventName, hookCopy)
					}
				}

				registry.AddSessionHook(
					sessionID,
					eventName,
					matcher.Matcher,
					hook,
					onSuccess,
					skillRoot,
				)
				registeredCount++
			}
		}
	}

	if registeredCount > 0 {
		logger.Debug("registered skill hooks",
			"count", registeredCount,
			"skill", skillName,
		)
	}
	return registeredCount
}

// DeregisterSkillHooks removes all hooks registered by a specific skill.
// This is called when a skill is unloaded.
func DeregisterSkillHooks(
	registry *SessionHookRegistry,
	sessionID string,
	skillRoot string,
	logger *slog.Logger,
) {
	registry.mu.Lock()
	defer registry.mu.Unlock()

	store := registry.sessions[sessionID]
	if store == nil {
		return
	}

	removedCount := 0
	for event, matchers := range store.Hooks {
		updatedMatchers := make([]SessionHookMatcher, 0, len(matchers))
		for _, m := range matchers {
			if m.SkillRoot == skillRoot {
				removedCount += len(m.Entries)
				continue
			}
			updatedMatchers = append(updatedMatchers, m)
		}
		if len(updatedMatchers) > 0 {
			store.Hooks[event] = updatedMatchers
		} else {
			delete(store.Hooks, event)
		}
	}

	if removedCount > 0 {
		logger.Debug("deregistered skill hooks",
			"count", removedCount,
			"skill_root", skillRoot,
			"session_id", sessionID,
		)
	}
}

// ParseFrontmatterAllowedTools extracts allowed_tools and disallowed_tools
// from CLAUDE.md frontmatter for use in permission hooks.
type FrontmatterToolPolicy struct {
	AllowedTools    []string `yaml:"allowed_tools"`
	DisallowedTools []string `yaml:"disallowed_tools"`
}

// ParseFrontmatterToolPolicy extracts tool allow/disallow lists from frontmatter.
func ParseFrontmatterToolPolicy(content string) (*FrontmatterToolPolicy, error) {
	frontmatter := extractFrontmatter(content)
	if frontmatter == "" {
		return nil, nil
	}

	var policy FrontmatterToolPolicy
	if err := yaml.Unmarshal([]byte(frontmatter), &policy); err != nil {
		return nil, fmt.Errorf("failed to parse frontmatter YAML: %w", err)
	}

	if len(policy.AllowedTools) == 0 && len(policy.DisallowedTools) == 0 {
		return nil, nil
	}
	return &policy, nil
}
