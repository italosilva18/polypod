// Package fixtures provides embedded test fixtures for tests across the project.
package fixtures

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
)

//go:embed data/*
var FS embed.FS

// Get returns the raw bytes of a fixture by name.
// Prefix "data/" is added automatically.
func Get(name string) ([]byte, error) {
	if name == "" {
		return nil, fmt.Errorf("fixtures: empty name")
	}
	data, err := FS.ReadFile("data/" + name)
	if err != nil {
		return nil, fmt.Errorf("fixtures: %w", err)
	}
	return data, nil
}

// MustGet is like Get but panics on error.
func MustGet(name string) []byte {
	data, err := Get(name)
	if err != nil {
		panic(err)
	}
	return data
}

// GetString returns a fixture as a string.
func GetString(name string) (string, error) {
	data, err := Get(name)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// MustGetString is like GetString but panics on error.
func MustGetString(name string) string {
	return string(MustGet(name))
}

// List returns all available fixture names.
func List() ([]string, error) {
	var names []string
	err := fs.WalkDir(FS, "data", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			names = append(names, path[5:]) // strip "data/" prefix
		}
		return nil
	})
	return names, err
}

// ===== Specific fixture accessors =====

// SampleGoFile returns a sample Go file for testing Go parsers.
func SampleGoFile() []byte {
	return MustGet("sample.go.txt")
}

// SampleGoFileString returns the sample Go file as a string.
func SampleGoFileString() string {
	return string(SampleGoFile())
}

// SampleMarkdown returns a sample markdown document.
func SampleMarkdown() []byte {
	return MustGet("sample.md")
}

// SampleMarkdownString returns the sample markdown as a string.
func SampleMarkdownString() string {
	return string(SampleMarkdown())
}

// SampleDiff returns a sample unified diff.
func SampleDiff() []byte {
	return MustGet("sample.diff")
}

// SampleDiffString returns the sample diff as a string.
func SampleDiffString() string {
	return string(SampleDiff())
}

// ===== Synthetic data generators (not embedded) =====

// SampleJSON returns a small synthetic JSON object.
func SampleJSON() []byte {
	data := map[string]any{
		"name":    "polypod",
		"version": "0.1.0",
		"features": []string{
			"tool-calling",
			"plan-mode",
			"compact",
		},
		"active": true,
		"count":  42,
	}
	out, _ := json.MarshalIndent(data, "", "  ")
	return out
}

// SampleMessages returns a synthetic list of chat messages.
func SampleMessages() []SampleMessage {
	return []SampleMessage{
		{Role: "system", Content: "You are a helpful assistant."},
		{Role: "user", Content: "What is the capital of France?"},
		{Role: "assistant", Content: "Paris."},
		{Role: "user", Content: "And of Germany?"},
		{Role: "assistant", Content: "Berlin."},
	}
}

// SampleMessage is a simple chat message for fixtures.
type SampleMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// SampleToolCall returns a synthetic tool call JSON.
func SampleToolCall() string {
	return `{"name":"read_file","arguments":{"path":"main.go"}}`
}

// SampleToolResult returns a synthetic tool result.
func SampleToolResult() string {
	return "package main\n\nfunc main() {\n\tprintln(\"hello\")\n}\n"
}

// SampleStackTrace returns a synthetic Go stack trace.
func SampleStackTrace() string {
	return `goroutine 1 [running]:
main.panicHere()
	/home/user/project/main.go:15 +0x30
main.main()
	/home/user/project/main.go:10 +0x25
exit status 2`
}

// SampleGitLog returns synthetic git log output.
func SampleGitLog() string {
	return `commit abc123 (HEAD -> main)
Author: Test User <test@example.com>
Date:   Mon Jan 1 12:00:00 2024 +0000

    Initial commit

commit def456
Author: Test User <test@example.com>
Date:   Sun Dec 31 12:00:00 2023 +0000

    Previous commit`
}
