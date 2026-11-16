// Package editor — Editor Utilities (quickopen + bookmarks + snippets)
package editor

type QuickOpen struct{}
type Bookmarks struct{ bookmarks []string }
type Snippets struct{ snippets map[string]string }

func NewQuickOpen() *QuickOpen { return &QuickOpen{} }
func NewBookmarks() *Bookmarks { return &Bookmarks{bookmarks: []string{}} }
func NewSnippets() *Snippets { return &Snippets{snippets: make(map[string]string)} }

func (q *QuickOpen) Open(path string) {}
func (b *Bookmarks) Add(path string) { b.bookmarks = append(b.bookmarks, path) }
func (s *Snippets) Add(name, code string) { s.snippets[name] = code }
