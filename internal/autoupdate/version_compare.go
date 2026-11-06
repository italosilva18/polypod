package autoupdate

import (
	"fmt"

	"github.com/costa/polypod/internal/semverutil"
)

// VersionComparison describes the result of comparing two versions.
type VersionComparison struct {
	Current    semverutil.Version
	Latest     semverutil.Version
	IsOutdated bool
	IsSame     bool
	IsNewer    bool
	DiffKind   string // "major", "minor", "patch", "prerelease", "none"
}

// CompareVersions compares the installed version against the latest.
// Returns a structured result indicating if an update is available.
func CompareVersions(current, latest string) (VersionComparison, error) {
	cur, err := semverutil.Parse(current)
	if err != nil {
		return VersionComparison{}, fmt.Errorf("parse current %q: %w", current, err)
	}
	lat, err := semverutil.Parse(latest)
	if err != nil {
		return VersionComparison{}, fmt.Errorf("parse latest %q: %w", latest, err)
	}

	cmp := cur.Compare(lat)

	result := VersionComparison{
		Current: cur,
		Latest:  lat,
	}

	switch {
	case cmp < 0:
		result.IsOutdated = true
		result.DiffKind = diffKind(cur, lat)
	case cmp == 0:
		result.IsSame = true
		result.DiffKind = "none"
	case cmp > 0:
		result.IsNewer = true
		result.DiffKind = diffKind(lat, cur)
	}

	return result, nil
}

// diffKind returns the highest-order component that differs between a and b.
func diffKind(a, b semverutil.Version) string {
	if a.Major != b.Major {
		return "major"
	}
	if a.Minor != b.Minor {
		return "minor"
	}
	if a.Patch != b.Patch {
		return "patch"
	}
	if a.Pre != b.Pre {
		return "prerelease"
	}
	return "none"
}

// ShouldNotifyUpdate returns true if the user should be notified about an update.
// Major/minor updates always notify; patches only if the threshold is met.
func ShouldNotifyUpdate(comparison VersionComparison, minDiffKind string) bool {
	if !comparison.IsOutdated {
		return false
	}

	rank := map[string]int{
		"prerelease": 1,
		"patch":      2,
		"minor":      3,
		"major":      4,
	}

	got := rank[comparison.DiffKind]
	want := rank[minDiffKind]
	if want == 0 {
		want = rank["patch"]
	}

	return got >= want
}

// FormatUpdateMessage returns a human-readable update notification string.
func FormatUpdateMessage(comparison VersionComparison) string {
	if !comparison.IsOutdated {
		return fmt.Sprintf("polypod %s (latest)", comparison.Current)
	}
	return fmt.Sprintf("polypod %s → %s (%s update available)",
		comparison.Current, comparison.Latest, comparison.DiffKind)
}

// PickLatest selects the highest version from a list of candidate version strings.
// Ignores entries that fail to parse.
func PickLatest(versions []string) (string, bool) {
	var parsed []semverutil.Version
	for _, v := range versions {
		if pv, err := semverutil.Parse(v); err == nil {
			parsed = append(parsed, pv)
		}
	}

	if len(parsed) == 0 {
		return "", false
	}

	highest, ok := semverutil.Highest(parsed)
	if !ok {
		return "", false
	}
	return highest.String(), true
}

// VersionMatchesConstraint returns true if a version satisfies a range expression.
// Wraps semverutil.Match.
func VersionMatchesConstraint(version, constraint string) (bool, error) {
	return semverutil.Match(version, constraint)
}
