// Package indexer — Codebase Indexing
package codebase

import (
	"os"
	"path/filepath"
	"strings"
)

type CodebaseIndex struct {
	files    map[string]FileInfo
	language map[string]int
}

type FileInfo struct {
	Path     string
	Language string
	Size     int64
	Lines    int
}

func NewCodebaseIndex() *CodebaseIndex {
	return &CodebaseIndex{
		files:    make(map[string]FileInfo),
		language: make(map[string]int),
	}
}

func (i *CodebaseIndex) IndexDirectory(dir string) error {
	return filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			lang := detectLanguage(path)
			i.files[path] = FileInfo{
				Path:     path,
				Language: lang,
				Size:     info.Size(),
				Lines:    countLines(path),
			}
			i.language[lang]++
		}
		return nil
	})
}

func (i *CodebaseIndex) GetFilesByLanguage(lang string) []FileInfo {
	var result []FileInfo
	for _, f := range i.files {
		if f.Language == lang {
			result = append(result, f)
		}
	}
	return result
}

func (i *CodebaseIndex) GetStats() map[string]int {
	return i.language
}

func detectLanguage(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".go":
		return "Go"
	case ".py":
		return "Python"
	case ".ts", ".tsx":
		return "TypeScript"
	case ".js", ".jsx":
		return "JavaScript"
	default:
		return "Unknown"
	}
}

func countLines(path string) int {
	// Simple line count
	data, _ := os.ReadFile(path)
	return strings.Count(string(data), "\n")
}
