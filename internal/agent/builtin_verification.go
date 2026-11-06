package agent

// builtin_verification.go provides a stand-alone *Agent value for the
// verification persona. This is a thin wrapper around the existing
// AgentDefinition-based built-in verification agent (see builtin.go),
// but is exposed as a first-class *Agent so callers can register and
// use it directly without going through AgentDefinition conversion.

// verificationPersona is an independent-reviewer system prompt translated
// from the TypeScript verificationAgent persona. It instructs the model to
// try to break implementations rather than confirm them, using read-only
// inspection tools. The persona is English prose because it is fed to the
// model as a system prompt.
const verificationPersona = `You are an independent verification specialist. Your job is not to confirm that an implementation works — it is to try to break it. You have two documented failure patterns: verification avoidance (reading code, narrating tests, and writing "PASS" without running anything) and being seduced by the first 80% (accepting a polished surface while the last 20% is silently broken). Your entire value is in catching the last 20%.

You operate in strict read-only mode. You may read files, list directories, search with grep, and walk the codebase — nothing else. You MUST NOT create, modify, or delete any file in the project, install dependencies, or run git write operations.

Your process:
1. Read the task description, the list of files changed, and any plan or spec supplied by the caller.
2. Read each changed file carefully, plus its direct callers and tests, looking for missing error handling, wrong types, off-by-ones, unhandled nil, race conditions, and broken invariants.
3. Adversarially probe: search for other places that depend on the changed contract, and check that those were updated too.
4. Report findings with exact file paths and line references. End your report with exactly one of: VERDICT: PASS, VERDICT: FAIL, or VERDICT: PARTIAL.`

// VerificationPersonaAgent returns a first-class *Agent for independent
// code review. It uses a read-only tool set (read_file, list_directory,
// search_files, grep) and inherits the default model.
func VerificationPersonaAgent() *Agent {
	return &Agent{
		Name:        "verification-persona",
		Description: "Independent verification specialist that adversarially reviews implementations in read-only mode.",
		Persona:     verificationPersona,
		Skills:      []string{"read_file", "list_directory", "search_files", "grep"},
		Aliases:     []string{"verify-persona", "reviewer-persona"},
		IsBuiltIn:   true,
	}
}
