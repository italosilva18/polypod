package autoupdate

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	repoOwner   = "italosilva18"
	repoName    = "polypod"
	currentVersion = "0.4.0"
)

// Release holds GitHub release info.
type Release struct {
	TagName string `json:"tag_name"`
	Name    string `json:"name"`
	Body    string `json:"body"`
	HTMLURL string `json:"html_url"`
	Date    string `json:"published_at"`
}

// CheckForUpdate checks GitHub for a newer release. Returns nil if up-to-date.
func CheckForUpdate() (*Release, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/latest", repoOwner, repoName)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github.v3+json")

	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("verificando atualizacao: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github HTTP %d", resp.StatusCode)
	}

	data, _ := io.ReadAll(resp.Body)
	var release Release
	if err := json.Unmarshal(data, &release); err != nil {
		return nil, err
	}

	latestVersion := strings.TrimPrefix(release.TagName, "v")
	if isNewer(latestVersion, currentVersion) {
		return &release, nil
	}

	return nil, nil // up to date
}

// FormatUpdateNotification creates a display string for update notification.
func FormatUpdateNotification(release *Release) string {
	if release == nil {
		return ""
	}
	return fmt.Sprintf("\033[33m[UPDATE] Nova versao disponivel: %s (atual: v%s)\033[0m\n\033[90m  %s\033[0m",
		release.TagName, currentVersion, release.HTMLURL)
}

// CurrentVersion returns the current version.
func CurrentVersion() string {
	return currentVersion
}

// isNewer returns true if version a is newer than b (simple semver).
func isNewer(a, b string) bool {
	aParts := parseSemver(a)
	bParts := parseSemver(b)

	for i := 0; i < 3; i++ {
		if aParts[i] > bParts[i] {
			return true
		}
		if aParts[i] < bParts[i] {
			return false
		}
	}
	return false
}

func parseSemver(version string) [3]int {
	var parts [3]int
	version = strings.TrimPrefix(version, "v")
	for i, s := range strings.SplitN(version, ".", 3) {
		if i >= 3 {
			break
		}
		fmt.Sscanf(s, "%d", &parts[i])
	}
	return parts
}
