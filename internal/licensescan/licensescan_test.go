package licensescan

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const mitLicense = `MIT License

Copyright (c) 2024 Test

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software.
`

const apacheLicense = `Apache License
Version 2.0, January 2004
http://www.apache.org/licenses/

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
`

const gpl3License = `GNU GENERAL PUBLIC LICENSE
Version 3, 29 June 2007

Copyright (C) 2007 Free Software Foundation, Inc.
Everyone is permitted to copy and distribute verbatim copies
of this license document, but changing it is not allowed.
`

const unlicenseText = `This is free and unencumbered software released into the public domain.

Anyone is free to copy, modify, publish, use, compile, sell, or distribute
this software, either in source code form or as a compiled binary.
`

// --- detectLicense tests ---

func TestDetectLicense_MIT(t *testing.T) {
	info := detectLicense("LICENSE", mitLicense, "LICENSE file")
	if info == nil {
		t.Fatal("detectLicense returned nil")
	}
	if info.SPDX != "MIT" {
		t.Errorf("SPDX = %q; want MIT", info.SPDX)
	}
	if info.Category != "permissive" {
		t.Errorf("Category = %q; want permissive", info.Category)
	}
}

func TestDetectLicense_Apache(t *testing.T) {
	info := detectLicense("LICENSE", apacheLicense, "LICENSE file")
	if info == nil {
		t.Fatal("detectLicense returned nil")
	}
	if info.SPDX != "Apache-2.0" {
		t.Errorf("SPDX = %q; want Apache-2.0", info.SPDX)
	}
}

func TestDetectLicense_GPL3(t *testing.T) {
	info := detectLicense("LICENSE", gpl3License, "LICENSE file")
	if info == nil {
		t.Fatal("detectLicense returned nil")
	}
	if !strings.HasPrefix(info.SPDX, "GPL") {
		t.Errorf("SPDX = %q; want GPL-*", info.SPDX)
	}
	if info.Category != "copyleft" {
		t.Errorf("Category = %q; want copyleft", info.Category)
	}
}

func TestDetectLicense_Unknown(t *testing.T) {
	info := detectLicense("LICENSE", "this is some random text with no license markers", "LICENSE file")
	if info == nil {
		t.Fatal("detectLicense returned nil")
	}
	if info.Category != "unknown" {
		t.Errorf("Category = %q; want unknown", info.Category)
	}
}

func TestDetectLicense_Empty(t *testing.T) {
	info := detectLicense("LICENSE", "", "LICENSE file")
	if info == nil {
		t.Fatal("detectLicense returned nil for empty content")
	}
	if info.Category != "unknown" {
		t.Errorf("Category = %q; want unknown", info.Category)
	}
}

// --- isLicenseFile tests ---

func TestIsLicenseFile(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{"LICENSE", true},
		{"LICENSE.txt", true},
		{"LICENSE.md", true},
		{"COPYING", true},
		{"license.md", true},
		{"LICENCE", true},
		{"UNLICENSE", true},
		{"main.go", false},
		{"README.md", false},
		{"package.json", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isLicenseFile(tt.name)
			if got != tt.want {
				t.Errorf("isLicenseFile(%q) = %v; want %v", tt.name, got, tt.want)
			}
		})
	}
}

// --- Scan tests ---

func TestScan_MITLicense(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "LICENSE"), []byte(mitLicense), 0644); err != nil {
		t.Fatal(err)
	}

	results, err := Scan(dir)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}

	foundMIT := false
	for _, r := range results {
		if r.SPDX == "MIT" {
			foundMIT = true
		}
	}
	if !foundMIT {
		t.Errorf("MIT not detected in results: %v", results)
	}
}

func TestScan_ApacheLicense(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "LICENSE"), []byte(apacheLicense), 0644); err != nil {
		t.Fatal(err)
	}

	results, err := Scan(dir)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	foundApache := false
	for _, r := range results {
		if r.SPDX == "Apache-2.0" {
			foundApache = true
		}
	}
	if !foundApache {
		t.Errorf("Apache not detected: %v", results)
	}
}

func TestScan_MultipleLicenses(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "subdir")
	if err := os.MkdirAll(sub, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "LICENSE"), []byte(mitLicense), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "COPYING"), []byte(gpl3License), 0644); err != nil {
		t.Fatal(err)
	}

	results, err := Scan(dir)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(results) < 2 {
		t.Errorf("got %d results; want >= 2", len(results))
	}
}

func TestScan_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	results, err := Scan(dir)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("empty dir results = %d; want 0", len(results))
	}
}

func TestScan_PackageJSON(t *testing.T) {
	dir := t.TempDir()
	pkg := `{
  "name": "test-pkg",
  "version": "1.0.0",
  "license": "ISC"
}`
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(pkg), 0644); err != nil {
		t.Fatal(err)
	}

	results, err := Scan(dir)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	foundISC := false
	for _, r := range results {
		if r.SPDX == "ISC" && r.Source == "package.json" {
			foundISC = true
		}
	}
	if !foundISC {
		t.Errorf("ISC from package.json not detected: %v", results)
	}
}

func TestScan_SkipsVendorDir(t *testing.T) {
	dir := t.TempDir()
	vendor := filepath.Join(dir, "vendor")
	if err := os.MkdirAll(vendor, 0755); err != nil {
		t.Fatal(err)
	}
	// vendor LICENSE should be skipped
	if err := os.WriteFile(filepath.Join(vendor, "LICENSE"), []byte(mitLicense), 0644); err != nil {
		t.Fatal(err)
	}

	results, err := Scan(dir)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	for _, r := range results {
		if strings.Contains(r.Path, "vendor") {
			t.Errorf("vendor dir not skipped: %v", r)
		}
	}
}

// --- Classify tests ---

func TestClassify(t *testing.T) {
	tests := []struct {
		spdx string
		want string
	}{
		{"MIT", "permissive"},
		{"Apache-2.0", "permissive"},
		{"GPL-3.0", "copyleft"},
		{"LGPL-3.0", "weak-copyleft"},
		{"Unlicense", "public-domain"},
		{"Unknown-XYZ", "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.spdx, func(t *testing.T) {
			got := Classify(tt.spdx)
			if got != tt.want {
				t.Errorf("Classify(%q) = %q; want %q", tt.spdx, got, tt.want)
			}
		})
	}
}

// --- FormatReport tests ---

func TestFormatReport_Empty(t *testing.T) {
	got := FormatReport(nil)
	if got != "No licenses detected." {
		t.Errorf("FormatReport(nil) = %q; want 'No licenses detected.'", got)
	}
}

func TestFormatReport_NonEmpty(t *testing.T) {
	licenses := []LicenseInfo{
		{
			Path:       "/tmp/LICENSE",
			SPDX:       "MIT",
			Name:       "MIT License",
			Category:   "permissive",
			Confidence: 1.0,
		},
		{
			Path:       "/tmp/subdir/COPYING",
			SPDX:       "GPL-3.0",
			Name:       "GNU General Public License v3.0",
			Category:   "copyleft",
			Confidence: 0.8,
		},
	}
	got := FormatReport(licenses)
	if !strings.Contains(got, "LICENSE SCAN REPORT") {
		t.Errorf("report missing header: %s", got)
	}
	if !strings.Contains(got, "MIT License") {
		t.Errorf("report missing MIT: %s", got)
	}
	if !strings.Contains(got, "COPYLEFT") {
		t.Errorf("report missing COPYLEFT category: %s", got)
	}
}

// --- extractNpmLicense tests ---

func TestExtractNpmLicense(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{
			name:    "simple",
			content: `{"license": "MIT"}`,
			want:    "MIT",
		},
		{
			name: "multiline",
			content: `{
  "name": "foo",
  "license": "Apache-2.0"
}`,
			want: "Apache-2.0",
		},
		{
			name:    "missing",
			content: `{"name": "foo"}`,
			want:    "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractNpmLicense(tt.content)
			if got != tt.want {
				t.Errorf("extractNpmLicense = %q; want %q", got, tt.want)
			}
		})
	}
}

func TestDetectLicense_Unlicense(t *testing.T) {
	info := detectLicense("UNLICENSE", unlicenseText, "LICENSE file")
	if info == nil {
		t.Fatal("detectLicense returned nil")
	}
	if info.SPDX != "Unlicense" {
		t.Errorf("SPDX = %q; want Unlicense", info.SPDX)
	}
	if info.Category != "public-domain" {
		t.Errorf("Category = %q; want public-domain", info.Category)
	}
}
