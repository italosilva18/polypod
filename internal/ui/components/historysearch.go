// Package components — History Search Dialog
package components

import "strings"

// HistorySearchDialog represents history search UI
type HistorySearchDialog struct {
	query      string
	history    []HistoryEntry
	filtered   []HistoryEntry
	selected   int
	isOpen     bool
}

// HistoryEntry represents a history entry
type HistoryEntry struct {
	ID        string
	Timestamp string
	Summary   string
	Tags      []string
}

// NewHistorySearchDialog creates new history search dialog
func NewHistorySearchDialog() *HistorySearchDialog {
	return &HistorySearchDialog{
		history:  make([]HistoryEntry, 0),
		filtered: make([]HistoryEntry, 0),
	}
}

// AddEntry adds a history entry
func (d *HistorySearchDialog) AddEntry(entry HistoryEntry) {
	d.history = append(d.history, entry)
	d.filter()
}

// SetQuery sets search query
func (d *HistorySearchDialog) SetQuery(query string) {
	d.query = query
	d.filter()
}

// filter filters history by query
func (d *HistorySearchDialog) filter() {
	d.filtered = make([]HistoryEntry, 0)
	for _, entry := range d.history {
		if contains(entry.Summary, d.query) || containsTags(entry.Tags, d.query) {
			d.filtered = append(d.filtered, entry)
		}
	}
}

// Open opens the dialog
func (d *HistorySearchDialog) Open() {
	d.isOpen = true
	d.selected = 0
}

// Close closes the dialog
func (d *HistorySearchDialog) Close() {
	d.isOpen = false
}

// Up moves selection up
func (d *HistorySearchDialog) Up() {
	if d.selected > 0 {
		d.selected--
	}
}

// Down moves selection down
func (d *HistorySearchDialog) Down() {
	if d.selected < len(d.filtered)-1 {
		d.selected++
	}
}

// Render renders the dialog
func (d *HistorySearchDialog) Render() string {
	if !d.isOpen {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("📜 History Search\n\n")
	
	for i, entry := range d.filtered {
		prefix := "  "
		if i == d.selected {
			prefix = "👉 "
		}
		sb.WriteString(fmt.Sprintf("%s[%s] %s\n", prefix, entry.Timestamp, entry.Summary))
	}
	
	return sb.String()
}

// GetSelected returns selected entry
func (d *HistorySearchDialog) GetSelected() *HistoryEntry {
	if len(d.filtered) == 0 {
		return nil
	}
	return &d.filtered[d.selected]
}

func contains(s, substr string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(substr))
}

func containsTags(tags []string, substr string) bool {
	for _, tag := range tags {
		if contains(tag, substr) {
			return true
		}
	}
	return false
}
