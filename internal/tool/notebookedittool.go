package tool

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/costa/polypod/internal/types"
)

// ---------------------------------------------------------------------------
// Jupyter notebook data model
// ---------------------------------------------------------------------------

// NotebookCell represents a single cell in a Jupyter notebook.
// Unknown fields are preserved via the Extra map to survive roundtrips.
type NotebookCell struct {
	CellType       string                     `json:"cell_type"`
	ID             string                     `json:"id,omitempty"`
	Source         json.RawMessage            `json:"source"`
	Metadata       map[string]json.RawMessage `json:"metadata"`
	ExecutionCount json.RawMessage            `json:"execution_count,omitempty"`
	Outputs        []json.RawMessage          `json:"outputs,omitempty"`

	// Extra preserves unknown top-level fields on the cell.
	Extra map[string]json.RawMessage `json:"-"`
}

// knownCellFields lists the fields NotebookCell unmarshals explicitly.
var knownCellFields = map[string]bool{
	"cell_type":       true,
	"id":              true,
	"source":          true,
	"metadata":        true,
	"execution_count": true,
	"outputs":         true,
}

// UnmarshalJSON preserves unknown cell fields into the Extra map.
func (c *NotebookCell) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	c.Extra = map[string]json.RawMessage{}
	for k, v := range raw {
		switch k {
		case "cell_type":
			if err := json.Unmarshal(v, &c.CellType); err != nil {
				return fmt.Errorf("cell_type: %w", err)
			}
		case "id":
			if err := json.Unmarshal(v, &c.ID); err != nil {
				return fmt.Errorf("id: %w", err)
			}
		case "source":
			c.Source = append(json.RawMessage{}, v...)
		case "metadata":
			if err := json.Unmarshal(v, &c.Metadata); err != nil {
				return fmt.Errorf("metadata: %w", err)
			}
		case "execution_count":
			c.ExecutionCount = append(json.RawMessage{}, v...)
		case "outputs":
			if err := json.Unmarshal(v, &c.Outputs); err != nil {
				return fmt.Errorf("outputs: %w", err)
			}
		default:
			c.Extra[k] = append(json.RawMessage{}, v...)
		}
	}
	if c.Metadata == nil {
		c.Metadata = map[string]json.RawMessage{}
	}
	return nil
}

// MarshalJSON emits known fields plus any preserved unknowns, respecting the
// Jupyter conventions that code cells always carry outputs/execution_count.
func (c *NotebookCell) MarshalJSON() ([]byte, error) {
	out := map[string]json.RawMessage{}
	for k, v := range c.Extra {
		if knownCellFields[k] {
			continue
		}
		out[k] = v
	}

	ct, err := json.Marshal(c.CellType)
	if err != nil {
		return nil, err
	}
	out["cell_type"] = ct

	if c.ID != "" {
		idB, err := json.Marshal(c.ID)
		if err != nil {
			return nil, err
		}
		out["id"] = idB
	}

	metaVal := c.Metadata
	if metaVal == nil {
		metaVal = map[string]json.RawMessage{}
	}
	metaB, err := json.Marshal(metaVal)
	if err != nil {
		return nil, err
	}
	out["metadata"] = metaB

	if len(c.Source) == 0 {
		out["source"] = json.RawMessage(`""`)
	} else {
		out["source"] = c.Source
	}

	if c.CellType == "code" {
		if len(c.ExecutionCount) == 0 {
			out["execution_count"] = json.RawMessage(`null`)
		} else {
			out["execution_count"] = c.ExecutionCount
		}
		if c.Outputs == nil {
			outB, err := json.Marshal([]json.RawMessage{})
			if err != nil {
				return nil, err
			}
			out["outputs"] = outB
		} else {
			outB, err := json.Marshal(c.Outputs)
			if err != nil {
				return nil, err
			}
			out["outputs"] = outB
		}
	}

	return json.Marshal(out)
}

// Notebook is the top-level Jupyter notebook structure.
// Unknown top-level fields are preserved via Extra.
type Notebook struct {
	Cells         []NotebookCell             `json:"cells"`
	Metadata      map[string]json.RawMessage `json:"metadata"`
	NBFormat      int                        `json:"nbformat"`
	NBFormatMinor int                        `json:"nbformat_minor"`

	Extra map[string]json.RawMessage `json:"-"`
}

var knownNotebookFields = map[string]bool{
	"cells":          true,
	"metadata":       true,
	"nbformat":       true,
	"nbformat_minor": true,
}

// UnmarshalJSON preserves unknown notebook-level fields.
func (n *Notebook) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	n.Extra = map[string]json.RawMessage{}
	for k, v := range raw {
		switch k {
		case "cells":
			if err := json.Unmarshal(v, &n.Cells); err != nil {
				return fmt.Errorf("cells: %w", err)
			}
		case "metadata":
			if err := json.Unmarshal(v, &n.Metadata); err != nil {
				return fmt.Errorf("metadata: %w", err)
			}
		case "nbformat":
			if err := json.Unmarshal(v, &n.NBFormat); err != nil {
				return fmt.Errorf("nbformat: %w", err)
			}
		case "nbformat_minor":
			if err := json.Unmarshal(v, &n.NBFormatMinor); err != nil {
				return fmt.Errorf("nbformat_minor: %w", err)
			}
		default:
			n.Extra[k] = append(json.RawMessage{}, v...)
		}
	}
	if n.Metadata == nil {
		n.Metadata = map[string]json.RawMessage{}
	}
	if n.Cells == nil {
		n.Cells = []NotebookCell{}
	}
	return nil
}

// MarshalJSON serializes back, preserving unknown fields.
func (n *Notebook) MarshalJSON() ([]byte, error) {
	out := map[string]json.RawMessage{}
	for k, v := range n.Extra {
		if knownNotebookFields[k] {
			continue
		}
		out[k] = v
	}
	cellsB, err := json.Marshal(n.Cells)
	if err != nil {
		return nil, err
	}
	out["cells"] = cellsB

	metaVal := n.Metadata
	if metaVal == nil {
		metaVal = map[string]json.RawMessage{}
	}
	metaB, err := json.Marshal(metaVal)
	if err != nil {
		return nil, err
	}
	out["metadata"] = metaB

	nbfB, err := json.Marshal(n.NBFormat)
	if err != nil {
		return nil, err
	}
	out["nbformat"] = nbfB

	nbmB, err := json.Marshal(n.NBFormatMinor)
	if err != nil {
		return nil, err
	}
	out["nbformat_minor"] = nbmB

	return json.Marshal(out)
}

// LoadNotebook reads and parses a .ipynb file into a Notebook.
func LoadNotebook(path string) (*Notebook, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var nb Notebook
	if err := json.Unmarshal(data, &nb); err != nil {
		return nil, fmt.Errorf("parse notebook: %w", err)
	}
	return &nb, nil
}

// SaveNotebook writes a Notebook back to disk using Jupyter's 1-space indent.
func SaveNotebook(path string, nb *Notebook) error {
	data, err := json.MarshalIndent(nb, "", " ")
	if err != nil {
		return fmt.Errorf("marshal notebook: %w", err)
	}
	data = append(data, '\n')
	mode := os.FileMode(0644)
	if info, statErr := os.Stat(path); statErr == nil {
		mode = info.Mode()
	}
	return os.WriteFile(path, data, mode)
}

// ---------------------------------------------------------------------------
// notebook_edit tool
// ---------------------------------------------------------------------------

// NewNotebookEditTool creates the notebook_edit tool, scoped to a workspace
// directory. Paths outside the workspace are rejected via permission check.
func NewNotebookEditTool(workingDir string) types.Tool {
	return types.BuildTool(types.ToolDef{
		NameFn: func() string { return "notebook_edit" },
		DescriptionFn: func() string {
			return "Edit a cell in a Jupyter notebook (.ipynb) file. " +
				"Supports replace (default), insert, and delete edit modes. " +
				"Preserves unknown notebook fields and clears output/execution_count " +
				"when a code cell is replaced."
		},
		InputSchemaFn: func() map[string]any {
			return map[string]any{
				"type": "object",
				"properties": map[string]any{
					"notebook_path": map[string]any{
						"type":        "string",
						"description": "Path to the .ipynb file to edit",
					},
					"cell_number": map[string]any{
						"type":        "integer",
						"description": "Zero-indexed cell position to edit",
					},
					"new_source": map[string]any{
						"type":        "string",
						"description": "New cell source (required for replace/insert)",
					},
					"edit_mode": map[string]any{
						"type":        "string",
						"enum":        []string{"replace", "insert", "delete"},
						"description": "Edit mode (default: replace)",
					},
					"cell_type": map[string]any{
						"type":        "string",
						"enum":        []string{"code", "markdown"},
						"description": "Cell type — required when edit_mode is insert",
					},
				},
				"required": []string{"notebook_path", "cell_number"},
			}
		},
		IsDestructiveFn: func(map[string]any) bool { return true },
		ValidateFn: func(args map[string]any) types.ValidationResult {
			nbPath, _ := args["notebook_path"].(string)
			if nbPath == "" {
				return types.ValidationResult{Valid: false, Message: "notebook_path is required"}
			}
			if !strings.HasSuffix(strings.ToLower(nbPath), ".ipynb") {
				return types.ValidationResult{
					Valid:   false,
					Message: "notebook_path must end with .ipynb",
				}
			}
			cellNum, ok := coerceInt(args["cell_number"])
			if !ok {
				return types.ValidationResult{
					Valid:   false,
					Message: "cell_number is required and must be an integer",
				}
			}
			if cellNum < 0 {
				return types.ValidationResult{
					Valid:   false,
					Message: "cell_number must be >= 0",
				}
			}
			mode := "replace"
			if m, ok := args["edit_mode"].(string); ok && m != "" {
				mode = m
			}
			switch mode {
			case "replace", "insert", "delete":
			default:
				return types.ValidationResult{
					Valid:   false,
					Message: "edit_mode must be replace, insert, or delete",
				}
			}
			if mode == "replace" || mode == "insert" {
				if _, ok := args["new_source"].(string); !ok {
					return types.ValidationResult{
						Valid:   false,
						Message: "new_source is required for replace/insert",
					}
				}
			}
			if mode == "insert" {
				ct, _ := args["cell_type"].(string)
				if ct != "code" && ct != "markdown" {
					return types.ValidationResult{
						Valid:   false,
						Message: "cell_type (code|markdown) is required when edit_mode=insert",
					}
				}
			}
			return types.ValidationResult{Valid: true}
		},
		CheckPermFn: func(args map[string]any, tc types.ToolContext) types.PermissionResult {
			base := workingDir
			if base == "" {
				base = tc.WorkingDir
			}
			nbPath, _ := args["notebook_path"].(string)
			resolved := resolvePath(base, nbPath)
			if base != "" && !isWithinDir(base, resolved) {
				return types.PermissionResult{
					Behavior: types.PermissionDeny,
					Message:  fmt.Sprintf("notebook_path %q is outside the workspace %q", resolved, base),
				}
			}
			return types.PermissionResult{Behavior: types.PermissionAllow}
		},
		ExecuteFn: func(tc types.ToolContext, args map[string]any) (types.ToolResult, error) {
			base := workingDir
			if base == "" {
				base = tc.WorkingDir
			}
			nbPath, _ := args["notebook_path"].(string)
			path := resolvePath(base, nbPath)

			cellNum, _ := coerceInt(args["cell_number"])
			newSource, _ := args["new_source"].(string)
			mode := "replace"
			if m, ok := args["edit_mode"].(string); ok && m != "" {
				mode = m
			}
			cellType, _ := args["cell_type"].(string)

			nb, err := LoadNotebook(path)
			if err != nil {
				if os.IsNotExist(err) {
					return types.ToolResult{
						Content: fmt.Sprintf("Notebook not found: %s", path),
						IsError: true,
					}, nil
				}
				return types.ToolResult{
					Content: fmt.Sprintf("Error loading notebook: %v", err),
					IsError: true,
				}, nil
			}

			switch mode {
			case "delete":
				if cellNum >= len(nb.Cells) {
					return types.ToolResult{
						Content: fmt.Sprintf("cell_number %d out of range (notebook has %d cells)", cellNum, len(nb.Cells)),
						IsError: true,
					}, nil
				}
				nb.Cells = append(nb.Cells[:cellNum], nb.Cells[cellNum+1:]...)

			case "insert":
				if cellNum > len(nb.Cells) {
					return types.ToolResult{
						Content: fmt.Sprintf("cell_number %d out of range for insert (notebook has %d cells)", cellNum, len(nb.Cells)),
						IsError: true,
					}, nil
				}
				srcBytes, _ := json.Marshal(newSource)
				newCell := NotebookCell{
					CellType: cellType,
					Source:   srcBytes,
					Metadata: map[string]json.RawMessage{},
					Extra:    map[string]json.RawMessage{},
				}
				if cellType == "code" {
					newCell.ExecutionCount = json.RawMessage(`null`)
					newCell.Outputs = []json.RawMessage{}
				}
				nb.Cells = append(nb.Cells, NotebookCell{})
				copy(nb.Cells[cellNum+1:], nb.Cells[cellNum:])
				nb.Cells[cellNum] = newCell

			case "replace":
				if cellNum >= len(nb.Cells) {
					return types.ToolResult{
						Content: fmt.Sprintf("cell_number %d out of range (notebook has %d cells)", cellNum, len(nb.Cells)),
						IsError: true,
					}, nil
				}
				target := &nb.Cells[cellNum]
				srcBytes, _ := json.Marshal(newSource)
				target.Source = srcBytes
				if cellType != "" && cellType != target.CellType {
					target.CellType = cellType
				}
				if target.CellType == "code" {
					target.ExecutionCount = json.RawMessage(`null`)
					target.Outputs = []json.RawMessage{}
				}
			}

			if err := SaveNotebook(path, nb); err != nil {
				return types.ToolResult{
					Content: fmt.Sprintf("Error saving notebook: %v", err),
					IsError: true,
				}, nil
			}

			var msg string
			switch mode {
			case "delete":
				msg = fmt.Sprintf("Deleted cell %d from %s", cellNum, path)
			case "insert":
				msg = fmt.Sprintf("Inserted %s cell at %d in %s", cellType, cellNum, path)
			default:
				msg = fmt.Sprintf("Replaced cell %d in %s", cellNum, path)
			}
			return types.ToolResult{Content: msg}, nil
		},
	})
}

// RegisterNotebookEditTool registers the notebook_edit tool on reg, rooted at
// the given workspace directory. Use the empty string to allow any absolute
// path (not recommended outside tests).
func RegisterNotebookEditTool(reg *Registry, workingDir string) {
	reg.Register(NewNotebookEditTool(workingDir))
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// coerceInt accepts int, int64, float64 (JSON number) and returns the value.
func coerceInt(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int32:
		return int(n), true
	case int64:
		return int(n), true
	case float64:
		return int(n), true
	case float32:
		return int(n), true
	case json.Number:
		i, err := n.Int64()
		if err != nil {
			return 0, false
		}
		return int(i), true
	}
	return 0, false
}

// isWithinDir reports whether path is contained in base after cleaning.
func isWithinDir(base, path string) bool {
	absBase, err := filepath.Abs(base)
	if err != nil {
		return false
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(absBase, absPath)
	if err != nil {
		return false
	}
	return rel == "." || (!strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel))
}
