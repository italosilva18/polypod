// Package context_mgr — Context Management
package context_mgr

import "strings"

type ContextManager struct {
	files []string
	tags  []string
}

func NewContextManager() *ContextManager {
	return &ContextManager{
		files: make([]string, 0),
		tags:  make([]string, 0),
	}
}

func (c *ContextManager) AddFile(path string) {
	c.files = append(c.files, path)
}

func (c *ContextManager) RemoveFile(path string) {
	for i, f := range c.files {
		if f == path {
			c.files = append(c.files[:i], c.files[i+1:]...)
			break
		}
	}
}

func (c *ContextManager) AddTag(tag string) {
	c.tags = append(c.tags, tag)
}

func (c *ContextManager) GetFiles() []string {
	return c.files
}

func (c *ContextManager) GetTags() []string {
	return c.tags
}

func (c *ContextManager) Clear() {
	c.files = make([]string, 0)
	c.tags = make([]string, 0)
}

func (c *ContextManager) FormatContext() string {
	var sb strings.Builder
	sb.WriteString("📎 **Context**:\n\n")
	if len(c.files) > 0 {
		sb.WriteString("**Files**:\n")
		for _, f := range c.files {
			sb.WriteString(fmt.Sprintf("- %s\n", f))
		}
	}
	if len(c.tags) > 0 {
		sb.WriteString("\n**Tags**:\n")
		for _, t := range c.tags {
			sb.WriteString(fmt.Sprintf("- %s\n", t))
		}
	}
	return sb.String()
}
