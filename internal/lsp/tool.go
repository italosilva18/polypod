package lsp

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/costa/polypod/internal/types"
)

// Tool name constant.
const ToolLSP = "lsp"

// MaxLSPFileSizeBytes is the max file size for LSP analysis.
const MaxLSPFileSizeBytes = 10_000_000

// LSPTool exposes LSP operations as a polypod tool.
type LSPTool struct {
	manager  *LSPServerManager
	registry *DiagnosticRegistry
}

// NewLSPTool creates the LSP tool backed by the given manager.
func NewLSPTool(manager *LSPServerManager, registry *DiagnosticRegistry) types.Tool {
	t := &LSPTool{
		manager:  manager,
		registry: registry,
	}
	return types.BuildTool(types.ToolDef{
		NameFn:          func() string { return ToolLSP },
		DescriptionFn:   t.description,
		InputSchemaFn:   t.inputSchema,
		ExecuteFn:       t.execute,
		ValidateFn:      t.validate,
		IsReadOnlyFn:    func(map[string]any) bool { return true },
		IsConcurrencyFn: func(map[string]any) bool { return true },
		IsEnabledFn:     t.isEnabled,
	})
}

func (t *LSPTool) isEnabled() bool {
	return t.manager != nil && t.manager.HasConfiguredServers()
}

func (t *LSPTool) description() string {
	return `Interact with Language Server Protocol (LSP) servers for code intelligence.

Supported operations:
- goto_definition: Find where a symbol is defined
- find_references: Find all references to a symbol
- hover: Get hover information (documentation, type info) for a symbol
- document_symbols: Get all symbols in a document
- workspace_symbols: Search for symbols across the workspace
- goto_implementation: Find implementations of an interface or abstract method
- diagnostics: Get diagnostics (errors/warnings) for a file
- prepare_call_hierarchy: Get call hierarchy item at a position
- incoming_calls: Find all callers of a function
- outgoing_calls: Find all functions called by a function

All position-based operations require: file_path, line (1-based), character (1-based).
The action field selects the operation.`
}

func (t *LSPTool) inputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action": map[string]any{
				"type": "string",
				"enum": []string{
					"goto_definition", "find_references", "hover",
					"document_symbols", "workspace_symbols",
					"goto_implementation", "diagnostics",
					"prepare_call_hierarchy", "incoming_calls", "outgoing_calls",
				},
				"description": "The LSP operation to perform",
			},
			"file_path": map[string]any{
				"type":        "string",
				"description": "Path to the file",
			},
			"line": map[string]any{
				"type":        "integer",
				"description": "Line number (1-based)",
			},
			"character": map[string]any{
				"type":        "integer",
				"description": "Character offset (1-based)",
			},
			"query": map[string]any{
				"type":        "string",
				"description": "Search query for workspace_symbols",
			},
			"severity_filter": map[string]any{
				"type":        "string",
				"description": "Filter diagnostics by severity: Error, Warning, Info, Hint",
			},
		},
		"required": []string{"action", "file_path"},
	}
}

func (t *LSPTool) validate(args map[string]any) types.ValidationResult {
	action, _ := args["action"].(string)
	filePath, _ := args["file_path"].(string)

	if action == "" {
		return types.ValidationResult{Valid: false, Message: "action is required"}
	}
	if filePath == "" {
		return types.ValidationResult{Valid: false, Message: "file_path is required"}
	}

	validActions := map[string]bool{
		"goto_definition": true, "find_references": true, "hover": true,
		"document_symbols": true, "workspace_symbols": true,
		"goto_implementation": true, "diagnostics": true,
		"prepare_call_hierarchy": true, "incoming_calls": true, "outgoing_calls": true,
	}
	if !validActions[action] {
		return types.ValidationResult{Valid: false, Message: fmt.Sprintf("invalid action: %s", action)}
	}

	// Position-based operations require line and character
	needsPosition := map[string]bool{
		"goto_definition": true, "find_references": true, "hover": true,
		"goto_implementation": true, "prepare_call_hierarchy": true,
		"incoming_calls": true, "outgoing_calls": true,
	}
	if needsPosition[action] {
		if _, ok := args["line"]; !ok {
			return types.ValidationResult{Valid: false, Message: "line is required for " + action}
		}
		if _, ok := args["character"]; !ok {
			return types.ValidationResult{Valid: false, Message: "character is required for " + action}
		}
	}

	// Check file exists
	absPath := resolvePath(filePath)
	info, err := os.Stat(absPath)
	if os.IsNotExist(err) {
		return types.ValidationResult{Valid: false, Message: fmt.Sprintf("file does not exist: %s", filePath)}
	}
	if err != nil {
		return types.ValidationResult{Valid: false, Message: fmt.Sprintf("cannot access file: %s", filePath)}
	}
	if info.IsDir() {
		return types.ValidationResult{Valid: false, Message: fmt.Sprintf("path is a directory: %s", filePath)}
	}
	if info.Size() > MaxLSPFileSizeBytes {
		return types.ValidationResult{Valid: false, Message: fmt.Sprintf("file too large for LSP: %dMB exceeds 10MB limit", info.Size()/1_000_000)}
	}

	return types.ValidationResult{Valid: true}
}

func (t *LSPTool) execute(ctx types.ToolContext, args map[string]any) (types.ToolResult, error) {
	action, _ := args["action"].(string)
	filePath, _ := args["file_path"].(string)
	absPath := resolvePath(filePath)

	// Ensure file is open
	if !t.manager.IsFileOpen(absPath) {
		content, err := os.ReadFile(absPath)
		if err != nil {
			return types.ToolResult{Content: fmt.Sprintf("Error reading file: %v", err), IsError: true}, nil
		}
		if err := t.manager.OpenFile(absPath, string(content)); err != nil {
			return types.ToolResult{Content: fmt.Sprintf("Error opening file in LSP: %v", err), IsError: true}, nil
		}
	}

	fileURI := pathToFileURI(absPath)
	line := toInt(args["line"]) - 1      // Convert to 0-based
	char := toInt(args["character"]) - 1 // Convert to 0-based
	position := Position{Line: line, Character: char}

	switch action {
	case "goto_definition":
		return t.gotoDefinition(absPath, fileURI, position)
	case "find_references":
		return t.findReferences(absPath, fileURI, position)
	case "hover":
		return t.doHover(absPath, fileURI, position)
	case "document_symbols":
		return t.documentSymbols(absPath, fileURI)
	case "workspace_symbols":
		query, _ := args["query"].(string)
		return t.workspaceSymbols(absPath, query)
	case "goto_implementation":
		return t.gotoImplementation(absPath, fileURI, position)
	case "diagnostics":
		filter, _ := args["severity_filter"].(string)
		return t.getDiagnostics(filter)
	case "prepare_call_hierarchy":
		return t.prepareCallHierarchy(absPath, fileURI, position)
	case "incoming_calls":
		return t.callHierarchyCalls(absPath, fileURI, position, "callHierarchy/incomingCalls", true)
	case "outgoing_calls":
		return t.callHierarchyCalls(absPath, fileURI, position, "callHierarchy/outgoingCalls", false)
	default:
		return types.ToolResult{Content: "Unknown action: " + action, IsError: true}, nil
	}
}

func (t *LSPTool) gotoDefinition(absPath, fileURI string, pos Position) (types.ToolResult, error) {
	params := TextDocumentPositionParams{
		TextDocument: TextDocumentIdentifier{URI: fileURI},
		Position:     pos,
	}
	raw, err := t.manager.SendRequest(absPath, "textDocument/definition", params)
	if err != nil {
		return types.ToolResult{Content: fmt.Sprintf("Error: %v", err), IsError: true}, nil
	}
	if raw == nil {
		return types.ToolResult{Content: "No LSP server available for this file type"}, nil
	}

	locations := parseLocations(raw)
	if len(locations) == 0 {
		return types.ToolResult{Content: "No definition found."}, nil
	}
	return types.ToolResult{Content: formatLocations("definition", locations)}, nil
}

func (t *LSPTool) findReferences(absPath, fileURI string, pos Position) (types.ToolResult, error) {
	params := map[string]any{
		"textDocument": TextDocumentIdentifier{URI: fileURI},
		"position":     pos,
		"context":      map[string]any{"includeDeclaration": true},
	}
	raw, err := t.manager.SendRequest(absPath, "textDocument/references", params)
	if err != nil {
		return types.ToolResult{Content: fmt.Sprintf("Error: %v", err), IsError: true}, nil
	}
	if raw == nil {
		return types.ToolResult{Content: "No LSP server available for this file type"}, nil
	}

	var locations []Location
	if err := json.Unmarshal(raw, &locations); err != nil {
		return types.ToolResult{Content: "No references found."}, nil
	}
	if len(locations) == 0 {
		return types.ToolResult{Content: "No references found."}, nil
	}

	// Group by file
	grouped := groupLocationsByFile(locations)
	var sb strings.Builder
	fmt.Fprintf(&sb, "Found %d reference(s) across %d file(s):", len(locations), len(grouped))
	for file, locs := range grouped {
		fmt.Fprintf(&sb, "\n\n%s:", file)
		for _, loc := range locs {
			fmt.Fprintf(&sb, "\n  Line %d:%d", loc.Range.Start.Line+1, loc.Range.Start.Character+1)
		}
	}
	return types.ToolResult{Content: sb.String()}, nil
}

func (t *LSPTool) doHover(absPath, fileURI string, pos Position) (types.ToolResult, error) {
	params := TextDocumentPositionParams{
		TextDocument: TextDocumentIdentifier{URI: fileURI},
		Position:     pos,
	}
	raw, err := t.manager.SendRequest(absPath, "textDocument/hover", params)
	if err != nil {
		return types.ToolResult{Content: fmt.Sprintf("Error: %v", err), IsError: true}, nil
	}
	if raw == nil {
		return types.ToolResult{Content: "No LSP server available for this file type"}, nil
	}

	var hover Hover
	if err := json.Unmarshal(raw, &hover); err != nil || hover.Contents.Value == "" {
		return types.ToolResult{Content: "No hover information available."}, nil
	}

	content := hover.Contents.Value
	if hover.Range != nil {
		content = fmt.Sprintf("Hover info at %d:%d:\n\n%s",
			hover.Range.Start.Line+1, hover.Range.Start.Character+1, content)
	}
	return types.ToolResult{Content: content}, nil
}

func (t *LSPTool) documentSymbols(absPath, fileURI string) (types.ToolResult, error) {
	params := map[string]any{
		"textDocument": TextDocumentIdentifier{URI: fileURI},
	}
	raw, err := t.manager.SendRequest(absPath, "textDocument/documentSymbol", params)
	if err != nil {
		return types.ToolResult{Content: fmt.Sprintf("Error: %v", err), IsError: true}, nil
	}
	if raw == nil {
		return types.ToolResult{Content: "No LSP server available for this file type"}, nil
	}

	// Try DocumentSymbol[] first (hierarchical)
	var docSymbols []DocumentSymbol
	if err := json.Unmarshal(raw, &docSymbols); err == nil && len(docSymbols) > 0 {
		var sb strings.Builder
		sb.WriteString("Document symbols:\n")
		formatDocSymbols(&sb, docSymbols, 0)
		return types.ToolResult{Content: sb.String()}, nil
	}

	// Try SymbolInformation[] (flat)
	var symInfos []SymbolInformation
	if err := json.Unmarshal(raw, &symInfos); err == nil && len(symInfos) > 0 {
		var sb strings.Builder
		fmt.Fprintf(&sb, "Found %d symbol(s):\n", len(symInfos))
		for _, s := range symInfos {
			line := s.Location.Range.Start.Line + 1
			container := ""
			if s.ContainerName != "" {
				container = fmt.Sprintf(" in %s", s.ContainerName)
			}
			fmt.Fprintf(&sb, "  %s (%s) - Line %d%s\n", s.Name, s.Kind.String(), line, container)
		}
		return types.ToolResult{Content: sb.String()}, nil
	}

	return types.ToolResult{Content: "No symbols found."}, nil
}

func (t *LSPTool) workspaceSymbols(absPath, query string) (types.ToolResult, error) {
	params := map[string]any{"query": query}
	raw, err := t.manager.SendRequest(absPath, "workspace/symbol", params)
	if err != nil {
		return types.ToolResult{Content: fmt.Sprintf("Error: %v", err), IsError: true}, nil
	}
	if raw == nil {
		return types.ToolResult{Content: "No LSP server available for this file type"}, nil
	}

	var symbols []SymbolInformation
	if err := json.Unmarshal(raw, &symbols); err != nil || len(symbols) == 0 {
		return types.ToolResult{Content: "No symbols found in workspace."}, nil
	}

	// Group by file
	grouped := make(map[string][]SymbolInformation)
	for _, s := range symbols {
		file := formatURI(s.Location.URI)
		grouped[file] = append(grouped[file], s)
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Found %d symbol(s) in workspace:", len(symbols))
	for file, syms := range grouped {
		fmt.Fprintf(&sb, "\n\n%s:", file)
		for _, s := range syms {
			line := s.Location.Range.Start.Line + 1
			fmt.Fprintf(&sb, "\n  %s (%s) - Line %d", s.Name, s.Kind.String(), line)
		}
	}
	return types.ToolResult{Content: sb.String()}, nil
}

func (t *LSPTool) gotoImplementation(absPath, fileURI string, pos Position) (types.ToolResult, error) {
	params := TextDocumentPositionParams{
		TextDocument: TextDocumentIdentifier{URI: fileURI},
		Position:     pos,
	}
	raw, err := t.manager.SendRequest(absPath, "textDocument/implementation", params)
	if err != nil {
		return types.ToolResult{Content: fmt.Sprintf("Error: %v", err), IsError: true}, nil
	}
	if raw == nil {
		return types.ToolResult{Content: "No LSP server available for this file type"}, nil
	}

	locations := parseLocations(raw)
	if len(locations) == 0 {
		return types.ToolResult{Content: "No implementations found."}, nil
	}
	return types.ToolResult{Content: formatLocations("implementation", locations)}, nil
}

func (t *LSPTool) getDiagnostics(severityFilter string) (types.ToolResult, error) {
	if t.registry == nil {
		return types.ToolResult{Content: "Diagnostic registry not available."}, nil
	}
	files := t.registry.CheckPending()
	if len(files) == 0 {
		return types.ToolResult{Content: "No pending diagnostics."}, nil
	}
	return types.ToolResult{Content: FormatDiagnostics(files, severityFilter)}, nil
}

func (t *LSPTool) prepareCallHierarchy(absPath, fileURI string, pos Position) (types.ToolResult, error) {
	params := TextDocumentPositionParams{
		TextDocument: TextDocumentIdentifier{URI: fileURI},
		Position:     pos,
	}
	raw, err := t.manager.SendRequest(absPath, "textDocument/prepareCallHierarchy", params)
	if err != nil {
		return types.ToolResult{Content: fmt.Sprintf("Error: %v", err), IsError: true}, nil
	}
	if raw == nil {
		return types.ToolResult{Content: "No LSP server available for this file type"}, nil
	}

	var items []CallHierarchyItem
	if err := json.Unmarshal(raw, &items); err != nil || len(items) == 0 {
		return types.ToolResult{Content: "No call hierarchy item found at this position."}, nil
	}

	var sb strings.Builder
	for _, item := range items {
		file := formatURI(item.URI)
		fmt.Fprintf(&sb, "%s (%s) - %s:%d\n", item.Name, item.Kind.String(), file, item.Range.Start.Line+1)
	}
	return types.ToolResult{Content: sb.String()}, nil
}

func (t *LSPTool) callHierarchyCalls(absPath, fileURI string, pos Position, callMethod string, isIncoming bool) (types.ToolResult, error) {
	// Step 1: prepare call hierarchy
	prepParams := TextDocumentPositionParams{
		TextDocument: TextDocumentIdentifier{URI: fileURI},
		Position:     pos,
	}
	raw, err := t.manager.SendRequest(absPath, "textDocument/prepareCallHierarchy", prepParams)
	if err != nil {
		return types.ToolResult{Content: fmt.Sprintf("Error: %v", err), IsError: true}, nil
	}
	if raw == nil {
		return types.ToolResult{Content: "No LSP server available for this file type"}, nil
	}

	var items []CallHierarchyItem
	if err := json.Unmarshal(raw, &items); err != nil || len(items) == 0 {
		return types.ToolResult{Content: "No call hierarchy item found at this position."}, nil
	}

	// Step 2: get calls using the first item
	callParams := map[string]any{"item": items[0]}
	callRaw, err := t.manager.SendRequest(absPath, callMethod, callParams)
	if err != nil {
		return types.ToolResult{Content: fmt.Sprintf("Error: %v", err), IsError: true}, nil
	}
	if callRaw == nil {
		return types.ToolResult{Content: "No calls found."}, nil
	}

	if isIncoming {
		var calls []CallHierarchyIncomingCall
		if err := json.Unmarshal(callRaw, &calls); err != nil || len(calls) == 0 {
			return types.ToolResult{Content: "No incoming calls found."}, nil
		}
		var sb strings.Builder
		fmt.Fprintf(&sb, "Found %d incoming call(s):", len(calls))
		for _, call := range calls {
			file := formatURI(call.From.URI)
			line := call.From.Range.Start.Line + 1
			fmt.Fprintf(&sb, "\n  %s (%s) - %s:%d", call.From.Name, call.From.Kind.String(), file, line)
			if len(call.FromRanges) > 0 {
				sites := make([]string, len(call.FromRanges))
				for i, r := range call.FromRanges {
					sites[i] = fmt.Sprintf("%d:%d", r.Start.Line+1, r.Start.Character+1)
				}
				fmt.Fprintf(&sb, " [calls at: %s]", strings.Join(sites, ", "))
			}
		}
		return types.ToolResult{Content: sb.String()}, nil
	}

	// Outgoing
	var calls []CallHierarchyOutgoingCall
	if err := json.Unmarshal(callRaw, &calls); err != nil || len(calls) == 0 {
		return types.ToolResult{Content: "No outgoing calls found."}, nil
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "Found %d outgoing call(s):", len(calls))
	for _, call := range calls {
		file := formatURI(call.To.URI)
		line := call.To.Range.Start.Line + 1
		fmt.Fprintf(&sb, "\n  %s (%s) - %s:%d", call.To.Name, call.To.Kind.String(), file, line)
		if len(call.FromRanges) > 0 {
			sites := make([]string, len(call.FromRanges))
			for i, r := range call.FromRanges {
				sites[i] = fmt.Sprintf("%d:%d", r.Start.Line+1, r.Start.Character+1)
			}
			fmt.Fprintf(&sb, " [called from: %s]", strings.Join(sites, ", "))
		}
	}
	return types.ToolResult{Content: sb.String()}, nil
}

// --- Helper functions ---

func resolvePath(p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	return abs
}

func toInt(v any) int {
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

// parseLocations handles Location, Location[], LocationLink, LocationLink[] responses.
func parseLocations(raw json.RawMessage) []Location {
	// Try as Location array
	var locs []Location
	if err := json.Unmarshal(raw, &locs); err == nil && len(locs) > 0 {
		return locs
	}

	// Try as single Location
	var loc Location
	if err := json.Unmarshal(raw, &loc); err == nil && loc.URI != "" {
		return []Location{loc}
	}

	// Try as LocationLink array
	var links []LocationLink
	if err := json.Unmarshal(raw, &links); err == nil && len(links) > 0 {
		result := make([]Location, len(links))
		for i, l := range links {
			result[i] = Location{URI: l.TargetURI, Range: l.TargetSelectionRange}
		}
		return result
	}

	// Try as single LocationLink
	var link LocationLink
	if err := json.Unmarshal(raw, &link); err == nil && link.TargetURI != "" {
		return []Location{{URI: link.TargetURI, Range: link.TargetSelectionRange}}
	}

	return nil
}

func formatLocations(label string, locations []Location) string {
	if len(locations) == 1 {
		loc := locations[0]
		return fmt.Sprintf("Defined in %s:%d:%d",
			formatURI(loc.URI), loc.Range.Start.Line+1, loc.Range.Start.Character+1)
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Found %d %s(s):", len(locations), label)
	for _, loc := range locations {
		fmt.Fprintf(&sb, "\n  %s:%d:%d",
			formatURI(loc.URI), loc.Range.Start.Line+1, loc.Range.Start.Character+1)
	}
	return sb.String()
}

func groupLocationsByFile(locations []Location) map[string][]Location {
	grouped := make(map[string][]Location)
	for _, loc := range locations {
		file := formatURI(loc.URI)
		grouped[file] = append(grouped[file], loc)
	}
	return grouped
}

func formatURI(uri string) string {
	if strings.HasPrefix(uri, "file://") {
		return fileURIToPath(uri)
	}
	return uri
}

func formatDocSymbols(sb *strings.Builder, symbols []DocumentSymbol, indent int) {
	prefix := strings.Repeat("  ", indent)
	for _, s := range symbols {
		line := s.Range.Start.Line + 1
		detail := ""
		if s.Detail != "" {
			detail = " " + s.Detail
		}
		fmt.Fprintf(sb, "%s%s (%s)%s - Line %d\n", prefix, s.Name, s.Kind.String(), detail, line)
		if len(s.Children) > 0 {
			formatDocSymbols(sb, s.Children, indent+1)
		}
	}
}
