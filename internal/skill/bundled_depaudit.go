package skill

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/sashabaranov/go-openai/jsonschema"
)

// newDepAuditSkill builds the dependency-audit skill: it parses go.mod and
// flags dependencies that match an embedded list of known CVEs.
func newDepAuditSkill() *Skill {
	return &Skill{
		Name:        "dependency-audit",
		Description: "Parse go.mod and flag dependencies with known CVEs from an embedded list.",
		Parameters: jsonschema.Definition{
			Type: jsonschema.Object,
			Properties: map[string]jsonschema.Definition{
				"mod_path": {
					Type:        jsonschema.String,
					Description: "Path to the go.mod file",
				},
			},
			Required: []string{"mod_path"},
		},
		Execute: func(args map[string]string) (string, error) {
			modPath := args["mod_path"]
			if modPath == "" {
				return "", fmt.Errorf("mod_path is required")
			}
			entries, err := auditModules(modPath)
			if err != nil {
				return "", err
			}
			return renderDepReport(entries), nil
		},
	}
}

// DepEntry represents a single dependency parsed from go.mod.
type DepEntry struct {
	Path     string
	Version  string
	Indirect bool
	CVEs     []string
	Notes    string
}

// auditModules parses go.mod and matches deps against the CVE list.
func auditModules(modPath string) ([]DepEntry, error) {
	data, err := os.ReadFile(modPath)
	if err != nil {
		return nil, fmt.Errorf("read go.mod: %w", err)
	}

	var entries []DepEntry
	inRequire := false
	multiLine := false

	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		if line == "" || strings.HasPrefix(line, "//") {
			continue
		}

		if strings.HasPrefix(line, "require (") {
			inRequire = true
			multiLine = true
			continue
		}
		if strings.HasPrefix(line, "require ") && !multiLine {
			// Single-line require
			parts := strings.Fields(strings.TrimPrefix(line, "require "))
			if len(parts) >= 2 {
				entries = append(entries, parseRequireLine(parts[0]+" "+parts[1], false))
			}
			continue
		}
		if line == ")" && multiLine {
			inRequire = false
			multiLine = false
			continue
		}

		if inRequire {
			indirect := strings.Contains(line, "// indirect")
			if idx := strings.Index(line, "//"); idx >= 0 {
				line = strings.TrimSpace(line[:idx])
			}
			if line != "" {
				entries = append(entries, parseRequireLine(line, indirect))
			}
		}
	}

	// Match against CVE list
	for i := range entries {
		entries[i].CVEs = lookupCVEs(entries[i].Path, entries[i].Version)
	}

	return entries, nil
}

func parseRequireLine(line string, indirect bool) DepEntry {
	parts := strings.Fields(line)
	entry := DepEntry{Indirect: indirect}
	if len(parts) >= 1 {
		entry.Path = parts[0]
	}
	if len(parts) >= 2 {
		entry.Version = parts[1]
	}
	return entry
}

// CVEEntry describes a known vulnerability.
type CVEEntry struct {
	Module      string
	VulnVersion string // exact vulnerable version prefix (e.g. "v1.0.0" matches "v1.0.0*")
	CVE         string
	Severity    string
	Note        string
}

// knownCVEs is a small embedded list of historical Go module vulnerabilities.
// Real usage would pull from https://pkg.go.dev/vuln or govulncheck.
var knownCVEs = []CVEEntry{
	{
		Module:      "github.com/dgrijalva/jwt-go",
		VulnVersion: "",
		CVE:         "CVE-2020-26160",
		Severity:    "HIGH",
		Note:        "JWT bypass; package deprecated - migrate to golang-jwt/jwt",
	},
	{
		Module:      "gopkg.in/yaml.v2",
		VulnVersion: "v2.2.0",
		CVE:         "CVE-2019-11254",
		Severity:    "MEDIUM",
		Note:        "Stack overflow via crafted YAML",
	},
	{
		Module:      "github.com/hashicorp/consul",
		VulnVersion: "v1.0.0",
		CVE:         "CVE-2020-13250",
		Severity:    "HIGH",
		Note:        "Privilege escalation via HTTP API",
	},
	{
		Module:      "github.com/gin-gonic/gin",
		VulnVersion: "v1.6.0",
		CVE:         "CVE-2020-28483",
		Severity:    "MEDIUM",
		Note:        "Path traversal via custom routes",
	},
	{
		Module:      "github.com/gorilla/websocket",
		VulnVersion: "v1.4.0",
		CVE:         "CVE-2020-27813",
		Severity:    "HIGH",
		Note:        "Integer overflow in message parsing",
	},
	{
		Module:      "github.com/prometheus/client_golang",
		VulnVersion: "v1.11.0",
		CVE:         "CVE-2022-21698",
		Severity:    "MEDIUM",
		Note:        "Unbounded metric cardinality via user input",
	},
	{
		Module:      "golang.org/x/crypto",
		VulnVersion: "v0.0.0-20211202192323",
		CVE:         "CVE-2022-27191",
		Severity:    "HIGH",
		Note:        "SSH panic via crafted signature",
	},
	{
		Module:      "golang.org/x/net",
		VulnVersion: "v0.17.0",
		CVE:         "CVE-2023-39325",
		Severity:    "HIGH",
		Note:        "HTTP/2 rapid reset DoS",
	},
}

// lookupCVEs returns CVE identifiers matching the given module and version.
func lookupCVEs(module, version string) []string {
	var cves []string
	for _, entry := range knownCVEs {
		if entry.Module != module {
			continue
		}
		// If no version specified in CVE entry, all versions are affected
		if entry.VulnVersion == "" {
			cves = append(cves, entry.CVE+" ("+entry.Severity+")")
			continue
		}
		// Prefix match (simplistic - real tool would parse semver)
		if strings.HasPrefix(version, entry.VulnVersion) {
			cves = append(cves, entry.CVE+" ("+entry.Severity+")")
		}
	}
	return cves
}

func renderDepReport(entries []DepEntry) string {
	var sb strings.Builder

	sb.WriteString("# Dependency Audit Report\n\n")

	var direct, indirect int
	var vulnerable int
	for _, e := range entries {
		if e.Indirect {
			indirect++
		} else {
			direct++
		}
		if len(e.CVEs) > 0 {
			vulnerable++
		}
	}

	sb.WriteString(fmt.Sprintf("**Total**: %d dependencies (%d direct, %d indirect)\n", len(entries), direct, indirect))
	sb.WriteString(fmt.Sprintf("**Vulnerable**: %d\n\n", vulnerable))

	if vulnerable > 0 {
		sb.WriteString("## Vulnerabilities Found\n\n")
		for _, e := range entries {
			if len(e.CVEs) == 0 {
				continue
			}
			sb.WriteString(fmt.Sprintf("### %s %s\n", e.Path, e.Version))
			if e.Indirect {
				sb.WriteString("_(indirect)_\n\n")
			}
			for _, cve := range e.CVEs {
				sb.WriteString("- " + cve + "\n")
			}
			sb.WriteString("\n")
		}
	}

	sb.WriteString("## All Dependencies\n\n")
	sb.WriteString("| Module | Version | Type | Status |\n")
	sb.WriteString("|--------|---------|------|--------|\n")
	for _, e := range entries {
		typ := "direct"
		if e.Indirect {
			typ = "indirect"
		}
		status := "OK"
		if len(e.CVEs) > 0 {
			status = fmt.Sprintf("**%d CVE(s)**", len(e.CVEs))
		}
		sb.WriteString(fmt.Sprintf("| %s | %s | %s | %s |\n", e.Path, e.Version, typ, status))
	}

	return sb.String()
}
