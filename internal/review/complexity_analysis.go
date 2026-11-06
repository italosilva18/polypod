package review

import (
	"fmt"
	"sort"
	"strings"

	"github.com/costa/polypod/internal/analysis"
)

// ComplexityReport summarizes cyclomatic complexity findings.
type ComplexityReport struct {
	TotalFunctions int
	AvgComplexity  float64
	MaxComplexity  int
	MinComplexity  int
	HighRisk       []analysis.FuncComplexity // score > 20
	MediumRisk     []analysis.FuncComplexity // 11-20
	LowRisk        []analysis.FuncComplexity // 6-10
	Simple         []analysis.FuncComplexity // <= 5
	Distribution   map[string]int
}

// AnalyzeFileComplexity runs cyclomatic analysis on a single Go file.
func AnalyzeFileComplexity(filePath string) (*ComplexityReport, error) {
	complexities, err := analysis.Cyclomatic(filePath)
	if err != nil {
		return nil, fmt.Errorf("analyze %s: %w", filePath, err)
	}
	return buildReport(complexities), nil
}

// AnalyzeDirectoryComplexity runs cyclomatic analysis on a directory (non-recursive).
func AnalyzeDirectoryComplexity(dir string) (*ComplexityReport, error) {
	complexities, err := analysis.CyclomaticDir(dir)
	if err != nil {
		return nil, fmt.Errorf("analyze dir %s: %w", dir, err)
	}
	return buildReport(complexities), nil
}

// AnalyzeProjectComplexity runs cyclomatic analysis on an entire project.
func AnalyzeProjectComplexity(root string) (*ComplexityReport, error) {
	complexities, err := analysis.CyclomaticRecursive(root)
	if err != nil {
		return nil, fmt.Errorf("analyze project %s: %w", root, err)
	}
	return buildReport(complexities), nil
}

// buildReport groups complexities by risk level.
func buildReport(complexities []analysis.FuncComplexity) *ComplexityReport {
	report := &ComplexityReport{
		TotalFunctions: len(complexities),
		Distribution:   make(map[string]int),
	}

	if len(complexities) == 0 {
		return report
	}

	report.MinComplexity = complexities[0].Score
	report.MaxComplexity = complexities[0].Score

	totalScore := 0
	for _, fc := range complexities {
		totalScore += fc.Score
		if fc.Score > report.MaxComplexity {
			report.MaxComplexity = fc.Score
		}
		if fc.Score < report.MinComplexity {
			report.MinComplexity = fc.Score
		}

		switch {
		case fc.Score > 20:
			report.HighRisk = append(report.HighRisk, fc)
		case fc.Score > 10:
			report.MediumRisk = append(report.MediumRisk, fc)
		case fc.Score > 5:
			report.LowRisk = append(report.LowRisk, fc)
		default:
			report.Simple = append(report.Simple, fc)
		}

		cat := analysis.ComplexityCategory(fc.Score)
		report.Distribution[cat]++
	}

	report.AvgComplexity = float64(totalScore) / float64(len(complexities))

	// Sort high risk by score desc for visibility
	sort.Slice(report.HighRisk, func(i, j int) bool {
		return report.HighRisk[i].Score > report.HighRisk[j].Score
	})
	sort.Slice(report.MediumRisk, func(i, j int) bool {
		return report.MediumRisk[i].Score > report.MediumRisk[j].Score
	})

	return report
}

// FormatComplexityReport renders a report as a markdown-like string.
func FormatComplexityReport(report *ComplexityReport) string {
	var sb strings.Builder

	sb.WriteString("# Complexity Analysis\n\n")

	if report.TotalFunctions == 0 {
		sb.WriteString("No functions analyzed.\n")
		return sb.String()
	}

	fmt.Fprintf(&sb, "- **Total functions**: %d\n", report.TotalFunctions)
	fmt.Fprintf(&sb, "- **Avg complexity**: %.2f\n", report.AvgComplexity)
	fmt.Fprintf(&sb, "- **Max complexity**: %d\n", report.MaxComplexity)
	fmt.Fprintf(&sb, "- **Min complexity**: %d\n\n", report.MinComplexity)

	sb.WriteString("## Distribution\n\n")
	categories := []string{"simple", "moderate", "complex", "very complex", "untestable"}
	for _, cat := range categories {
		count := report.Distribution[cat]
		if count > 0 {
			fmt.Fprintf(&sb, "- **%s**: %d\n", cat, count)
		}
	}
	sb.WriteString("\n")

	if len(report.HighRisk) > 0 {
		sb.WriteString("## HIGH RISK (score > 20)\n\n")
		for _, fc := range report.HighRisk {
			fmt.Fprintf(&sb, "- %s:%d `%s` (score: %d)\n",
				fc.File, fc.Line, fc.Func, fc.Score)
		}
		sb.WriteString("\n")
	}

	if len(report.MediumRisk) > 0 {
		sb.WriteString("## MEDIUM RISK (11-20)\n\n")
		// Only show top 10 for medium
		max := 10
		if len(report.MediumRisk) < max {
			max = len(report.MediumRisk)
		}
		for i := 0; i < max; i++ {
			fc := report.MediumRisk[i]
			fmt.Fprintf(&sb, "- %s:%d `%s` (score: %d)\n",
				fc.File, fc.Line, fc.Func, fc.Score)
		}
		if len(report.MediumRisk) > max {
			fmt.Fprintf(&sb, "- ... and %d more\n", len(report.MediumRisk)-max)
		}
		sb.WriteString("\n")
	}

	return sb.String()
}

// RecommendRefactoring suggests functions to refactor based on complexity.
func RecommendRefactoring(report *ComplexityReport, maxSuggestions int) []string {
	if maxSuggestions <= 0 {
		maxSuggestions = 5
	}

	var suggestions []string
	for _, fc := range report.HighRisk {
		if len(suggestions) >= maxSuggestions {
			break
		}
		name := fc.Func
		if fc.Receiver != "" {
			name = fc.Receiver + "." + fc.Func
		}
		suggestions = append(suggestions,
			fmt.Sprintf("Consider refactoring %s (%s:%d) - complexity score: %d",
				name, fc.File, fc.Line, fc.Score))
	}
	return suggestions
}
