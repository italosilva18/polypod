// Package ui — TUI Components extracted from Claude Code
// Sources: src/components/, src/utils/ink/

package ui

import (
	"fmt"
	"strings"
)

// Theme represents UI theme
type Theme struct {
	Name        string
	Primary     string
	Secondary   string
	Success     string
	Warning     string
	Error       string
	Background  string
	Foreground  string
}

// DefaultTheme returns default theme
func DefaultTheme() *Theme {
	return &Theme{
		Name:       "default",
		Primary:    "#007ACC",
		Secondary:  "#6C6C6C",
		Success:    "#4EC9B0",
		Warning:    "#CE9178",
		Error:      "#F44747",
		Background: "#1E1E1E",
		Foreground: "#D4D4D4",
	}
}

// FuzzyPicker performs fuzzy search
type FuzzyPicker struct {
	items      []string
	filtered   []string
	query      string
	selected   int
	scroll     int
	maxVisible int
}

// NewFuzzyPicker creates fuzzy picker
func NewFuzzyPicker(items []string) *FuzzyPicker {
	return &FuzzyPicker{
		items:      items,
		filtered:   items,
		maxVisible: 10,
	}
}

// Filter filters items by query
func (p *FuzzyPicker) Filter(query string) {
	p.query = strings.ToLower(query)
	p.filtered = make([]string, 0)
	
	for _, item := range p.items {
		if strings.Contains(strings.ToLower(item), p.query) {
			p.filtered = append(p.filtered, item)
		}
	}
	
	p.selected = 0
	p.scroll = 0
}

// Select selects item by index
func (p *FuzzyPicker) Select(index int) string {
	if index >= 0 && index < len(p.filtered) {
		return p.filtered[index]
	}
	return ""
}

// Up moves selection up
func (p *FuzzyPicker) Up() {
	if p.selected > 0 {
		p.selected--
	}
}

// Down moves selection down
func (p *FuzzyPicker) Down() {
	if p.selected < len(p.filtered)-1 {
		p.selected++
	}
}

// Render renders picker
func (p *FuzzyPicker) Render() string {
	var sb strings.Builder
	
	sb.WriteString(fmt.Sprintf("🔍 Search: %s\n\n", p.query))
	
	start := p.scroll
	end := start + p.maxVisible
	if end > len(p.filtered) {
		end = len(p.filtered)
		start = end - p.maxVisible
		if start < 0 {
			start = 0
		}
	}
	
	for i := start; i < end; i++ {
		prefix := "  "
		if i == p.selected {
			prefix = "👉 "
		}
		sb.WriteString(fmt.Sprintf("%s%s\n", prefix, p.filtered[i]))
	}
	
	return sb.String()
}

// SearchBox represents search input
type SearchBox struct {
	value      string
	placeholder string
	focused    bool
}

// NewSearchBox creates search box
func NewSearchBox(placeholder string) *SearchBox {
	return &SearchBox{
		placeholder: placeholder,
	}
}

// SetValue sets value
func (s *SearchBox) SetValue(value string) {
	s.value = value
}

// GetValue gets value
func (s *SearchBox) GetValue() string {
	return s.value
}

// Render renders search box
func (s *SearchBox) Render() string {
	if s.focused {
		return fmt.Sprintf("🔍 [%s]", s.value)
	}
	if s.value == "" {
		return fmt.Sprintf("🔍 %s", s.placeholder)
	}
	return fmt.Sprintf("🔍 %s", s.value)
}

// LoadingState represents loading indicator
type LoadingState struct {
	message string
	spinner int
}

// NewLoadingState creates loading state
func NewLoadingState(message string) *LoadingState {
	return &LoadingState{message: message}
}

// Update updates spinner
func (l *LoadingState) Update() {
	l.spinner = (l.spinner + 1) % 4
}

// Render renders loading state
func (l *LoadingState) Render() string {
	spinners := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	return fmt.Sprintf("%s %s", spinners[l.spinner], l.message)
}

// TreeSelect represents tree selection
type TreeSelect struct {
	nodes      []TreeNode
	selected   int
	expanded   map[int]bool
}

// TreeNode represents tree node
type TreeNode struct {
	Label    string
	Children []TreeNode
	Level    int
}

// NewTreeSelect creates tree select
func NewTreeSelect(nodes []TreeNode) *TreeSelect {
	return &TreeSelect{
		nodes:    nodes,
		expanded: make(map[int]bool),
	}
}

// Render renders tree
func (t *TreeSelect) Render() string {
	var sb strings.Builder
	t.renderNodes(t.nodes, 0, &sb)
	return sb.String()
}

func (t *TreeSelect) renderNodes(nodes []TreeNode, level int, sb *strings.Builder) {
	for _, node := range nodes {
		indent := strings.Repeat("  ", level)
		sb.WriteString(fmt.Sprintf("%s📁 %s\n", indent, node.Label))
		if t.expanded[level] && len(node.Children) > 0 {
			t.renderNodes(node.Children, level+1, sb)
		}
	}
}

// FormatTheme formats theme for display
func FormatTheme(theme *Theme) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("🎨 **Theme**: %s\n\n", theme.Name))
	sb.WriteString(fmt.Sprintf("Primary: %s\n", theme.Primary))
	sb.WriteString(fmt.Sprintf("Secondary: %s\n", theme.Secondary))
	sb.WriteString(fmt.Sprintf("Success: %s\n", theme.Success))
	sb.WriteString(fmt.Sprintf("Warning: %s\n", theme.Warning))
	sb.WriteString(fmt.Sprintf("Error: %s\n", theme.Error))
	return sb.String()
}
