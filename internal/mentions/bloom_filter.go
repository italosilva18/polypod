package mentions

import (
	"path/filepath"
	"strings"
	"sync"

	"github.com/costa/polypod/internal/bloom"
)

// FastPathChecker is a bloom-filter-backed probabilistic path existence checker.
// It's used as a first-pass filter before expensive filesystem checks.
// May have false positives but never false negatives.
type FastPathChecker struct {
	mu     sync.RWMutex
	filter *bloom.Filter
	count  int
}

// NewFastPathChecker creates a checker sized for the expected number of paths.
func NewFastPathChecker(expectedPaths int) *FastPathChecker {
	return &FastPathChecker{
		filter: bloom.New(expectedPaths, 0.01),
	}
}

// Add inserts a path into the filter.
// Also adds the basename and lowercased variants.
func (f *FastPathChecker) Add(path string) {
	f.mu.Lock()
	defer f.mu.Unlock()

	clean := filepath.Clean(path)
	f.filter.AddString(clean)
	f.filter.AddString(strings.ToLower(clean))

	base := filepath.Base(clean)
	f.filter.AddString(base)
	f.filter.AddString(strings.ToLower(base))

	// Also index without extension
	if ext := filepath.Ext(base); ext != "" {
		noExt := strings.TrimSuffix(base, ext)
		f.filter.AddString(noExt)
		f.filter.AddString(strings.ToLower(noExt))
	}

	f.count++
}

// AddMany adds multiple paths at once.
func (f *FastPathChecker) AddMany(paths []string) {
	for _, p := range paths {
		f.Add(p)
	}
}

// MaybeContains returns false if the query is DEFINITELY not in the index,
// and true if it MAY be (caller should verify with actual filesystem check).
func (f *FastPathChecker) MaybeContains(query string) bool {
	f.mu.RLock()
	defer f.mu.RUnlock()

	if f.filter == nil {
		return true
	}

	// Try exact match first
	if f.filter.Contains([]byte(query)) {
		return true
	}

	// Try cleaned form
	clean := filepath.Clean(query)
	if f.filter.Contains([]byte(clean)) {
		return true
	}

	// Try lowercase
	if f.filter.Contains([]byte(strings.ToLower(clean))) {
		return true
	}

	return false
}

// MaybeContainsBasename checks only the basename (last path component).
func (f *FastPathChecker) MaybeContainsBasename(query string) bool {
	f.mu.RLock()
	defer f.mu.RUnlock()

	if f.filter == nil {
		return true
	}

	base := filepath.Base(query)
	if f.filter.Contains([]byte(base)) {
		return true
	}
	return f.filter.Contains([]byte(strings.ToLower(base)))
}

// Count returns the number of paths added.
func (f *FastPathChecker) Count() int {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.count
}

// EstimatedFPRate returns the current estimated false-positive rate.
func (f *FastPathChecker) EstimatedFPRate() float64 {
	f.mu.RLock()
	defer f.mu.RUnlock()
	if f.filter == nil {
		return 0
	}
	return f.filter.EstimatedFPRate()
}

// Reset clears the filter.
func (f *FastPathChecker) Reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.filter.Reset()
	f.count = 0
}
