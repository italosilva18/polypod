package tooloutput

import (
	"fmt"
	"regexp"
	"strings"
)

// CompressThreshold is the minimum byte size before command/build/test output
// undergoes structural diagnostic extraction instead of raw context dumping.
const CompressThreshold = 12 << 10 // 12 KiB

var commandOutputTools = map[string]bool{
	"bash":               true,
	"bash_output":        true,
	"run_project_checks": true,
	"powershell":         true,
	"parallel_best_of_n": true,
}

var (
	reCompilerDiag = regexp.MustCompile(`(?i)(^|\s)([A-Za-z0-9_./\\-]+:[0-9]+(:[0-9]+)?:\s*(error|warning|fatal|undefined|cannot|syntax|invalid|undeclared|not enough|too many|type|mismatched)|error\[E[0-9]+\]:|SyntaxError:|TypeError:|ReferenceError:|ImportError:|ModuleNotFoundError:)`)
	reTestFailure  = regexp.MustCompile(`^(--- FAIL:|FAIL\s+\S+|FAILED\s+|●\s+|panic:|fatal error:|Traceback \(most recent call last\):|AssertionError|E\s+assert\s+)`)
)

// CompressForModel keeps non-command tool outputs intact up to PreviewLimit,
// while structurally compressing large build/test/shell outputs (>12 KiB) into:
//   1. Head context (first 20 lines)
//   2. High-signal compiler errors, test failures, panics, and stack frames
//   3. Tail summary (last 25 lines)
//   4. Full raw archive pointer (`tool_output` id=<id>) so nothing is lost.
func CompressForModel(toolName, content string) string {
	if !commandOutputTools[toolName] || len(content) <= CompressThreshold {
		return Preview(content)
	}
	if len(content) > FileLimit {
		return Preview(content)
	}

	lines := strings.Split(content, "\n")
	if len(lines) <= 80 {
		return Preview(content)
	}

	const headCount = 20
	const tailCount = 25

	var highlights []string
	seen := make(map[string]bool)
	captureFollowup := 0

	midStart := headCount
	midEnd := len(lines) - tailCount
	if midEnd < midStart {
		return Preview(content)
	}

	for i := midStart; i < midEnd; i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			if captureFollowup > 0 {
				captureFollowup--
			}
			continue
		}

		isFail := reTestFailure.MatchString(trimmed)
		isDiag := !isFail && reCompilerDiag.MatchString(line)

		if isFail {
			captureFollowup = 8
		} else if isDiag {
			captureFollowup = 2
		}

		if isFail || isDiag || captureFollowup > 0 {
			if !isFail && !isDiag {
				captureFollowup--
			}
			clipped := strings.ToValidUTF8(line, "")
			if len(clipped) > 240 {
				clipped = clipped[:240] + "..."
			}
			entry := fmt.Sprintf("L%d: %s", i+1, clipped)
			if !seen[clipped] {
				seen[clipped] = true
				highlights = append(highlights, entry)
			}
			if len(highlights) >= 65 {
				highlights = append(highlights, "... [demais diagnósticos omitidos no resumo; consulte o log completo via tool_output]")
				break
			}
		}
	}

	// For non-diagnostic command dumps (e.g., large git log or JSON), allow up to
	// 24 KiB raw before compressing into head + tail + archive ID.
	if len(highlights) == 0 && len(content) <= 24<<10 {
		return content
	}

	id, storeErr := Store(content)

	var b strings.Builder
	headSlice := strings.Join(lines[:headCount], "\n")
	tailSlice := strings.Join(lines[midEnd:], "\n")

	b.WriteString(strings.ToValidUTF8(headSlice, ""))
	b.WriteString("\n\n")

	if len(highlights) > 0 {
		fmt.Fprintf(&b, "=== [DIAGNÓSTICOS EXTRAÍDOS AUTOMATICAMENTE (%d linhas críticas de %d totais)] ===\n", len(highlights), len(lines))
		b.WriteString(strings.Join(highlights, "\n"))
		b.WriteString("\n=== [FIM DOS DIAGNÓSTICOS EXTRAÍDOS] ===\n\n")
	} else {
		fmt.Fprintf(&b, "... [%d linhas intermediárias omitidas para preservar janela de contexto] ...\n\n", midEnd-midStart)
	}

	b.WriteString(strings.ToValidUTF8(tailSlice, ""))
	b.WriteString("\n")

	if storeErr == nil && id != "" {
		fmt.Fprintf(&b, "\n[Saída comprimida estruturalmente: %d bytes originais -> resumo de alta densidade. Log completo salvo: use tool_output com id=%s e offset em bytes se precisar paginar.]", len(content), id)
	} else {
		fmt.Fprintf(&b, "\n[Saída comprimida estruturalmente: %d bytes originais]", len(content))
	}
	return b.String()
}
