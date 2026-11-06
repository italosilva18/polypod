// Package argsubst provides variable substitution in strings.
//
// It replaces ${VAR}, ${VAR:default}, ${VAR:-fallback}, and ${VAR:+alternate}
// patterns, with support for nested references like ${VAR:${OTHER:fallback}}.
package argsubst

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

// SubstConfig controls how variable substitution behaves.
type SubstConfig struct {
	// Env is the primary source for variable values.
	Env map[string]string
	// FallbackToOS checks os.Getenv when a variable is not found in Env.
	FallbackToOS bool
	// UndefinedAction determines what happens when a variable is undefined
	// and has no default. Valid values: "keep" (leave ${VAR} as-is),
	// "empty" (replace with ""), "error" (return an error).
	UndefinedAction string
}

// ErrUndefinedVar is returned when UndefinedAction is "error" and a variable
// is referenced that has no value and no default.
var ErrUndefinedVar = errors.New("undefined variable")

// Substitute replaces all ${...} patterns in input according to cfg.
//
// Supported patterns:
//   - ${VAR}           — value of VAR
//   - ${VAR:default}   — value of VAR, or "default" if empty/unset
//   - ${VAR:-default}  — same (bash-style)
//   - ${VAR:+alternate} — "alternate" if VAR is set and non-empty, else ""
//   - ${VAR:${OTHER:fallback}} — nested substitution in defaults
func Substitute(input string, cfg SubstConfig) (string, error) {
	return substituteRec(input, cfg, 0)
}

const maxDepth = 16

func substituteRec(input string, cfg SubstConfig, depth int) (string, error) {
	if depth > maxDepth {
		return "", fmt.Errorf("argsubst: recursion depth exceeded (%d)", maxDepth)
	}

	var b strings.Builder
	b.Grow(len(input))

	i := 0
	for i < len(input) {
		// Look for "${"
		idx := strings.Index(input[i:], "${")
		if idx < 0 {
			b.WriteString(input[i:])
			break
		}
		b.WriteString(input[i : i+idx])
		i += idx // now pointing at '$'

		// Find matching '}', accounting for nesting.
		inner, end, ok := extractBraced(input, i+2) // skip "${"
		if !ok {
			// No matching '}' — keep literal text.
			b.WriteString("${")
			i += 2
			continue
		}

		resolved, err := resolveExpr(inner, cfg, depth)
		if err != nil {
			return "", err
		}
		b.WriteString(resolved)
		i = end
	}
	return b.String(), nil
}

// extractBraced finds the matching '}' for an opening "${", handling nested
// "${...}" groups. start points to the first character after "${".
// Returns (inner content, index after closing '}', ok).
func extractBraced(input string, start int) (string, int, bool) {
	depth := 1
	i := start
	for i < len(input) {
		switch {
		case input[i] == '}':
			depth--
			if depth == 0 {
				return input[start:i], i + 1, true
			}
			i++
		case i+1 < len(input) && input[i] == '$' && input[i+1] == '{':
			depth++
			i += 2
		default:
			i++
		}
	}
	return "", 0, false
}

// resolveExpr handles the content inside ${...}. It may contain a colon
// separator for default/alternate values.
func resolveExpr(expr string, cfg SubstConfig, depth int) (string, error) {
	// Find the first ':' that is not inside a nested ${...}.
	colonIdx := findTopLevelColon(expr)

	if colonIdx < 0 {
		// Simple ${VAR}.
		name := expr
		val, found := lookupVar(name, cfg)
		if found {
			return val, nil
		}
		return handleUndefined(name, cfg)
	}

	name := expr[:colonIdx]
	rest := expr[colonIdx+1:] // everything after the colon

	// Determine the operator: :-, :+, or plain :.
	switch {
	case len(rest) > 0 && rest[0] == '-':
		// ${VAR:-default} — use default if unset or empty.
		fallback := rest[1:]
		val, found := lookupVar(name, cfg)
		if found && val != "" {
			return val, nil
		}
		return substituteRec(fallback, cfg, depth+1)

	case len(rest) > 0 && rest[0] == '+':
		// ${VAR:+alternate} — use alternate if VAR is set and non-empty.
		alternate := rest[1:]
		val, found := lookupVar(name, cfg)
		if found && val != "" {
			return substituteRec(alternate, cfg, depth+1)
		}
		return "", nil

	default:
		// ${VAR:default} — use default if unset or empty (same as :-).
		val, found := lookupVar(name, cfg)
		if found && val != "" {
			return val, nil
		}
		return substituteRec(rest, cfg, depth+1)
	}
}

// findTopLevelColon returns the index of the first ':' in expr that is not
// nested inside a ${...} block, or -1 if none found.
func findTopLevelColon(expr string) int {
	depth := 0
	for i := 0; i < len(expr); i++ {
		switch {
		case i+1 < len(expr) && expr[i] == '$' && expr[i+1] == '{':
			depth++
			i++ // skip '{'
		case expr[i] == '}':
			if depth > 0 {
				depth--
			}
		case expr[i] == ':' && depth == 0:
			return i
		}
	}
	return -1
}

// lookupVar resolves a variable name from the config's Env map and
// optionally falls back to os.Getenv.
func lookupVar(name string, cfg SubstConfig) (string, bool) {
	if cfg.Env != nil {
		if v, ok := cfg.Env[name]; ok {
			return v, true
		}
	}
	if cfg.FallbackToOS {
		if v, ok := os.LookupEnv(name); ok {
			return v, true
		}
	}
	return "", false
}

// handleUndefined applies the UndefinedAction policy for a missing variable.
func handleUndefined(name string, cfg SubstConfig) (string, error) {
	switch cfg.UndefinedAction {
	case "error":
		return "", fmt.Errorf("%w: %s", ErrUndefinedVar, name)
	case "empty":
		return "", nil
	default: // "keep" or unset
		return "${" + name + "}", nil
	}
}

// SubstituteSimple is a simplified substitution that never returns errors.
// Undefined variables are kept as-is (${VAR} remains literally).
func SubstituteSimple(input string, env map[string]string) string {
	result, _ := Substitute(input, SubstConfig{
		Env:             env,
		FallbackToOS:    false,
		UndefinedAction: "keep",
	})
	return result
}

// ExtractVars returns a deduplicated list of variable names referenced in input.
// The order follows first occurrence. Nested references inside defaults are
// included as well.
func ExtractVars(input string) []string {
	seen := make(map[string]bool)
	var result []string
	extractVarsRec(input, seen, &result)
	return result
}

func extractVarsRec(input string, seen map[string]bool, result *[]string) {
	i := 0
	for i < len(input) {
		idx := strings.Index(input[i:], "${")
		if idx < 0 {
			break
		}
		i += idx
		inner, end, ok := extractBraced(input, i+2)
		if !ok {
			i += 2
			continue
		}

		// The variable name is everything before the first top-level colon.
		colonIdx := findTopLevelColon(inner)
		var name string
		if colonIdx < 0 {
			name = inner
		} else {
			name = inner[:colonIdx]
		}

		if name != "" && !seen[name] {
			seen[name] = true
			*result = append(*result, name)
		}

		// Recurse into the rest (default/alternate part) to find nested vars.
		if colonIdx >= 0 {
			rest := inner[colonIdx+1:]
			// Skip operator character (- or +) if present.
			if len(rest) > 0 && (rest[0] == '-' || rest[0] == '+') {
				rest = rest[1:]
			}
			extractVarsRec(rest, seen, result)
		}

		i = end
	}
}

// HasVars reports whether input contains any ${...} variable reference.
func HasVars(input string) bool {
	i := 0
	for i < len(input) {
		idx := strings.Index(input[i:], "${")
		if idx < 0 {
			return false
		}
		i += idx
		_, _, ok := extractBraced(input, i+2)
		if ok {
			return true
		}
		i += 2
	}
	return false
}
