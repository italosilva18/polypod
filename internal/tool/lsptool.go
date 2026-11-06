// Package tool: lsptool.go
//
// The "lsp" tool is a bridge between the polypod tool system and the
// Language Server Protocol support in internal/lsp. It exposes a small,
// LLM-friendly subset of LSP operations (definition, references, hover,
// symbols) against whatever language servers the host process has
// configured.
//
// The internal/lsp package already ships a richer LSPTool implementation
// wired directly into a *lsp.LSPServerManager, but some callers want a
// simpler surface area with a different operation vocabulary and the
// ability to plug in a fake LSP client during tests. This file provides
// exactly that: it accepts an LSPClient interface (satisfied by
// *lsp.LSPServerManager) and maps the user-facing operation names onto
// LSP JSON-RPC method calls.
package tool

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/costa/polypod/internal/lsp"
	"github.com/costa/polypod/internal/types"
)

// ToolNameLSP is the name under which the bridge tool registers itself.
const ToolNameLSP = "lsp"

// LSPClient is the minimal surface the bridge tool needs. It is satisfied
// by *lsp.LSPServerManager but kept as an interface so tests can inject a
// fake implementation without spawning a real language server.
type LSPClient interface {
	// IsConnected reports whether at least one LSP server is currently
	// usable.
	IsConnected() bool

	// OpenFile notifies the underlying server about a file's contents
	// so subsequent requests can resolve positions. Callers should pass
	// an absolute path whenever possible.
	OpenFile(filePath, content string) error

	// SendRequest dispatches a JSON-RPC request (e.g. "textDocument/definition")
	// to the server responsible for filePath and returns the raw response
	// payload.
	SendRequest(filePath, method string, params any) (json.RawMessage, error)
}

// Compile-time assertion: the real manager satisfies our interface.
var _ LSPClient = (*lsp.LSPServerManager)(nil)

// NewLSPTool builds a types.Tool that dispatches LSP operations through
// the provided client. Passing a nil client is allowed; the tool will
// report itself as disabled and every Execute call will return a friendly
// "not available" error.
func NewLSPTool(client LSPClient) types.Tool {
	return types.BuildTool(types.ToolDef{
		NameFn:        func() string { return ToolNameLSP },
		DescriptionFn: lspDescription,
		InputSchemaFn: lspInputSchema,
		ValidateFn:    lspValidate,
		IsReadOnlyFn:  func(map[string]any) bool { return true },
		// Read-only LSP queries are safe to run concurrently.
		IsConcurrencyFn: func(map[string]any) bool { return true },
		IsEnabledFn: func() bool {
			return client != nil && client.IsConnected()
		},
		ExecuteFn: func(ctx types.ToolContext, args map[string]any) (types.ToolResult, error) {
			return lspExecute(client, ctx, args)
		},
	})
}

// RegisterLSPTool constructs the LSP bridge tool and registers it with
// the given registry. It is a thin convenience wrapper that mirrors the
// RegisterBashTool pattern used elsewhere in this package.
func RegisterLSPTool(reg *Registry, client LSPClient) types.Tool {
	t := NewLSPTool(client)
	reg.Register(t)
	return t
}

// lspDescription returns the tool description shown to the LLM.
func lspDescription() string {
	return `Query a Language Server Protocol (LSP) server for code intelligence.

Supported operations:
  - definition: jump to the definition of a symbol at file:line:character
  - references: list all references to a symbol at file:line:character
  - hover:      fetch hover info (type signature, docs) at file:line:character
  - symbols:    search workspace symbols matching a query string

Positions are 1-based to match what editors display. The tool is read-only
and safe to call concurrently. If no LSP server is configured for the target
file type, the tool returns a friendly error rather than failing the task.`
}

// lspInputSchema declares the JSON schema for the tool input. We use a
// single flat schema (rather than a discriminated union) because most
// OpenAI-compatible function-calling APIs don't understand discriminators.
func lspInputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"operation": map[string]any{
				"type":        "string",
				"enum":        []string{"definition", "references", "hover", "symbols"},
				"description": "Which LSP operation to perform.",
			},
			"file": map[string]any{
				"type":        "string",
				"description": "Path to the source file. Required for definition, references, hover.",
			},
			"line": map[string]any{
				"type":        "integer",
				"description": "Line number (1-based). Required for definition, references, hover.",
				"minimum":     1,
			},
			"character": map[string]any{
				"type":        "integer",
				"description": "Character offset within the line (1-based). Required for definition, references, hover.",
				"minimum":     1,
			},
			"query": map[string]any{
				"type":        "string",
				"description": "Symbol search query. Required for the 'symbols' operation.",
			},
		},
		"required": []string{"operation"},
	}
}

// lspValidate enforces the cross-field requirements of the schema.
func lspValidate(args map[string]any) types.ValidationResult {
	op, _ := args["operation"].(string)
	if op == "" {
		return types.ValidationResult{Valid: false, Message: "operation is required"}
	}

	switch op {
	case "definition", "references", "hover":
		file, _ := args["file"].(string)
		if strings.TrimSpace(file) == "" {
			return types.ValidationResult{
				Valid:   false,
				Message: fmt.Sprintf("file is required for operation %q", op),
			}
		}
		if _, ok := args["line"]; !ok {
			return types.ValidationResult{
				Valid:   false,
				Message: fmt.Sprintf("line is required for operation %q", op),
			}
		}
		if _, ok := args["character"]; !ok {
			return types.ValidationResult{
				Valid:   false,
				Message: fmt.Sprintf("character is required for operation %q", op),
			}
		}
	case "symbols":
		query, _ := args["query"].(string)
		if strings.TrimSpace(query) == "" {
			return types.ValidationResult{
				Valid:   false,
				Message: "query is required for operation \"symbols\"",
			}
		}
	default:
		return types.ValidationResult{
			Valid:   false,
			Message: fmt.Sprintf("unknown operation %q (expected definition, references, hover, or symbols)", op),
		}
	}

	return types.ValidationResult{Valid: true}
}

// lspExecute dispatches the operation to the underlying client and
// formats the response as a text blob.
func lspExecute(client LSPClient, _ types.ToolContext, args map[string]any) (types.ToolResult, error) {
	if client == nil {
		return types.ToolResult{
			Content: "LSP is not configured in this environment. No language servers are available.",
			IsError: true,
		}, nil
	}
	if !client.IsConnected() {
		return types.ToolResult{
			Content: "No LSP server is currently running. Start a language server (for example gopls) and try again.",
			IsError: true,
		}, nil
	}

	op, _ := args["operation"].(string)

	switch op {
	case "definition":
		return lspRunDefinition(client, args)
	case "references":
		return lspRunReferences(client, args)
	case "hover":
		return lspRunHover(client, args)
	case "symbols":
		return lspRunSymbols(client, args)
	default:
		return types.ToolResult{
			Content: fmt.Sprintf("Unknown LSP operation %q.", op),
			IsError: true,
		}, nil
	}
}

// --- operation handlers ---------------------------------------------------

func lspRunDefinition(client LSPClient, args map[string]any) (types.ToolResult, error) {
	absPath, pos, errRes, ok := lspResolvePosition(client, args)
	if !ok {
		return errRes, nil
	}
	params := lsp.TextDocumentPositionParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: lspPathToURI(absPath)},
		Position:     pos,
	}
	raw, err := client.SendRequest(absPath, "textDocument/definition", params)
	if err != nil {
		return types.ToolResult{Content: fmt.Sprintf("LSP error: %v", err), IsError: true}, nil
	}
	if len(raw) == 0 || string(raw) == "null" {
		return types.ToolResult{Content: "No definition found."}, nil
	}

	locations := lspParseLocations(raw)
	if len(locations) == 0 {
		return types.ToolResult{Content: "No definition found."}, nil
	}
	return types.ToolResult{Content: lspFormatLocations("definition", locations)}, nil
}

func lspRunReferences(client LSPClient, args map[string]any) (types.ToolResult, error) {
	absPath, pos, errRes, ok := lspResolvePosition(client, args)
	if !ok {
		return errRes, nil
	}
	params := map[string]any{
		"textDocument": lsp.TextDocumentIdentifier{URI: lspPathToURI(absPath)},
		"position":     pos,
		"context":      map[string]any{"includeDeclaration": true},
	}
	raw, err := client.SendRequest(absPath, "textDocument/references", params)
	if err != nil {
		return types.ToolResult{Content: fmt.Sprintf("LSP error: %v", err), IsError: true}, nil
	}
	if len(raw) == 0 || string(raw) == "null" {
		return types.ToolResult{Content: "No references found."}, nil
	}

	var locations []lsp.Location
	if err := json.Unmarshal(raw, &locations); err != nil || len(locations) == 0 {
		return types.ToolResult{Content: "No references found."}, nil
	}

	grouped := make(map[string][]lsp.Location)
	order := make([]string, 0)
	for _, loc := range locations {
		file := lspURIToDisplay(loc.URI)
		if _, seen := grouped[file]; !seen {
			order = append(order, file)
		}
		grouped[file] = append(grouped[file], loc)
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Found %d reference(s) across %d file(s):", len(locations), len(order))
	for _, file := range order {
		fmt.Fprintf(&sb, "\n\n%s:", file)
		for _, loc := range grouped[file] {
			fmt.Fprintf(&sb, "\n  %d:%d", loc.Range.Start.Line+1, loc.Range.Start.Character+1)
		}
	}
	return types.ToolResult{Content: sb.String()}, nil
}

func lspRunHover(client LSPClient, args map[string]any) (types.ToolResult, error) {
	absPath, pos, errRes, ok := lspResolvePosition(client, args)
	if !ok {
		return errRes, nil
	}
	params := lsp.TextDocumentPositionParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: lspPathToURI(absPath)},
		Position:     pos,
	}
	raw, err := client.SendRequest(absPath, "textDocument/hover", params)
	if err != nil {
		return types.ToolResult{Content: fmt.Sprintf("LSP error: %v", err), IsError: true}, nil
	}
	if len(raw) == 0 || string(raw) == "null" {
		return types.ToolResult{Content: "No hover information available."}, nil
	}

	var hover lsp.Hover
	if err := json.Unmarshal(raw, &hover); err != nil || hover.Contents.Value == "" {
		return types.ToolResult{Content: "No hover information available."}, nil
	}
	return types.ToolResult{Content: hover.Contents.Value}, nil
}

func lspRunSymbols(client LSPClient, args map[string]any) (types.ToolResult, error) {
	query, _ := args["query"].(string)
	// workspace/symbol is a workspace-level request but the manager routes
	// by file extension, so we fall back to the provided file (if any) or
	// the current directory to pick a server.
	routeFile, _ := args["file"].(string)
	if strings.TrimSpace(routeFile) == "" {
		routeFile = "."
	}
	absPath := lspAbs(routeFile)

	raw, err := client.SendRequest(absPath, "workspace/symbol", map[string]any{"query": query})
	if err != nil {
		return types.ToolResult{Content: fmt.Sprintf("LSP error: %v", err), IsError: true}, nil
	}
	if len(raw) == 0 || string(raw) == "null" {
		return types.ToolResult{Content: "No symbols found in workspace."}, nil
	}

	var symbols []lsp.SymbolInformation
	if err := json.Unmarshal(raw, &symbols); err != nil || len(symbols) == 0 {
		return types.ToolResult{Content: "No symbols found in workspace."}, nil
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Found %d symbol(s) matching %q:", len(symbols), query)
	for _, s := range symbols {
		file := lspURIToDisplay(s.Location.URI)
		line := s.Location.Range.Start.Line + 1
		container := ""
		if s.ContainerName != "" {
			container = " in " + s.ContainerName
		}
		fmt.Fprintf(&sb, "\n  %s (%s)%s - %s:%d",
			s.Name, s.Kind.String(), container, file, line)
	}
	return types.ToolResult{Content: sb.String()}, nil
}

// --- helpers --------------------------------------------------------------

// lspResolvePosition reads file/line/character from args, converts the path
// to absolute form, opens the file in the LSP client if possible, and
// returns a zero-based Position. If anything goes wrong it returns an
// error ToolResult with ok=false.
func lspResolvePosition(client LSPClient, args map[string]any) (string, lsp.Position, types.ToolResult, bool) {
	file, _ := args["file"].(string)
	absPath := lspAbs(file)

	// Best-effort: push file content to the server so it knows about the
	// document. We silently ignore errors here — the request might still
	// succeed if the server already has the file loaded, and if not the
	// SendRequest call below will surface the failure.
	if content, err := os.ReadFile(absPath); err == nil {
		_ = client.OpenFile(absPath, string(content))
	}

	line := lspToInt(args["line"]) - 1
	char := lspToInt(args["character"]) - 1
	if line < 0 {
		line = 0
	}
	if char < 0 {
		char = 0
	}
	return absPath, lsp.Position{Line: line, Character: char}, types.ToolResult{}, true
}

func lspToInt(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case int64:
		return int(n)
	case json.Number:
		i, _ := n.Int64()
		return int(i)
	default:
		return 0
	}
}

func lspAbs(p string) string {
	if p == "" {
		return ""
	}
	if filepath.IsAbs(p) {
		return p
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	return abs
}

// lspPathToURI mirrors lsp.pathToFileURI (which is package-private).
func lspPathToURI(absPath string) string {
	p := filepath.ToSlash(absPath)
	if !strings.HasPrefix(p, "/") {
		return "file:///" + p
	}
	return "file://" + p
}

// lspURIToDisplay converts a file:// URI back to a friendly path. On
// parse failures it returns the URI unchanged.
func lspURIToDisplay(uri string) string {
	if !strings.HasPrefix(uri, "file://") {
		return uri
	}
	s := strings.TrimPrefix(uri, "file://")
	if len(s) >= 3 && s[0] == '/' && s[2] == ':' {
		s = s[1:]
	}
	return filepath.FromSlash(s)
}

// lspParseLocations decodes a textDocument/definition (or similar) reply
// which may be a single Location, an array of Location, a single
// LocationLink, or an array of LocationLink.
func lspParseLocations(raw json.RawMessage) []lsp.Location {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}

	var locs []lsp.Location
	if err := json.Unmarshal(raw, &locs); err == nil && len(locs) > 0 {
		return locs
	}

	var single lsp.Location
	if err := json.Unmarshal(raw, &single); err == nil && single.URI != "" {
		return []lsp.Location{single}
	}

	var links []lsp.LocationLink
	if err := json.Unmarshal(raw, &links); err == nil && len(links) > 0 {
		out := make([]lsp.Location, len(links))
		for i, l := range links {
			out[i] = lsp.Location{URI: l.TargetURI, Range: l.TargetSelectionRange}
		}
		return out
	}

	var link lsp.LocationLink
	if err := json.Unmarshal(raw, &link); err == nil && link.TargetURI != "" {
		return []lsp.Location{{URI: link.TargetURI, Range: link.TargetSelectionRange}}
	}

	return nil
}

func lspFormatLocations(label string, locations []lsp.Location) string {
	if len(locations) == 1 {
		loc := locations[0]
		return fmt.Sprintf("%s: %s:%d:%d",
			strings.Title(label), lspURIToDisplay(loc.URI),
			loc.Range.Start.Line+1, loc.Range.Start.Character+1)
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "Found %d %s(s):", len(locations), label)
	for _, loc := range locations {
		fmt.Fprintf(&sb, "\n  %s:%d:%d",
			lspURIToDisplay(loc.URI),
			loc.Range.Start.Line+1,
			loc.Range.Start.Character+1)
	}
	return sb.String()
}
