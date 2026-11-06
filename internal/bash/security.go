package bash

import (
	"regexp"
	"strings"
)

// SecurityCheck is the result of a security validation.
type SecurityCheck struct {
	Safe         bool
	Message      string
	IsMisparsing bool // true if this is a parser-differential concern
}

// ValidateCommandSecurity runs all security checks on a bash command.
// Migrated from Claude Code's bashSecurity.ts
func ValidateCommandSecurity(command string) SecurityCheck {
	checks := []func(string) SecurityCheck{
		validateCommandSubstitution,
		validateObfuscatedFlags,
		validateShellMetacharacters,
		validateDangerousPatterns,
		validateRedirections,
		validateNewlines,
		validateIFSInjection,
		validateProcEnvironAccess,
		validateBackslashEscapedWhitespace,
		validateBraceExpansion,
		validateUnicodeWhitespace,
		validateMidWordHash,
		validateCarriageReturn,
	}

	for _, check := range checks {
		if result := check(command); !result.Safe {
			return result
		}
	}
	return SecurityCheck{Safe: true}
}

var (
	cmdSubPatterns = []*regexp.Regexp{
		regexp.MustCompile(`\$\(`), // $()
		regexp.MustCompile("``"),   // ``
		regexp.MustCompile(`\$\{`), // ${}
		regexp.MustCompile(`<\(`),  // <()
		regexp.MustCompile(`>\(`),  // >()
	}
)

func validateCommandSubstitution(cmd string) SecurityCheck {
	// Allow heredoc patterns: $(cat <<'DELIM'...DELIM)
	if isHeredocPattern(cmd) {
		return SecurityCheck{Safe: true}
	}
	for _, p := range cmdSubPatterns {
		if p.MatchString(cmd) {
			return SecurityCheck{
				Safe:    false,
				Message: "substituicao de comando detectada ($(), ``, ${}, <(), >())",
			}
		}
	}
	return SecurityCheck{Safe: true}
}

func isHeredocPattern(cmd string) bool {
	// Allow $(cat <<'DELIM'...DELIM) patterns
	return strings.Contains(cmd, "<<'") || strings.Contains(cmd, `<<\EOF`)
}

var (
	obfuscatedPatterns = []*regexp.Regexp{
		regexp.MustCompile(`\$'[^']*'`), // ANSI-C quoting
		regexp.MustCompile(`\$"[^"]*"`), // locale quoting
		regexp.MustCompile(`""-`),       // empty quote + flag
		regexp.MustCompile(`''-`),       // empty quote + flag
		regexp.MustCompile(`["']{3,}`),  // triple+ quotes
	}
)

func validateObfuscatedFlags(cmd string) SecurityCheck {
	for _, p := range obfuscatedPatterns {
		if p.MatchString(cmd) {
			return SecurityCheck{
				Safe:    false,
				Message: "obfuscacao de comando detectada (aspas ANSI-C, locale, ou aspas vazias adjacentes a flags)",
			}
		}
	}
	return SecurityCheck{Safe: true}
}

func validateShellMetacharacters(cmd string) SecurityCheck {
	// Check for unquoted ; | &
	inSingleQuote := false
	inDoubleQuote := false
	for i := 0; i < len(cmd); i++ {
		ch := cmd[i]
		switch {
		case ch == '\'' && !inDoubleQuote:
			inSingleQuote = !inSingleQuote
		case ch == '"' && !inSingleQuote:
			inDoubleQuote = !inDoubleQuote
		case !inSingleQuote && !inDoubleQuote:
			if ch == ';' || ch == '|' || (ch == '&' && (i+1 >= len(cmd) || cmd[i+1] != '&')) {
				return SecurityCheck{
					Safe:    false,
					Message: "metacaractere shell detectado (;, |, &)",
				}
			}
		}
	}
	return SecurityCheck{Safe: true}
}

var (
	dangerousPatterns = []*regexp.Regexp{
		regexp.MustCompile(`LD_PRELOAD`),
		regexp.MustCompile(`PYTHONPATH`),
		regexp.MustCompile(`/proc/self/`),
		regexp.MustCompile(`/dev/tcp/`),
		regexp.MustCompile(`/dev/udp/`),
	}
)

func validateDangerousPatterns(cmd string) SecurityCheck {
	for _, p := range dangerousPatterns {
		if p.MatchString(cmd) {
			return SecurityCheck{
				Safe:    false,
				Message: "padrao perigoso detectado no comando",
			}
		}
	}
	return SecurityCheck{Safe: true}
}

func validateRedirections(cmd string) SecurityCheck {
	// Block input redirection (<) which can read from unexpected sources
	inQuote := false
	for i := 0; i < len(cmd); i++ {
		ch := cmd[i]
		if ch == '\'' || ch == '"' {
			inQuote = !inQuote
		}
		if !inQuote && ch == '<' && (i+1 >= len(cmd) || cmd[i+1] != '<') {
			return SecurityCheck{
				Safe:    false,
				Message: "redirecionamento de entrada (<) detectado",
			}
		}
	}
	return SecurityCheck{Safe: true}
}

func validateNewlines(cmd string) SecurityCheck {
	lines := strings.Split(cmd, "\n")
	if len(lines) <= 1 {
		return SecurityCheck{Safe: true}
	}
	for i, line := range lines {
		if i == 0 {
			continue
		}
		trimmed := strings.TrimSpace(line)
		if trimmed != "" && !strings.HasPrefix(trimmed, "#") {
			// Check if previous line ends with backslash (continuation)
			prevLine := lines[i-1]
			if !strings.HasSuffix(prevLine, "\\") {
				return SecurityCheck{
					Safe:    false,
					Message: "nova linha detectada como separador de comando",
				}
			}
		}
	}
	return SecurityCheck{Safe: true}
}

func validateIFSInjection(cmd string) SecurityCheck {
	if strings.Contains(cmd, "$IFS") || strings.Contains(cmd, "${") && strings.Contains(cmd, "IFS") {
		return SecurityCheck{
			Safe:    false,
			Message: "injecao IFS detectada ($IFS ou ${...IFS...})",
		}
	}
	return SecurityCheck{Safe: true}
}

func validateProcEnvironAccess(cmd string) SecurityCheck {
	if strings.Contains(cmd, "/proc/") && strings.Contains(cmd, "/environ") {
		return SecurityCheck{
			Safe:    false,
			Message: "acesso a /proc/*/environ detectado",
		}
	}
	return SecurityCheck{Safe: true}
}

func validateBackslashEscapedWhitespace(cmd string) SecurityCheck {
	if strings.Contains(cmd, `\ `) {
		return SecurityCheck{
			Safe:    false,
			Message: "espaco com barra invertida detectado (\\ )",
		}
	}
	return SecurityCheck{Safe: true}
}

func validateBraceExpansion(cmd string) SecurityCheck {
	if regexp.MustCompile(`\{[a-zA-Z0-9]+(\.\.|,)[a-zA-Z0-9,]+\}`).MatchString(cmd) {
		return SecurityCheck{
			Safe:    false,
			Message: "expansao de chaves detectada ({a,b} ou {1..5})",
		}
	}
	return SecurityCheck{Safe: true}
}

func validateUnicodeWhitespace(cmd string) SecurityCheck {
	// Check for non-ASCII whitespace that shell-quote might split differently than bash
	for _, r := range cmd {
		if r > 127 && (r == '\u00A0' || r == '\u2000' || r == '\u2001' || r == '\u2002' || r == '\u2003') {
			return SecurityCheck{
				Safe:    false,
				Message: "whitespace unicode detectado",
			}
		}
	}
	return SecurityCheck{Safe: true}
}

func validateMidWordHash(cmd string) SecurityCheck {
	// Block mid-word # (shell-quote treats as comment, bash treats as literal)
	if regexp.MustCompile(`[a-zA-Z0-9]#[a-zA-Z0-9]`).MatchString(cmd) {
		return SecurityCheck{
			Safe:         false,
			Message:      "# no meio da palavra detectado",
			IsMisparsing: true,
		}
	}
	return SecurityCheck{Safe: true}
}

func validateCarriageReturn(cmd string) SecurityCheck {
	inQuote := false
	for _, r := range cmd {
		if r == '\'' || r == '"' {
			inQuote = !inQuote
		}
		if !inQuote && r == '\r' {
			return SecurityCheck{
				Safe:         false,
				Message:      "carriage return (\\r) fora de aspas",
				IsMisparsing: true,
			}
		}
	}
	return SecurityCheck{Safe: true}
}
