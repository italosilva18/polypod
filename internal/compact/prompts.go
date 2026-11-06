package compact

import (
	"fmt"
	"strings"

	"github.com/costa/polypod/internal/conversation"
)

// Prompt constants for the compaction summarizer.

const noToolsPreamble = `CRITICAL: Respond with TEXT ONLY. Do NOT call any tools.

- Do NOT use Read, Bash, Grep, Glob, Edit, Write, or ANY other tool.
- You already have all the context you need in the conversation above.
- Tool calls will be REJECTED and will waste your only turn — you will fail the task.
- Your entire response must be plain text: an <analysis> block followed by a <summary> block.

`

const detailedAnalysisBase = `Before providing your final summary, wrap your analysis in <analysis> tags to organize your thoughts and ensure you've covered all necessary points. In your analysis process:

1. Chronologically analyze each message and section of the conversation. For each section thoroughly identify:
   - The user's explicit requests and intents
   - Your approach to addressing the user's requests
   - Key decisions, technical concepts and code patterns
   - Specific details like:
     - file names
     - full code snippets
     - function signatures
     - file edits
   - Errors that you ran into and how you fixed them
   - Pay special attention to specific user feedback that you received, especially if the user told you to do something differently.
2. Double-check for technical accuracy and completeness, addressing each required element thoroughly.`

const detailedAnalysisPartial = `Before providing your final summary, wrap your analysis in <analysis> tags to organize your thoughts and ensure you've covered all necessary points. In your analysis process:

1. Analyze the recent messages chronologically. For each section thoroughly identify:
   - The user's explicit requests and intents
   - Your approach to addressing the user's requests
   - Key decisions, technical concepts and code patterns
   - Specific details like:
     - file names
     - full code snippets
     - function signatures
     - file edits
   - Errors that you ran into and how you fixed them
   - Pay special attention to specific user feedback that you received, especially if the user told you to do something differently.
2. Double-check for technical accuracy and completeness, addressing each required element thoroughly.`

const baseCompactPrompt = `Your task is to create a detailed summary of the conversation so far, paying close attention to the user's explicit requests and your previous actions.
This summary should be thorough in capturing technical details, code patterns, and architectural decisions that would be essential for continuing development work without losing context.

%s

Your summary should include the following sections:

1. Primary Request and Intent: Capture all of the user's explicit requests and intents in detail
2. Key Technical Concepts: List all important technical concepts, technologies, and frameworks discussed.
3. Files and Code Sections: Enumerate specific files and code sections examined, modified, or created. Pay special attention to the most recent messages and include full code snippets where applicable and include a summary of why this file read or edit is important.
4. Errors and fixes: List all errors that you ran into, and how you fixed them. Pay special attention to specific user feedback that you received, especially if the user told you to do something differently.
5. Problem Solving: Document problems solved and any ongoing troubleshooting efforts.
6. All user messages: List ALL user messages that are not tool results. These are critical for understanding the users' feedback and changing intent.
7. Pending Tasks: Outline any pending tasks that you have explicitly been asked to work on.
8. Current Work: Describe in detail precisely what was being worked on immediately before this summary request, paying special attention to the most recent messages from both user and assistant. Include file names and code snippets where applicable.
9. Optional Next Step: List the next step that you will take that is related to the most recent work you were doing. IMPORTANT: ensure that this step is DIRECTLY in line with the user's most recent explicit requests, and the task you were working on immediately before this summary request. If your last task was concluded, then only list next steps if they are explicitly in line with the users request. Do not start on tangential requests or really old requests that were already completed without confirming with the user first.
                       If there is a next step, include direct quotes from the most recent conversation showing exactly what task you were working on and where you left off. This should be verbatim to ensure there's no drift in task interpretation.

Here's an example of how your output should be structured:

<example>
<analysis>
[Your thought process, ensuring all points are covered thoroughly and accurately]
</analysis>

<summary>
1. Primary Request and Intent:
   [Detailed description]

2. Key Technical Concepts:
   - [Concept 1]
   - [Concept 2]
   - [...]

3. Files and Code Sections:
   - [File Name 1]
      - [Summary of why this file is important]
      - [Summary of the changes made to this file, if any]
      - [Important Code Snippet]
   - [File Name 2]
      - [Important Code Snippet]
   - [...]

4. Errors and fixes:
    - [Detailed description of error 1]:
      - [How you fixed the error]
      - [User feedback on the error if any]
    - [...]

5. Problem Solving:
   [Description of solved problems and ongoing troubleshooting]

6. All user messages:
    - [Detailed non tool use user message]
    - [...]

7. Pending Tasks:
   - [Task 1]
   - [Task 2]
   - [...]

8. Current Work:
   [Precise description of current work]

9. Optional Next Step:
   [Optional Next step to take]

</summary>
</example>

Please provide your summary based on the conversation so far, following this structure and ensuring precision and thoroughness in your response.

There may be additional summarization instructions provided in the included context. If so, remember to follow these instructions when creating the above summary. Examples of instructions include:
<example>
## Compact Instructions
When summarizing the conversation focus on typescript code changes and also remember the mistakes you made and how you fixed them.
</example>

<example>
# Summary instructions
When you are using compact - please focus on test output and code changes. Include file reads verbatim.
</example>
`

const partialCompactFromPrompt = `Your task is to create a detailed summary of the RECENT portion of the conversation — the messages that follow earlier retained context. The earlier messages are being kept intact and do NOT need to be summarized. Focus your summary on what was discussed, learned, and accomplished in the recent messages only.

%s

Your summary should include the following sections:

1. Primary Request and Intent: Capture the user's explicit requests and intents from the recent messages
2. Key Technical Concepts: List important technical concepts, technologies, and frameworks discussed recently.
3. Files and Code Sections: Enumerate specific files and code sections examined, modified, or created. Include full code snippets where applicable and include a summary of why this file read or edit is important.
4. Errors and fixes: List errors encountered and how they were fixed.
5. Problem Solving: Document problems solved and any ongoing troubleshooting efforts.
6. All user messages: List ALL user messages from the recent portion that are not tool results.
7. Pending Tasks: Outline any pending tasks from the recent messages.
8. Current Work: Describe precisely what was being worked on immediately before this summary request.
9. Optional Next Step: List the next step related to the most recent work. Include direct quotes from the most recent conversation.

Please provide your summary based on the RECENT messages only (after the retained earlier context), following this structure and ensuring precision and thoroughness in your response.
`

const partialCompactUpToPrompt = `Your task is to create a detailed summary of this conversation. This summary will be placed at the start of a continuing session; newer messages that build on this context will follow after your summary (you do not see them here). Summarize thoroughly so that someone reading only your summary and then the newer messages can fully understand what happened and continue the work.

%s

Your summary should include the following sections:

1. Primary Request and Intent: Capture the user's explicit requests and intents in detail
2. Key Technical Concepts: List important technical concepts, technologies, and frameworks discussed.
3. Files and Code Sections: Enumerate specific files and code sections examined, modified, or created. Include full code snippets where applicable and include a summary of why this file read or edit is important.
4. Errors and fixes: List errors encountered and how they were fixed.
5. Problem Solving: Document problems solved and any ongoing troubleshooting efforts.
6. All user messages: List ALL user messages that are not tool results.
7. Pending Tasks: Outline any pending tasks.
8. Work Completed: Describe what was accomplished by the end of this portion.
9. Context for Continuing Work: Summarize any context, decisions, or state that would be needed to understand and continue the work in subsequent messages.

Please provide your summary following this structure, ensuring precision and thoroughness in your response.
`

const noToolsTrailer = "\n\nREMINDER: Do NOT call any tools. Respond with plain text only — " +
	"an <analysis> block followed by a <summary> block. " +
	"Tool calls will be rejected and you will fail the task."

// GetCompactPrompt builds the full compaction prompt for a complete conversation
// summarization. If customInstructions is non-empty, it is appended to guide
// the summarizer.
func GetCompactPrompt(customInstructions string) string {
	prompt := noToolsPreamble + fmt.Sprintf(baseCompactPrompt, detailedAnalysisBase)

	if customInstructions = strings.TrimSpace(customInstructions); customInstructions != "" {
		prompt += "\n\nAdditional Instructions:\n" + customInstructions
	}

	prompt += noToolsTrailer
	return prompt
}

// GetPartialCompactPrompt builds the prompt for partial compaction.
// Direction controls which template is used:
//   - "from": summarize recent messages (after retained context)
//   - "up_to": summarize earlier messages (before retained context)
func GetPartialCompactPrompt(customInstructions string, direction string) string {
	var template string
	switch direction {
	case "up_to":
		template = fmt.Sprintf(partialCompactUpToPrompt, detailedAnalysisBase)
	default: // "from"
		template = fmt.Sprintf(partialCompactFromPrompt, detailedAnalysisPartial)
	}

	prompt := noToolsPreamble + template

	if customInstructions = strings.TrimSpace(customInstructions); customInstructions != "" {
		prompt += "\n\nAdditional Instructions:\n" + customInstructions
	}

	prompt += noToolsTrailer
	return prompt
}

// FormatMessagesForSummarization converts a message slice into a text format
// suitable for the summarization prompt. Each message is prefixed with its role
// and separated by blank lines.
func FormatMessagesForSummarization(messages []conversation.Message) string {
	var sb strings.Builder

	for _, msg := range messages {
		role := string(msg.Role)
		content := msg.Content

		// Truncate extremely long messages to avoid overwhelming the summarizer
		if len(content) > 50_000 {
			content = content[:50_000] + "\n\n[... message truncated for summarization]"
		}

		fmt.Fprintf(&sb, "[%s]: %s\n\n", role, content)
	}

	return sb.String()
}

// ExtractKeyFacts extracts structured key facts from a conversation summary.
// It parses the numbered sections from a formatted summary and returns them
// as a map of section name to content.
func ExtractKeyFacts(summary string) map[string]string {
	facts := make(map[string]string)

	sections := []struct {
		num  int
		name string
	}{
		{1, "Primary Request and Intent"},
		{2, "Key Technical Concepts"},
		{3, "Files and Code Sections"},
		{4, "Errors and fixes"},
		{5, "Problem Solving"},
		{6, "All user messages"},
		{7, "Pending Tasks"},
		{8, "Current Work"},
		{9, "Optional Next Step"},
	}

	for i, section := range sections {
		prefix := fmt.Sprintf("%d. %s:", section.num, section.name)
		idx := strings.Index(summary, prefix)
		if idx == -1 {
			// Try without the colon
			prefix = fmt.Sprintf("%d. %s", section.num, section.name)
			idx = strings.Index(summary, prefix)
		}
		if idx == -1 {
			continue
		}

		// Find the end of this section (start of next section or end of string)
		start := idx + len(prefix)
		var end int
		if i+1 < len(sections) {
			nextPrefix := fmt.Sprintf("%d.", sections[i+1].num)
			nextIdx := strings.Index(summary[start:], nextPrefix)
			if nextIdx != -1 {
				end = start + nextIdx
			} else {
				end = len(summary)
			}
		} else {
			end = len(summary)
		}

		content := strings.TrimSpace(summary[start:end])
		if content != "" {
			facts[section.name] = content
		}
	}

	return facts
}

// BuildCompactSummaryMessage creates the user-facing summary message that
// replaces the compacted portion of the conversation.
func BuildCompactSummaryMessage(
	summary string,
	suppressFollowUp bool,
	transcriptPath string,
) string {
	formatted := FormatCompactSummary(summary)

	base := fmt.Sprintf(
		"This session is being continued from a previous conversation that ran out of context. The summary below covers the earlier portion of the conversation.\n\n%s",
		formatted,
	)

	if transcriptPath != "" {
		base += fmt.Sprintf(
			"\n\nIf you need specific details from before compaction (like exact code snippets, error messages, or content you generated), read the full transcript at: %s",
			transcriptPath,
		)
	}

	if suppressFollowUp {
		base += "\nContinue the conversation from where it left off without asking the user any further questions. Resume directly — do not acknowledge the summary, do not recap what was happening, do not preface with \"I'll continue\" or similar. Pick up the last task as if the break never happened."
	}

	return base
}
