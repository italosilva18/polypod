// Package code_map — Code Map Visualization
package codemap

type CodeMap struct {
	structures []CodeStructure
}

type CodeStructure struct {
	Name     string
	Type     string
	Location string
	Children []CodeStructure
}

func NewCodeMap() *CodeMap {
	return &CodeMap{}
}

func (m *CodeMap) AddStructure(structure CodeStructure) {
	m.structures = append(m.structures, structure)
}

func (m *CodeMap) GetStructures() []CodeStructure {
	return m.structures
}

func (m *CodeMap) FormatMap() string {
	result := "🗺️  **Code Map**:\n\n"
	for _, s := range m.structures {
		result += "- " + s.Type + ": " + s.Name + "\n"
	}
	return result
}
