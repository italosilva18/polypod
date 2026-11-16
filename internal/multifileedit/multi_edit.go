// Package multi_edit — Multi-file Edit
package multifileedit

type MultiFileEditor struct {
	edits []FileEdit
}

type FileEdit struct {
	Path    string
	OldText string
	NewText string
}

func NewMultiFileEditor() *MultiFileEditor {
	return &MultiFileEditor{}
}

func (e *MultiFileEditor) AddEdit(path, oldText, newText string) {
	e.edits = append(e.edits, FileEdit{Path: path, OldText: oldText, NewText: newText})
}

func (e *MultiFileEditor) Execute() error {
	for _, edit := range e.edits {
		// Execute edit
		// In real implementation, apply patch
	}
	return nil
}

func (e *MultiFileEditor) Preview() string {
	// Return preview of changes
	return "Preview of multi-file edits"
}

func (e *MultiFileEditor) GetEdits() []FileEdit {
	return e.edits
}
