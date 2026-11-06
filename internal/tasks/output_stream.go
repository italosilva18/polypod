package tasks

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
)

// output_stream.go provides a lightweight append-only output stream for
// background tasks. Each task writes stdout/stderr into its own log file
// and callers poll with an offset so they can stream deltas without
// re-reading the whole file.
//
// Migrated from the TypeScript tasks output path / offset tracking logic
// in src_extracted/src/tasks/.

// OutputStream is an append-only writer paired with a seekable reader.
// Write appends to the underlying file; ReadFrom(offset) returns the delta
// between the given offset and the current size of the file.
//
// OutputStream is safe for concurrent Write and ReadFrom calls.
type OutputStream struct {
	mu     sync.Mutex
	path   string
	writer *os.File
	reader *os.File
	offset int64 // current end-of-file offset (writer cursor)
	closed bool
}

// NewOutputStream opens (or creates) the file at path in append-write mode
// with permissions 0600, and opens a separate read handle on the same
// file. The initial offset is the current file size so callers can treat
// pre-existing content as "already consumed".
func NewOutputStream(path string) (*OutputStream, error) {
	if path == "" {
		return nil, fmt.Errorf("output stream: empty path")
	}
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, fmt.Errorf("output stream: mkdir %s: %w", dir, err)
		}
	}

	w, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("output stream: open write %s: %w", path, err)
	}

	r, err := os.OpenFile(path, os.O_RDONLY, 0o600)
	if err != nil {
		_ = w.Close()
		return nil, fmt.Errorf("output stream: open read %s: %w", path, err)
	}

	// The initial offset is the current size of the file. Anything that
	// existed before this stream was opened is treated as already-seen.
	info, err := w.Stat()
	if err != nil {
		_ = w.Close()
		_ = r.Close()
		return nil, fmt.Errorf("output stream: stat %s: %w", path, err)
	}

	return &OutputStream{
		path:   path,
		writer: w,
		reader: r,
		offset: info.Size(),
	}, nil
}

// Path returns the absolute file path backing this stream.
func (s *OutputStream) Path() string {
	return s.path
}

// Offset returns the current end-of-file offset, which is also the next
// position that a Write will append to.
func (s *OutputStream) Offset() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.offset
}

// Write appends p to the underlying log file. It satisfies io.Writer.
func (s *OutputStream) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return 0, fmt.Errorf("output stream: write on closed stream")
	}
	if len(p) == 0 {
		return 0, nil
	}
	n, err := s.writer.Write(p)
	s.offset += int64(n)
	return n, err
}

// ReadFrom returns all bytes written after the given offset, along with
// the new offset (typically the current end of the file). If offset is
// greater than the current size, an empty slice and the current offset
// are returned with a nil error.
//
// A negative offset is treated as 0.
func (s *OutputStream) ReadFrom(offset int64) ([]byte, int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, offset, fmt.Errorf("output stream: read on closed stream")
	}
	if offset < 0 {
		offset = 0
	}

	info, err := s.reader.Stat()
	if err != nil {
		return nil, offset, fmt.Errorf("output stream: stat: %w", err)
	}
	size := info.Size()
	if offset >= size {
		return nil, size, nil
	}

	if _, err := s.reader.Seek(offset, io.SeekStart); err != nil {
		return nil, offset, fmt.Errorf("output stream: seek: %w", err)
	}

	remaining := size - offset
	buf := make([]byte, remaining)
	n, err := io.ReadFull(s.reader, buf)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return nil, offset, fmt.Errorf("output stream: read: %w", err)
	}
	return buf[:n], offset + int64(n), nil
}

// Close closes both underlying handles. It is safe to call multiple times;
// subsequent calls return nil.
func (s *OutputStream) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true

	var firstErr error
	if s.writer != nil {
		if err := s.writer.Close(); err != nil {
			firstErr = err
		}
		s.writer = nil
	}
	if s.reader != nil {
		if err := s.reader.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
		s.reader = nil
	}
	return firstErr
}

// TaskOutputPath returns the canonical log path for a task, replacing the
// historical hard-coded `/tmp/polypod_task_<id>.log` path from types.go.
//
// The returned path is `<baseDir>/polypod_task_<taskID>.log`. The base
// directory is not created by this function — callers that need the
// directory should use DefaultTaskOutputBaseDir (which creates it) or
// call os.MkdirAll themselves.
func TaskOutputPath(baseDir, taskID string) string {
	if baseDir == "" {
		baseDir = os.TempDir()
	}
	return filepath.Join(baseDir, fmt.Sprintf("polypod_task_%s.log", taskID))
}

// DefaultTaskOutputBaseDir returns the canonical directory under which all
// task log files live. Preference order:
//
//  1. $XDG_STATE_HOME/polypod/tasks
//  2. $HOME/.polypod/tasks
//  3. $TMPDIR/polypod-tasks (ultimate fallback when HOME is unset)
//
// The returned directory is created with 0700 permissions if it does not
// already exist. If creation fails, the intended path is still returned
// so callers can report a useful error.
func DefaultTaskOutputBaseDir() string {
	var base string
	if xdg := os.Getenv("XDG_STATE_HOME"); xdg != "" {
		base = filepath.Join(xdg, "polypod", "tasks")
	} else if home, err := os.UserHomeDir(); err == nil && home != "" {
		base = filepath.Join(home, ".polypod", "tasks")
	} else {
		base = filepath.Join(os.TempDir(), "polypod-tasks")
	}
	_ = os.MkdirAll(base, 0o700)
	return base
}
