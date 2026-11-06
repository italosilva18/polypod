package skill

import (
	"fmt"
	"strings"

	"github.com/sashabaranov/go-openai/jsonschema"
)

// newCodeReviewSkill returns the bundled code-review skill.
//
// The skill builds a structured prompt template for reviewing a snippet of
// code. It does not call any AI itself; the caller is expected to feed the
// returned prompt to the model. Inputs:
//
//	code      (required) - the code to review
//	language  (optional) - programming language for syntax highlighting
//	file_path (optional) - file path for additional context
func newCodeReviewSkill() *Skill {
	return &Skill{
		Name:        "code-review",
		Description: "Review code for quality, bugs, security, performance, and best practices.",
		Parameters: jsonschema.Definition{
			Type: jsonschema.Object,
			Properties: map[string]jsonschema.Definition{
				"code": {
					Type:        jsonschema.String,
					Description: "The code to review",
				},
				"language": {
					Type:        jsonschema.String,
					Description: "Programming language (optional, for syntax highlighting)",
				},
				"file_path": {
					Type:        jsonschema.String,
					Description: "Optional file path for context",
				},
			},
			Required: []string{"code"},
		},
		Execute: func(args map[string]string) (string, error) {
			code := args["code"]
			language := args["language"]
			filePath := args["file_path"]

			if code == "" {
				return "", fmt.Errorf("code is required")
			}

			var sb strings.Builder
			sb.WriteString("# Code Review\n\n")
			sb.WriteString("Please review the following code for:\n\n")

			sb.WriteString("## Quality Issues\n")
			sb.WriteString("- Code smells (long methods, large classes, duplication)\n")
			sb.WriteString("- Naming issues (unclear, inconsistent, misleading names)\n")
			sb.WriteString("- Complexity (deeply nested logic, too many branches)\n")
			sb.WriteString("- Missing abstractions or wrong level of abstraction\n\n")

			sb.WriteString("## Potential Bugs\n")
			sb.WriteString("- Logic errors\n")
			sb.WriteString("- Off-by-one errors\n")
			sb.WriteString("- Null pointer / nil dereference\n")
			sb.WriteString("- Race conditions, deadlocks\n")
			sb.WriteString("- Error handling issues (ignored errors, wrong error types)\n")
			sb.WriteString("- Resource leaks (unclosed files, connections)\n\n")

			sb.WriteString("## Security\n")
			sb.WriteString("- Input validation\n")
			sb.WriteString("- Injection vulnerabilities (SQL, command, path)\n")
			sb.WriteString("- Weak crypto\n")
			sb.WriteString("- Hardcoded secrets\n\n")

			sb.WriteString("## Performance\n")
			sb.WriteString("- Unnecessary allocations\n")
			sb.WriteString("- O(n^2) loops that could be O(n)\n")
			sb.WriteString("- Missing early returns\n")
			sb.WriteString("- Inefficient data structures\n\n")

			sb.WriteString("## Best Practices\n")
			sb.WriteString("- Missing documentation\n")
			sb.WriteString("- Missing tests\n")
			sb.WriteString("- Style inconsistencies\n")
			sb.WriteString("- Idiomatic language usage\n\n")

			sb.WriteString("## Output Format\n")
			sb.WriteString("Organize findings by severity: CRITICAL, HIGH, MEDIUM, LOW, INFO.\n")
			sb.WriteString("For each finding provide:\n")
			sb.WriteString("- **Location**: file:line\n")
			sb.WriteString("- **Issue**: brief description\n")
			sb.WriteString("- **Explanation**: why it matters\n")
			sb.WriteString("- **Suggestion**: how to fix\n\n")

			if filePath != "" {
				sb.WriteString(fmt.Sprintf("---\n\n**File**: `%s`\n\n", filePath))
			} else {
				sb.WriteString("---\n\n")
			}

			if language == "" {
				language = "auto"
			}
			sb.WriteString(fmt.Sprintf("```%s\n%s\n```\n", language, code))

			return sb.String(), nil
		},
	}
}
