// Package semverutil provides semantic version parsing, comparison, and range matching.
package semverutil

import (
	"fmt"
	"strconv"
	"strings"
)

// Version represents a parsed semantic version.
type Version struct {
	Major int
	Minor int
	Patch int
	Pre   string // pre-release tag (e.g. "alpha.1")
	Build string // build metadata (e.g. "20130313144700")
}

// Parse parses a semver string like "1.2.3-alpha.1+build.123".
func Parse(s string) (Version, error) {
	s = strings.TrimPrefix(s, "v")
	s = strings.TrimPrefix(s, "V")
	if s == "" {
		return Version{}, fmt.Errorf("empty version")
	}

	var v Version

	// Extract build metadata
	if idx := strings.Index(s, "+"); idx >= 0 {
		v.Build = s[idx+1:]
		s = s[:idx]
	}

	// Extract pre-release
	if idx := strings.Index(s, "-"); idx >= 0 {
		v.Pre = s[idx+1:]
		s = s[:idx]
	}

	// Parse numeric parts
	parts := strings.Split(s, ".")
	if len(parts) == 0 || len(parts) > 3 {
		return Version{}, fmt.Errorf("invalid version format: %s", s)
	}

	nums := make([]int, 3)
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return Version{}, fmt.Errorf("invalid version component %q: %v", p, err)
		}
		if n < 0 {
			return Version{}, fmt.Errorf("version component must be non-negative: %d", n)
		}
		nums[i] = n
	}
	v.Major = nums[0]
	v.Minor = nums[1]
	v.Patch = nums[2]
	return v, nil
}

// MustParse is like Parse but panics on error.
func MustParse(s string) Version {
	v, err := Parse(s)
	if err != nil {
		panic(err)
	}
	return v
}

// String returns the canonical string representation.
func (v Version) String() string {
	s := fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
	if v.Pre != "" {
		s += "-" + v.Pre
	}
	if v.Build != "" {
		s += "+" + v.Build
	}
	return s
}

// Compare returns -1, 0, or 1 comparing v to o.
// Build metadata is NOT considered per semver spec.
func (v Version) Compare(o Version) int {
	if v.Major != o.Major {
		return intCmp(v.Major, o.Major)
	}
	if v.Minor != o.Minor {
		return intCmp(v.Minor, o.Minor)
	}
	if v.Patch != o.Patch {
		return intCmp(v.Patch, o.Patch)
	}
	// Pre-release: a version WITH pre is LESS than one WITHOUT
	if v.Pre == "" && o.Pre != "" {
		return 1
	}
	if v.Pre != "" && o.Pre == "" {
		return -1
	}
	if v.Pre != o.Pre {
		return comparePre(v.Pre, o.Pre)
	}
	return 0
}

// Less returns true if v < o.
func (v Version) Less(o Version) bool {
	return v.Compare(o) < 0
}

// Equal returns true if v == o (ignoring build metadata).
func (v Version) Equal(o Version) bool {
	return v.Compare(o) == 0
}

// IsZero returns true if v is the zero value.
func (v Version) IsZero() bool {
	return v.Major == 0 && v.Minor == 0 && v.Patch == 0 && v.Pre == "" && v.Build == ""
}

// Bump returns a new version with the given component bumped.
// kind: "major", "minor", or "patch".
func (v Version) Bump(kind string) Version {
	result := v
	result.Pre = ""
	result.Build = ""
	switch kind {
	case "major":
		result.Major++
		result.Minor = 0
		result.Patch = 0
	case "minor":
		result.Minor++
		result.Patch = 0
	case "patch":
		result.Patch++
	}
	return result
}

// Match checks if v satisfies the range expression.
// Supported: "1.2.3", "^1.2.3", "~1.2.3", ">=1.0.0", ">1.0.0", "<=2.0.0", "<2.0.0",
//
//	">=1.0.0 <2.0.0" (AND), "1.x", "1.2.x", "*".
func Match(v string, rangeExpr string) (bool, error) {
	ver, err := Parse(v)
	if err != nil {
		return false, err
	}
	return MatchVersion(ver, rangeExpr)
}

// MatchVersion is like Match but takes a parsed Version.
func MatchVersion(v Version, rangeExpr string) (bool, error) {
	rangeExpr = strings.TrimSpace(rangeExpr)
	if rangeExpr == "" || rangeExpr == "*" {
		return true, nil
	}

	// Handle AND ranges (space-separated)
	parts := strings.Fields(rangeExpr)
	for _, part := range parts {
		ok, err := matchSingle(v, part)
		if err != nil {
			return false, err
		}
		if !ok {
			return false, nil
		}
	}
	return true, nil
}

func matchSingle(v Version, expr string) (bool, error) {
	expr = strings.TrimSpace(expr)

	// Prefix operators
	switch {
	case strings.HasPrefix(expr, "^"):
		ref, err := Parse(strings.TrimPrefix(expr, "^"))
		if err != nil {
			return false, err
		}
		// Compatible with: ref <= v < next major (if major > 0)
		// For major 0: ref <= v < next minor
		if v.Compare(ref) < 0 {
			return false, nil
		}
		if ref.Major > 0 {
			return v.Major == ref.Major, nil
		}
		if ref.Minor > 0 {
			return v.Major == 0 && v.Minor == ref.Minor, nil
		}
		return v.Major == 0 && v.Minor == 0 && v.Patch == ref.Patch, nil
	case strings.HasPrefix(expr, "~"):
		ref, err := Parse(strings.TrimPrefix(expr, "~"))
		if err != nil {
			return false, err
		}
		// ~1.2.3 := >=1.2.3, <1.3.0
		if v.Compare(ref) < 0 {
			return false, nil
		}
		return v.Major == ref.Major && v.Minor == ref.Minor, nil
	case strings.HasPrefix(expr, ">="):
		ref, err := Parse(strings.TrimPrefix(expr, ">="))
		if err != nil {
			return false, err
		}
		return v.Compare(ref) >= 0, nil
	case strings.HasPrefix(expr, "<="):
		ref, err := Parse(strings.TrimPrefix(expr, "<="))
		if err != nil {
			return false, err
		}
		return v.Compare(ref) <= 0, nil
	case strings.HasPrefix(expr, ">"):
		ref, err := Parse(strings.TrimPrefix(expr, ">"))
		if err != nil {
			return false, err
		}
		return v.Compare(ref) > 0, nil
	case strings.HasPrefix(expr, "<"):
		ref, err := Parse(strings.TrimPrefix(expr, "<"))
		if err != nil {
			return false, err
		}
		return v.Compare(ref) < 0, nil
	case strings.HasPrefix(expr, "="):
		ref, err := Parse(strings.TrimPrefix(expr, "="))
		if err != nil {
			return false, err
		}
		return v.Equal(ref), nil
	}

	// x-ranges: 1.x, 1.2.x
	if strings.Contains(expr, "x") || strings.Contains(expr, "X") || strings.Contains(expr, "*") {
		return matchXRange(v, expr)
	}

	// Exact match
	ref, err := Parse(expr)
	if err != nil {
		return false, err
	}
	return v.Equal(ref), nil
}

func matchXRange(v Version, expr string) (bool, error) {
	expr = strings.ReplaceAll(expr, "X", "x")
	expr = strings.ReplaceAll(expr, "*", "x")
	parts := strings.Split(expr, ".")

	if len(parts) >= 1 && parts[0] != "x" {
		n, err := strconv.Atoi(parts[0])
		if err != nil {
			return false, err
		}
		if v.Major != n {
			return false, nil
		}
	}
	if len(parts) >= 2 && parts[1] != "x" {
		n, err := strconv.Atoi(parts[1])
		if err != nil {
			return false, err
		}
		if v.Minor != n {
			return false, nil
		}
	}
	if len(parts) >= 3 && parts[2] != "x" {
		n, err := strconv.Atoi(parts[2])
		if err != nil {
			return false, err
		}
		if v.Patch != n {
			return false, nil
		}
	}
	return true, nil
}

func intCmp(a, b int) int {
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}

// comparePre compares two pre-release strings per semver rules.
func comparePre(a, b string) int {
	aParts := strings.Split(a, ".")
	bParts := strings.Split(b, ".")
	for i := 0; i < len(aParts) && i < len(bParts); i++ {
		ap, bp := aParts[i], bParts[i]
		an, aIsNum := tryAtoi(ap)
		bn, bIsNum := tryAtoi(bp)
		switch {
		case aIsNum && bIsNum:
			if an != bn {
				return intCmp(an, bn)
			}
		case aIsNum && !bIsNum:
			return -1 // numeric < alphanumeric
		case !aIsNum && bIsNum:
			return 1
		default:
			if ap != bp {
				if ap < bp {
					return -1
				}
				return 1
			}
		}
	}
	return intCmp(len(aParts), len(bParts))
}

func tryAtoi(s string) (int, bool) {
	n, err := strconv.Atoi(s)
	return n, err == nil
}

// Sort sorts a slice of versions in ascending order.
func Sort(versions []Version) {
	// Simple insertion sort (typical version lists are small)
	for i := 1; i < len(versions); i++ {
		j := i
		for j > 0 && versions[j].Less(versions[j-1]) {
			versions[j-1], versions[j] = versions[j], versions[j-1]
			j--
		}
	}
}

// Highest returns the highest version from a slice.
func Highest(versions []Version) (Version, bool) {
	if len(versions) == 0 {
		return Version{}, false
	}
	best := versions[0]
	for _, v := range versions[1:] {
		if v.Compare(best) > 0 {
			best = v
		}
	}
	return best, true
}
