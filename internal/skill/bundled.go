package skill

import (
	"fmt"
	"strings"
)

// BundledSkill defines a built-in skill that ships with the binary.
// Migrated from Claude Code TypeScript source.
// Source: skills/bundled/*.ts, skills/bundledSkills.ts
type BundledSkill struct {
	Name                   string
	Description            string
	Aliases                []string
	WhenToUse              string
	ArgumentHint           string
	AllowedTools           []string
	DisableModelInvocation bool
	UserInvocable          bool
	// GetPrompt returns the skill prompt given user arguments.
	GetPrompt func(args string) string
}

// bundledSkills is the internal registry of bundled skills.
var bundledSkills []*BundledSkill

// RegisterBundledSkill adds a bundled skill to the internal registry.
func RegisterBundledSkill(s *BundledSkill) {
	bundledSkills = append(bundledSkills, s)
}

// GetBundledSkills returns a copy of all registered bundled skills.
func GetBundledSkills() []*BundledSkill {
	result := make([]*BundledSkill, len(bundledSkills))
	copy(result, bundledSkills)
	return result
}

// ClearBundledSkills clears the bundled skills registry (for testing).
func ClearBundledSkills() {
	bundledSkills = nil
}

// RegisterBundledSkills initializes all bundled skills that ship with the binary.
// Called at startup.
func RegisterBundledSkills() {
	registerVerifySkill()
	registerDebugSkill()
	registerBatchSkill()
	registerLoopSkill()
	registerStuckSkill()
	registerSimplifySkill()
	registerSkillifySkill()
	registerRememberSkill()
}

// ---------------------------------------------------------------------------
// Verify Skill — verify code changes (run tests, lint)
// ---------------------------------------------------------------------------

func registerVerifySkill() {
	RegisterBundledSkill(&BundledSkill{
		Name:          "verify",
		Description:   "Verify a code change does what it should by running the app.",
		UserInvocable: true,
		GetPrompt: func(args string) string {
			prompt := `# Verify Skill

Verify the current code change works correctly.

## Steps

### 1. Identify Changed Files
Run ` + "`git diff --name-only`" + ` to find modified files. If no git changes, check the most recently edited files.

### 2. Determine Test Strategy
Based on the changes, determine:
- Unit tests to run (look for test files adjacent to changed files)
- Integration tests if applicable
- Linting/type checking commands

### 3. Run Verification
Execute tests and linting. For each:
- Run the test command
- Report pass/fail with relevant output
- If tests fail, investigate and suggest fixes

### 4. Report
Summarize what was verified and the results.
`
			if args != "" {
				prompt += fmt.Sprintf("\n## User Request\n\n%s", args)
			}
			return prompt
		},
	})
}

// ---------------------------------------------------------------------------
// Debug Skill — debug issues systematically
// ---------------------------------------------------------------------------

func registerDebugSkill() {
	RegisterBundledSkill(&BundledSkill{
		Name:                   "debug",
		Description:            "Enable debug logging for this session and help diagnose issues.",
		AllowedTools:           []string{"Read", "Grep", "Glob"},
		ArgumentHint:           "[issue description]",
		DisableModelInvocation: true,
		UserInvocable:          true,
		GetPrompt: func(args string) string {
			issueDesc := args
			if issueDesc == "" {
				issueDesc = "The user did not describe a specific issue. Read the debug log and summarize any errors, warnings, or notable issues."
			}
			return fmt.Sprintf(`# Debug Skill

Help the user debug an issue they're encountering.

## Issue Description

%s

## Instructions

1. Review the user's issue description
2. Look for [ERROR] and [WARN] entries, stack traces, and failure patterns
3. Explain what you found in plain language
4. Suggest concrete fixes or next steps
`, issueDesc)
		},
	})
}

// ---------------------------------------------------------------------------
// Batch Skill — batch process multiple items in parallel
// ---------------------------------------------------------------------------

func registerBatchSkill() {
	RegisterBundledSkill(&BundledSkill{
		Name:                   "batch",
		Description:            "Research and plan a large-scale change, then execute it in parallel across 5-30 isolated worktree agents that each open a PR.",
		WhenToUse:              "Use when the user wants to make a sweeping, mechanical change across many files (migrations, refactors, bulk renames) that can be decomposed into independent parallel units.",
		ArgumentHint:           "<instruction>",
		UserInvocable:          true,
		DisableModelInvocation: true,
		GetPrompt: func(args string) string {
			instruction := strings.TrimSpace(args)
			if instruction == "" {
				return `Provide an instruction describing the batch change you want to make.

Examples:
  /batch migrate from react to vue
  /batch replace all uses of lodash with native equivalents
  /batch add type annotations to all untyped function parameters`
			}
			return fmt.Sprintf(`# Batch: Parallel Work Orchestration

You are orchestrating a large, parallelizable change across this codebase.

## User Instruction

%s

## Phase 1: Research and Plan

1. **Understand the scope.** Launch subagents to deeply research what this instruction touches.
2. **Decompose into independent units.** Break the work into 5-30 self-contained units.
3. **Determine the e2e test recipe.** Figure out verification for each unit.
4. **Write the plan.** Include summary, numbered work units, e2e recipe, and worker instructions.

## Phase 2: Spawn Workers

Once the plan is approved, spawn one background agent per work unit.
All agents must use isolation and run in the background. Launch them all in parallel.

For each agent, the prompt must be fully self-contained with:
- The overall goal
- This unit's specific task
- Codebase conventions to follow
- The e2e test recipe
- Worker instructions (implement, simplify, test, commit, push, create PR)

## Phase 3: Track Progress

Render a status table and update as agents complete.
When all agents report, render the final table and summary.
`, instruction)
		},
	})
}

// ---------------------------------------------------------------------------
// Loop Skill — iterative task execution
// ---------------------------------------------------------------------------

func registerLoopSkill() {
	RegisterBundledSkill(&BundledSkill{
		Name:          "loop",
		Description:   "Run a prompt or slash command on a recurring interval (e.g. /loop 5m /foo, defaults to 10m).",
		WhenToUse:     "When the user wants to set up a recurring task, poll for status, or run something repeatedly on an interval.",
		ArgumentHint:  "[interval] <prompt>",
		UserInvocable: true,
		GetPrompt: func(args string) string {
			trimmed := strings.TrimSpace(args)
			if trimmed == "" {
				return `Usage: /loop [interval] <prompt>

Run a prompt or slash command on a recurring interval.

Intervals: Ns, Nm, Nh, Nd (e.g. 5m, 30m, 2h, 1d). Minimum granularity is 1 minute.
If no interval is specified, defaults to 10m.

Examples:
  /loop 5m /babysit-prs
  /loop 30m check the deploy
  /loop 1h /standup 1
  /loop check the deploy          (defaults to 10m)
  /loop check the deploy every 20m`
			}
			return fmt.Sprintf(`# /loop — schedule a recurring prompt

Parse the input below into [interval] <prompt> and schedule it.

## Parsing (in priority order)

1. **Leading token**: if the first token matches N[smhd] (e.g. 5m, 2h), that's the interval.
2. **Trailing "every" clause**: if input ends with "every <N><unit>", extract that as interval.
3. **Default**: interval is 10m, entire input is the prompt.

## Action

1. Schedule the recurring task with the parsed interval and prompt.
2. Confirm what's scheduled, the interval, and how to cancel.
3. Execute the parsed prompt immediately — don't wait for the first scheduled fire.

## Input

%s`, trimmed)
		},
	})
}

// ---------------------------------------------------------------------------
// Stuck Skill — recovery when stuck
// ---------------------------------------------------------------------------

func registerStuckSkill() {
	RegisterBundledSkill(&BundledSkill{
		Name:          "stuck",
		Description:   "Investigate frozen/stuck/slow sessions on this machine and produce a diagnostic report.",
		UserInvocable: true,
		GetPrompt: func(args string) string {
			prompt := `# /stuck — diagnose frozen/slow sessions

The user thinks a session is frozen, stuck, or very slow. Investigate and report.

## What to look for

Scan for other processes. Signs of a stuck session:
- **High CPU (>=90%%) sustained** — likely an infinite loop
- **Process state D (uninterruptible sleep)** — often an I/O hang
- **Process state T (stopped)** — user probably hit Ctrl+Z
- **Process state Z (zombie)** — parent isn't reaping
- **Very high RSS (>=4GB)** — possible memory leak

## Investigation steps

1. **List all related processes** with ps
2. **For anything suspicious**, gather more context (child processes, sustained CPU check)
3. **Consider a stack dump** for truly frozen processes

## Report

If every session looks healthy, tell the user directly.
If you found a stuck/slow session, provide a detailed diagnostic with:
- PID, CPU%%, RSS, state, uptime, command line, child processes
- Your diagnosis of what's likely wrong
- Suggested remediation

## Notes
- Don't kill or signal any processes — this is diagnostic only.
`
			if args != "" {
				prompt += fmt.Sprintf("\n## User-provided context\n\n%s\n", args)
			}
			return prompt
		},
	})
}

// ---------------------------------------------------------------------------
// Simplify Skill — simplify complex code
// ---------------------------------------------------------------------------

func registerSimplifySkill() {
	RegisterBundledSkill(&BundledSkill{
		Name:          "simplify",
		Description:   "Review changed code for reuse, quality, and efficiency, then fix any issues found.",
		UserInvocable: true,
		GetPrompt: func(args string) string {
			prompt := `# Simplify: Code Review and Cleanup

Review all changed files for reuse, quality, and efficiency. Fix any issues found.

## Phase 1: Identify Changes

Run ` + "`git diff`" + ` (or ` + "`git diff HEAD`" + ` if there are staged changes) to see what changed.

## Phase 2: Launch Three Review Agents in Parallel

### Agent 1: Code Reuse Review
- Search for existing utilities that could replace newly written code
- Flag new functions that duplicate existing functionality
- Flag inline logic that could use an existing utility

### Agent 2: Code Quality Review
- Redundant state, parameter sprawl, copy-paste with slight variation
- Leaky abstractions, stringly-typed code
- Unnecessary nesting, unnecessary comments

### Agent 3: Efficiency Review
- Unnecessary work, missed concurrency, hot-path bloat
- Recurring no-op updates, unnecessary existence checks
- Memory leaks, overly broad operations

## Phase 3: Fix Issues

Aggregate findings and fix each issue directly. Skip false positives.
Summarize what was fixed (or confirm the code was already clean).
`
			if args != "" {
				prompt += fmt.Sprintf("\n\n## Additional Focus\n\n%s", args)
			}
			return prompt
		},
	})
}

// ---------------------------------------------------------------------------
// Skillify Skill — create new skills from patterns
// ---------------------------------------------------------------------------

func registerSkillifySkill() {
	RegisterBundledSkill(&BundledSkill{
		Name:                   "skillify",
		Description:            "Capture this session's repeatable process into a skill. Call at end of the process you want to capture with an optional description.",
		AllowedTools:           []string{"Read", "Write", "Edit", "Glob", "Grep", "AskUserQuestion"},
		UserInvocable:          true,
		DisableModelInvocation: true,
		ArgumentHint:           "[description of the process you want to capture]",
		GetPrompt: func(args string) string {
			userDesc := ""
			if args != "" {
				userDesc = fmt.Sprintf(`The user described this process as: "%s"`, args)
			}
			return fmt.Sprintf(`# Skillify %s

You are capturing this session's repeatable process as a reusable skill.

## Your Task

### Step 1: Analyze the Session

Identify:
- What repeatable process was performed
- The distinct steps (in order)
- The success criteria for each step
- What tools and permissions were needed
- What the goals and success artifacts were

### Step 2: Interview the User

Use AskUserQuestion to understand what the user wants to automate:

**Round 1**: Suggest a name, description, high-level goals and success criteria.
**Round 2**: Present high-level steps. Ask about arguments and where to save.
**Round 3**: For each step, ask about success criteria, execution method, dependencies, human checkpoints.
**Round 4**: Confirm trigger phrases and any gotchas.

### Step 3: Write the SKILL.md

Create the skill file with frontmatter (name, description, allowed-tools, when_to_use, arguments) and step-by-step instructions.

### Step 4: Confirm and Save

Show the SKILL.md content for review, then save after confirmation.
`, userDesc)
		},
	})
}

// ---------------------------------------------------------------------------
// Remember Skill — save to memory
// ---------------------------------------------------------------------------

func registerRememberSkill() {
	RegisterBundledSkill(&BundledSkill{
		Name:          "remember",
		Description:   "Review auto-memory entries and propose promotions to config files or shared memory. Also detects outdated, conflicting, and duplicate entries across memory layers.",
		WhenToUse:     "Use when the user wants to review, organize, or promote their auto-memory entries.",
		UserInvocable: true,
		GetPrompt: func(args string) string {
			prompt := `# Memory Review

## Goal
Review the user's memory landscape and produce a clear report of proposed changes, grouped by action type. Do NOT apply changes — present proposals for user approval.

## Steps

### 1. Gather all memory layers
Read config files from the project root. Review auto-memory content from your system prompt.

### 2. Classify each auto-memory entry
Determine the best destination for each:
- **Project config** — project conventions all contributors should follow
- **Local config** — personal instructions specific to this user
- **Team memory** — org-wide knowledge (if team memory is configured)
- **Stay in auto-memory** — working notes, temporary context

### 3. Identify cleanup opportunities
Scan across all layers for:
- **Duplicates** — entries already captured elsewhere → propose removing
- **Outdated** — entries contradicted by newer entries → propose updating
- **Conflicts** — contradictions between layers → propose resolution

### 4. Present the report
Output grouped by action type:
1. **Promotions** — entries to move, with destination and rationale
2. **Cleanup** — duplicates, outdated, conflicts to resolve
3. **Ambiguous** — entries needing user input on destination
4. **No action needed** — entries that should stay put

## Rules
- Present ALL proposals before making any changes
- Do NOT modify files without explicit user approval
- Ask about ambiguous entries — don't guess
`
			if args != "" {
				prompt += fmt.Sprintf("\n## Additional context from user\n\n%s", args)
			}
			return prompt
		},
	})
}
