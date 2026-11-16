// Package lsp — Language Server Protocol extracted from Claude Code
// Sources: src/services/lsp/

package lsp

import (
	"fmt"
	"strings"
)

// LSPSymbol represents a code symbol
type LSPSymbol struct {
	Name      string `json:"name"`
	Kind      string `json:"kind"` // function, class, variable, etc.
	FilePath  string `json:"file_path"`
	Line      int    `json:"line"`
	Column    int    `json:"column"`
}

// LSPDiagnostic represents a diagnostic issue
type LSPDiagnostic struct {
	FilePath  string `json:"file_path"`
	Line      int    `json:"line"`
	Message   string `json:"message"`
	Severity  string `json:"severity"` // error, warning, info, hint
	Source    string `json:"source"`
}

// LSPCompletion represents a completion item
type LSPCompletion struct {
	Label    string `json:"label"`
	Kind     string `json:"kind"`
	Detail   string `json:"detail"`
	InsertText string `json:"insert_text"`
}

// LSPClient represents LSP client connection
type LSPClient struct {
	ServerName string `json:"server_name"`
	Connected  bool   `json:"connected"`
	RootPath   string `json:"root_path"`
}

// LSPManager manages LSP connections
type LSPManager struct {
	clients map[string]*LSPClient
}

// NewLSPManager creates new LSP manager
func NewLSPManager() *LSPManager {
	return &LSPManager{
		clients: make(map[string]*LSPClient),
	}
}

// Connect connects to LSP server
func (m *LSPManager) Connect(serverName, rootPath string) error {
	m.clients[serverName] = &LSPClient{
		ServerName: serverName,
		Connected:  true,
		RootPath:   rootPath,
	}
	return nil
}

// Disconnect disconnects from LSP server
func (m *LSPManager) Disconnect(serverName string) error {
	client, exists := m.clients[serverName]
	if !exists {
		return fmt.Errorf("client not found")
	}
	client.Connected = false
	return nil
}

// FindDefinition finds symbol definition
func (m *LSPManager) FindDefinition(filePath string, line, column int) (*LSPSymbol, error) {
	// Placeholder - full implementation needs LSP protocol
	return &LSPSymbol{
		Name:     "definition",
		Kind:     "function",
		FilePath: filePath,
		Line:     line,
		Column:   column,
	}, nil
}

// FindReferences finds symbol references
func (m *LSPManager) FindReferences(filePath string, line, column int) ([]LSPSymbol, error) {
	// Placeholder
	return []LSPSymbol{
		{Name: "ref1", Kind: "reference", FilePath: filePath, Line: line},
		{Name: "ref2", Kind: "reference", FilePath: filePath, Line: line + 5},
	}, nil
}

// GetDiagnostics gets diagnostics for file
func (m *LSPManager) GetDiagnostics(filePath string) ([]LSPDiagnostic, error) {
	// Placeholder
	return []LSPDiagnostic{
		{FilePath: filePath, Line: 10, Message: "Unused variable", Severity: "warning"},
	}, nil
}

// GetCompletions gets completions at position
func (m *LSPManager) GetCompletions(filePath string, line, column int) ([]LSPCompletion, error) {
	// Placeholder
	return []LSPCompletion{
		{Label: "function1", Kind: "function", Detail: "Function detail"},
		{Label: "variable1", Kind: "variable", Detail: "Variable detail"},
	}, nil
}

// FormatDiagnostics formats diagnostics for display
func FormatDiagnostics(diagnostics []LSPDiagnostic) string {
	var sb strings.Builder
	
	sb.WriteString("🔍 **Diagnostics**:\n\n")
	
	for _, diag := range diagnostics {
		icon := "ℹ️"
		switch diag.Severity {
		case "error":
			icon = "❌"
		case "warning":
			icon = "⚠️"
		case "hint":
			icon = "💡"
		}
		
		sb.WriteString(fmt.Sprintf("%s Line %d: %s\n", icon, diag.Line, diag.Message))
	}
	
	return sb.String()
}

// FormatCompletions formats completions for display
func FormatCompletions(completions []LSPCompletion) string {
	var sb strings.Builder
	
	sb.WriteString("💡 **Completions**:\n\n")
	
	for i, comp := range completions {
		sb.WriteString(fmt.Sprintf("%d. **%s** (%s)\n", i+1, comp.Label, comp.Kind))
		if comp.Detail != "" {
			sb.WriteString(fmt.Sprintf("   %s\n", comp.Detail))
		}
	}
	
	return sb.String()
}
