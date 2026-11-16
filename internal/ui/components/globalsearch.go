// Package components — Global Search Dialog
package components

import (
	"fmt"
	"strings"
)

// GlobalSearchDialog represents a global search UI
type GlobalSearchDialog struct {
	query      string
	results    []SearchResult
	selected   int
	maxVisible int
	isOpen     bool
}

// SearchResult represents a search result
type SearchResult struct {
	FilePath  string
	LineNum   int
	Content   string
	MatchType string
}

// NewGlobalSearchDialog creates new search dialog
func NewGlobalSearchDialog() *GlobalSearchDialog {
	return &GlobalSearchDialog{
		maxVisible: 10,
		results:    make([]SearchResult, 0),
	}
}

// Open opens the dialog
func (d *GlobalSearchDialog) Open() {
	d.isOpen = true
	d.selected = 0
}

// Close closes the dialog
func (d *GlobalSearchDialog) Close() {
	d.isOpen = false
}

// SetQuery sets search query
func (d *GlobalSearchDialog) SetQuery(query string) {
	d.query = query
}

// AddResult adds a search result
func (d *GlobalSearchDialog) AddResult(result SearchResult) {
	d.results = append(d.results, result)
}

// Up moves selection up
func (d *GlobalSearchDialog) Up() {
	if d.selected > 0 {
		d.selected--
	}
}

// Down moves selection down
func (d *GlobalSearchDialog) Down() {
	if d.selected < len(d.results)-1 {
		d.selected++
	}
}

// Render renders the dialog
func (d *GlobalSearchDialog) Render() string {
	if !d.isOpen {
		return ""
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("🔍 Search: %s\n\n", d.query))
	
	for i, result := range d.results {
		prefix := "  "
		if i == d.selected {
			prefix = "👉 "
		}
		sb.WriteString(fmt.Sprintf("%s%s:%d - %s\n", 
			prefix, result.FilePath, result.LineNum, 
			truncate(result.Content, 60)))
	}
	
	return sb.String()
}

// GetSelected returns selected result
func (d *GlobalSearchDialog) GetSelected() *SearchResult {
	if len(d.results) == 0 {
		return nil
	}
	return &d.results[d.selected]
}

// Clear clears all results
func (d *GlobalSearchDialog) Clear() {
	d.results = make([]SearchResult, 0)
	d.selected = 0
}

// IsOpen returns dialog state
func (d *GlobalSearchDialog) IsOpen() bool {
	return d.isOpen
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
