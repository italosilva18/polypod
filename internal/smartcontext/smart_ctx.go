// Package smartcontext — Smart Context
package smartcontext

type SmartContext struct {
	files        []string
	autoIncluded map[string]bool
	relevance    map[string]float64
}

func NewSmartContext() *SmartContext {
	return &SmartContext{
		files:        make([]string, 0),
		autoIncluded: make(map[string]bool),
		relevance:    make(map[string]float64),
	}
}

func (c *SmartContext) AddFile(path string, relevance float64) {
	c.files = append(c.files, path)
	c.relevance[path] = relevance
}

func (c *SmartContext) AutoInclude(path string) {
	c.autoIncluded[path] = true
}

func (c *SmartContext) GetContext() []string {
	return c.files
}

func (c *SmartContext) GetRelevance(path string) float64 {
	return c.relevance[path]
}

func (c *SmartContext) FormatContext() string {
	result := "📎 **Smart Context**:\n\n"
	for _, f := range c.files {
		relevance := c.relevance[f]
		result += "- " + f + " (relevance: " + string(rune(relevance)) + ")\n"
	}
	return result
}
