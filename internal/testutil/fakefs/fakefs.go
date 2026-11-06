// Package fakefs provides an in-memory filesystem for tests.
package fakefs

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"sort"
	"strings"
	"sync"
	"time"
)

// FS is an in-memory filesystem.
type FS struct {
	mu      sync.RWMutex
	files   map[string]*fakeFile
	errors  map[string]error // path -> error to inject
	latency map[string]time.Duration
}

type fakeFile struct {
	name    string
	data    []byte
	isDir   bool
	mode    os.FileMode
	modTime time.Time
}

// New creates a new fake filesystem.
func New() *FS {
	fs := &FS{
		files:   make(map[string]*fakeFile),
		errors:  make(map[string]error),
		latency: make(map[string]time.Duration),
	}
	// Create root
	fs.files["/"] = &fakeFile{
		name:    "/",
		isDir:   true,
		mode:    0755 | os.ModeDir,
		modTime: time.Now(),
	}
	return fs
}

// WriteFile writes data to a path.
func (f *FS) WriteFile(p string, data []byte) error {
	return f.WriteFileWithMode(p, data, 0644)
}

// WriteFileWithMode writes data with explicit mode.
func (f *FS) WriteFileWithMode(p string, data []byte, mode os.FileMode) error {
	p = clean(p)

	if err := f.checkInjections(p); err != nil {
		return err
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	// Ensure parent directory exists
	parent := path.Dir(p)
	if parent != "/" && parent != "." {
		if _, ok := f.files[parent]; !ok {
			return fmt.Errorf("parent directory does not exist: %s", parent)
		}
	}

	f.files[p] = &fakeFile{
		name:    path.Base(p),
		data:    append([]byte(nil), data...),
		mode:    mode,
		modTime: time.Now(),
	}
	return nil
}

// ReadFile reads data from a path.
func (f *FS) ReadFile(p string) ([]byte, error) {
	p = clean(p)

	if err := f.checkInjections(p); err != nil {
		return nil, err
	}

	f.mu.RLock()
	defer f.mu.RUnlock()

	file, ok := f.files[p]
	if !ok {
		return nil, &fs.PathError{Op: "open", Path: p, Err: fs.ErrNotExist}
	}
	if file.isDir {
		return nil, &fs.PathError{Op: "read", Path: p, Err: errors.New("is a directory")}
	}
	return append([]byte(nil), file.data...), nil
}

// Mkdir creates a single directory.
func (f *FS) Mkdir(p string) error {
	return f.MkdirMode(p, 0755)
}

// MkdirMode creates a directory with the given mode.
func (f *FS) MkdirMode(p string, mode os.FileMode) error {
	p = clean(p)

	if err := f.checkInjections(p); err != nil {
		return err
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	if _, ok := f.files[p]; ok {
		return &fs.PathError{Op: "mkdir", Path: p, Err: fs.ErrExist}
	}

	parent := path.Dir(p)
	if parent != "/" && parent != "." {
		parentFile, ok := f.files[parent]
		if !ok || !parentFile.isDir {
			return fmt.Errorf("parent directory does not exist: %s", parent)
		}
	}

	f.files[p] = &fakeFile{
		name:    path.Base(p),
		isDir:   true,
		mode:    mode | os.ModeDir,
		modTime: time.Now(),
	}
	return nil
}

// MkdirAll creates a directory and all parents.
func (f *FS) MkdirAll(p string) error {
	p = clean(p)

	f.mu.Lock()
	defer f.mu.Unlock()

	if existing, ok := f.files[p]; ok && existing.isDir {
		return nil
	}

	// Build all parent paths
	parts := strings.Split(strings.TrimPrefix(p, "/"), "/")
	current := ""
	for _, part := range parts {
		if part == "" {
			continue
		}
		current = current + "/" + part
		if _, ok := f.files[current]; !ok {
			f.files[current] = &fakeFile{
				name:    part,
				isDir:   true,
				mode:    0755 | os.ModeDir,
				modTime: time.Now(),
			}
		}
	}
	return nil
}

// Remove deletes a single file or empty directory.
func (f *FS) Remove(p string) error {
	p = clean(p)

	if err := f.checkInjections(p); err != nil {
		return err
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	file, ok := f.files[p]
	if !ok {
		return &fs.PathError{Op: "remove", Path: p, Err: fs.ErrNotExist}
	}

	if file.isDir {
		// Check empty
		prefix := p + "/"
		for k := range f.files {
			if strings.HasPrefix(k, prefix) {
				return &fs.PathError{Op: "remove", Path: p, Err: errors.New("directory not empty")}
			}
		}
	}

	delete(f.files, p)
	return nil
}

// RemoveAll recursively deletes.
func (f *FS) RemoveAll(p string) error {
	p = clean(p)

	f.mu.Lock()
	defer f.mu.Unlock()

	delete(f.files, p)
	prefix := p + "/"
	for k := range f.files {
		if strings.HasPrefix(k, prefix) {
			delete(f.files, k)
		}
	}
	return nil
}

// Stat returns file info.
func (f *FS) Stat(p string) (os.FileInfo, error) {
	p = clean(p)

	f.mu.RLock()
	defer f.mu.RUnlock()

	file, ok := f.files[p]
	if !ok {
		return nil, &fs.PathError{Op: "stat", Path: p, Err: fs.ErrNotExist}
	}
	return &fakeFileInfo{f: file}, nil
}

// Exists returns true if a path exists.
func (f *FS) Exists(p string) bool {
	p = clean(p)
	f.mu.RLock()
	defer f.mu.RUnlock()
	_, ok := f.files[p]
	return ok
}

// IsDir returns true if path is a directory.
func (f *FS) IsDir(p string) bool {
	p = clean(p)
	f.mu.RLock()
	defer f.mu.RUnlock()
	file, ok := f.files[p]
	return ok && file.isDir
}

// ReadDir lists entries in a directory.
func (f *FS) ReadDir(p string) ([]os.FileInfo, error) {
	p = clean(p)

	f.mu.RLock()
	defer f.mu.RUnlock()

	dir, ok := f.files[p]
	if !ok {
		return nil, &fs.PathError{Op: "readdir", Path: p, Err: fs.ErrNotExist}
	}
	if !dir.isDir {
		return nil, &fs.PathError{Op: "readdir", Path: p, Err: errors.New("not a directory")}
	}

	var result []os.FileInfo
	prefix := p
	if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	if p == "/" {
		prefix = "/"
	}

	for k, file := range f.files {
		if k == p {
			continue
		}
		if !strings.HasPrefix(k, prefix) {
			continue
		}
		rest := strings.TrimPrefix(k, prefix)
		if strings.Contains(rest, "/") {
			continue // not a direct child
		}
		result = append(result, &fakeFileInfo{f: file})
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].Name() < result[j].Name()
	})
	return result, nil
}

// InjectError sets an error to return on operations against a path.
func (f *FS) InjectError(p string, err error) {
	p = clean(p)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.errors[p] = err
}

// ClearError removes an injected error.
func (f *FS) ClearError(p string) {
	p = clean(p)
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.errors, p)
}

// InjectLatency sets a delay for operations on a path.
func (f *FS) InjectLatency(p string, d time.Duration) {
	p = clean(p)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.latency[p] = d
}

func (f *FS) checkInjections(p string) error {
	f.mu.RLock()
	err := f.errors[p]
	latency := f.latency[p]
	f.mu.RUnlock()

	if latency > 0 {
		time.Sleep(latency)
	}
	return err
}

// Snapshot captures the current state.
type Snapshot struct {
	files map[string]*fakeFile
}

// Snapshot returns a snapshot of the current filesystem.
func (f *FS) Snapshot() Snapshot {
	f.mu.RLock()
	defer f.mu.RUnlock()

	files := make(map[string]*fakeFile, len(f.files))
	for k, v := range f.files {
		copy := *v
		copy.data = append([]byte(nil), v.data...)
		files[k] = &copy
	}
	return Snapshot{files: files}
}

// Restore replaces state from a snapshot.
func (f *FS) Restore(s Snapshot) {
	f.mu.Lock()
	defer f.mu.Unlock()

	files := make(map[string]*fakeFile, len(s.files))
	for k, v := range s.files {
		copy := *v
		copy.data = append([]byte(nil), v.data...)
		files[k] = &copy
	}
	f.files = files
}

// AllPaths returns a sorted list of all paths in the filesystem.
func (f *FS) AllPaths() []string {
	f.mu.RLock()
	defer f.mu.RUnlock()

	result := make([]string, 0, len(f.files))
	for k := range f.files {
		result = append(result, k)
	}
	sort.Strings(result)
	return result
}

// Reset clears the entire filesystem (except root).
func (f *FS) Reset() {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.files = make(map[string]*fakeFile)
	f.files["/"] = &fakeFile{
		name:    "/",
		isDir:   true,
		mode:    0755 | os.ModeDir,
		modTime: time.Now(),
	}
	f.errors = make(map[string]error)
	f.latency = make(map[string]time.Duration)
}

// clean normalizes a path.
func clean(p string) string {
	if p == "" {
		return "/"
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return path.Clean(p)
}

// ===== fakeFileInfo implements os.FileInfo =====

type fakeFileInfo struct {
	f *fakeFile
}

func (fi *fakeFileInfo) Name() string       { return fi.f.name }
func (fi *fakeFileInfo) Size() int64        { return int64(len(fi.f.data)) }
func (fi *fakeFileInfo) Mode() os.FileMode  { return fi.f.mode }
func (fi *fakeFileInfo) ModTime() time.Time { return fi.f.modTime }
func (fi *fakeFileInfo) IsDir() bool        { return fi.f.isDir }
func (fi *fakeFileInfo) Sys() any           { return nil }
