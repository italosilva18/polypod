// Package licensescan scans project trees for license files and classifies them.
package licensescan

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// LicenseInfo describes a detected license.
type LicenseInfo struct {
	Path       string  // file path where detected
	SPDX       string  // SPDX identifier (MIT, Apache-2.0, GPL-3.0, etc.)
	Category   string  // "permissive", "weak-copyleft", "copyleft", "proprietary", "public-domain", "unknown"
	Name       string  // human-friendly name
	Confidence float64 // 0-1
	Source     string  // "LICENSE file", "go.mod", "package.json", "header comment"
}

// LicenseDB contains the detection rules.
var licenseDB = map[string]struct {
	Name     string
	Category string
	Markers  []string // case-insensitive substrings that identify this license
}{
	"MIT": {
		Name:     "MIT License",
		Category: "permissive",
		Markers: []string{
			"permission is hereby granted, free of charge",
			"mit license",
		},
	},
	"Apache-2.0": {
		Name:     "Apache License 2.0",
		Category: "permissive",
		Markers: []string{
			"apache license, version 2.0",
			"apache license",
			"licensed under the apache license",
		},
	},
	"BSD-3-Clause": {
		Name:     "BSD 3-Clause License",
		Category: "permissive",
		Markers: []string{
			"redistribution and use in source and binary forms, with or without modification",
			"neither the name of",
			"bsd 3-clause",
		},
	},
	"BSD-2-Clause": {
		Name:     "BSD 2-Clause License",
		Category: "permissive",
		Markers: []string{
			"redistribution and use in source and binary forms, with or without modification",
			"bsd 2-clause",
		},
	},
	"ISC": {
		Name:     "ISC License",
		Category: "permissive",
		Markers: []string{
			"isc license",
			"permission to use, copy, modify, and/or distribute this software",
		},
	},
	"GPL-3.0": {
		Name:     "GNU General Public License v3.0",
		Category: "copyleft",
		Markers: []string{
			"gnu general public license",
			"gpl-3.0",
			"version 3, 29 june 2007",
		},
	},
	"GPL-2.0": {
		Name:     "GNU General Public License v2.0",
		Category: "copyleft",
		Markers: []string{
			"gnu general public license",
			"version 2",
			"june 1991",
		},
	},
	"LGPL-3.0": {
		Name:     "GNU Lesser General Public License v3.0",
		Category: "weak-copyleft",
		Markers: []string{
			"gnu lesser general public license",
			"lgpl",
		},
	},
	"MPL-2.0": {
		Name:     "Mozilla Public License 2.0",
		Category: "weak-copyleft",
		Markers: []string{
			"mozilla public license",
			"version 2.0",
		},
	},
	"AGPL-3.0": {
		Name:     "GNU Affero General Public License v3.0",
		Category: "copyleft",
		Markers: []string{
			"gnu affero general public license",
			"agpl",
		},
	},
	"Unlicense": {
		Name:     "The Unlicense",
		Category: "public-domain",
		Markers: []string{
			"this is free and unencumbered software released into the public domain",
			"unlicense",
		},
	},
	"CC0-1.0": {
		Name:     "Creative Commons Zero v1.0",
		Category: "public-domain",
		Markers: []string{
			"cc0 1.0",
			"creative commons zero",
			"public domain dedication",
		},
	},
}

// LicenseFileNames lists common license file names.
var LicenseFileNames = []string{
	"LICENSE", "LICENSE.txt", "LICENSE.md",
	"LICENCE", "LICENCE.txt",
	"COPYING", "COPYING.txt",
	"COPYRIGHT", "COPYRIGHT.txt",
	"UNLICENSE",
	"MIT-LICENSE", "MIT-LICENSE.txt",
	"APACHE-LICENSE", "APACHE-LICENSE.txt",
}

// Config controls the license scan.
type Config struct {
	SkipDirs     []string
	MaxFileSize  int64
	CheckHeaders bool // scan source file headers
}

// DefaultConfig returns sensible defaults.
func DefaultConfig() Config {
	return Config{
		SkipDirs:     []string{"vendor", "node_modules", ".git", "dist", "build"},
		MaxFileSize:  500 * 1024, // 500KB
		CheckHeaders: false,
	}
}

// Scan walks a directory tree and returns all detected licenses.
func Scan(root string) ([]LicenseInfo, error) {
	return ScanWithConfig(root, DefaultConfig())
}

// ScanWithConfig scans with explicit configuration.
func ScanWithConfig(root string, cfg Config) ([]LicenseInfo, error) {
	var results []LicenseInfo

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			name := info.Name()
			for _, skip := range cfg.SkipDirs {
				if name == skip {
					return filepath.SkipDir
				}
			}
			return nil
		}

		base := info.Name()

		// LICENSE files
		if isLicenseFile(base) {
			if info.Size() > cfg.MaxFileSize {
				return nil
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return nil
			}
			info := detectLicense(path, string(data), "LICENSE file")
			if info != nil {
				results = append(results, *info)
			}
			return nil
		}

		// go.mod - extract module info (actual deps listed)
		if base == "go.mod" && info.Size() < cfg.MaxFileSize {
			data, err := os.ReadFile(path)
			if err != nil {
				return nil
			}
			// For go.mod we just note its presence
			if strings.Contains(string(data), "module ") {
				results = append(results, LicenseInfo{
					Path:       path,
					Category:   "unknown",
					Source:     "go.mod",
					Name:       "Go module",
					Confidence: 0.5,
				})
			}
			return nil
		}

		// package.json - extract license field
		if base == "package.json" && info.Size() < cfg.MaxFileSize {
			data, err := os.ReadFile(path)
			if err != nil {
				return nil
			}
			license := extractNpmLicense(string(data))
			if license != "" {
				info := &LicenseInfo{
					Path:       path,
					SPDX:       license,
					Source:     "package.json",
					Confidence: 0.9,
				}
				if entry, ok := licenseDB[license]; ok {
					info.Name = entry.Name
					info.Category = entry.Category
				} else {
					info.Category = "unknown"
					info.Name = license
				}
				results = append(results, *info)
			}
			return nil
		}

		return nil
	})

	if err != nil {
		return nil, err
	}
	return results, nil
}

// isLicenseFile returns true if a filename looks like a license file.
func isLicenseFile(name string) bool {
	upper := strings.ToUpper(name)
	for _, ln := range LicenseFileNames {
		if upper == strings.ToUpper(ln) {
			return true
		}
	}
	// Also match LICENSE.* patterns
	if strings.HasPrefix(upper, "LICENSE") || strings.HasPrefix(upper, "LICENCE") ||
		strings.HasPrefix(upper, "COPYING") {
		return true
	}
	return false
}

// detectLicense attempts to classify a license from file content.
func detectLicense(path, content, source string) *LicenseInfo {
	lower := strings.ToLower(content)

	// Try each license
	bestSPDX := ""
	bestScore := 0
	for spdx, entry := range licenseDB {
		score := 0
		for _, marker := range entry.Markers {
			if strings.Contains(lower, strings.ToLower(marker)) {
				score++
			}
		}
		if score > bestScore {
			bestScore = score
			bestSPDX = spdx
		}
	}

	if bestSPDX == "" {
		return &LicenseInfo{
			Path:       path,
			SPDX:       "",
			Name:       "Unknown",
			Category:   "unknown",
			Source:     source,
			Confidence: 0.0,
		}
	}

	entry := licenseDB[bestSPDX]
	confidence := float64(bestScore) / float64(len(entry.Markers))
	if confidence > 1.0 {
		confidence = 1.0
	}

	return &LicenseInfo{
		Path:       path,
		SPDX:       bestSPDX,
		Name:       entry.Name,
		Category:   entry.Category,
		Source:     source,
		Confidence: confidence,
	}
}

// extractNpmLicense pulls the "license" field from a package.json.
func extractNpmLicense(content string) string {
	scanner := bufio.NewScanner(strings.NewReader(content))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.Contains(line, `"license"`) {
			// Extract value between quotes after ":"
			idx := strings.Index(line, ":")
			if idx < 0 {
				continue
			}
			rest := line[idx+1:]
			start := strings.Index(rest, `"`)
			if start < 0 {
				continue
			}
			rest = rest[start+1:]
			end := strings.Index(rest, `"`)
			if end < 0 {
				continue
			}
			return rest[:end]
		}
	}
	return ""
}

// Classify returns the category for an SPDX identifier.
func Classify(spdx string) string {
	if entry, ok := licenseDB[spdx]; ok {
		return entry.Category
	}
	return "unknown"
}

// FormatReport returns a human-friendly scan report.
func FormatReport(licenses []LicenseInfo) string {
	if len(licenses) == 0 {
		return "No licenses detected."
	}

	// Group by category
	byCategory := make(map[string][]LicenseInfo)
	for _, l := range licenses {
		byCategory[l.Category] = append(byCategory[l.Category], l)
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("LICENSE SCAN REPORT (%d findings)\n", len(licenses)))
	sb.WriteString("====================\n\n")

	categories := []string{"copyleft", "weak-copyleft", "permissive", "public-domain", "proprietary", "unknown"}
	for _, cat := range categories {
		items := byCategory[cat]
		if len(items) == 0 {
			continue
		}
		sb.WriteString(fmt.Sprintf("## %s (%d)\n", strings.ToUpper(cat), len(items)))
		sort.Slice(items, func(i, j int) bool {
			return items[i].Path < items[j].Path
		})
		for _, l := range items {
			sb.WriteString(fmt.Sprintf("  %s: %s (%.0f%%)\n", l.Path, l.Name, l.Confidence*100))
		}
		sb.WriteString("\n")
	}

	return sb.String()
}
