package bash

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	MaxOutputReadBytes = 64 << 10
	outputRetention    = 24 * time.Hour
)

type outputCapture struct {
	mu       sync.Mutex
	preview  limitedBuffer
	file     *os.File
	writeErr error
}

func newOutputCapture(file *os.File) *outputCapture {
	return &outputCapture{preview: limitedBuffer{maxSize: MaxOutputBytes}, file: file}
}

func (c *outputCapture) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, _ = c.preview.Write(p)
	if c.file == nil || c.writeErr != nil {
		return len(p), nil
	}
	n, err := c.file.Write(p)
	if err != nil {
		c.writeErr = err
	} else if n != len(p) {
		c.writeErr = io.ErrShortWrite
	}
	return len(p), nil
}

func (c *outputCapture) close() error {
	if c.file == nil {
		return nil
	}
	return c.file.Close()
}

func outputStoreDir() (string, error) {
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(cacheDir, "polypod", "bash-output")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return "", err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("invalid bash output directory %s", dir)
	}
	if err := os.Chmod(dir, 0700); err != nil {
		return "", err
	}
	return dir, nil
}

func newOutputID() (string, error) {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(id[:]), nil
}

func openOutputCaptures() (string, *outputCapture, *outputCapture, error) {
	dir, err := outputStoreDir()
	if err != nil {
		return "", nil, nil, err
	}
	pruneStoredOutputs(dir)
	id, err := newOutputID()
	if err != nil {
		return "", nil, nil, err
	}
	stdout, err := os.OpenFile(filepath.Join(dir, id+".stdout"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return "", nil, nil, err
	}
	stderr, err := os.OpenFile(filepath.Join(dir, id+".stderr"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		stdout.Close()
		os.Remove(stdout.Name())
		return "", nil, nil, err
	}
	return id, newOutputCapture(stdout), newOutputCapture(stderr), nil
}

func validOutputID(id string) bool {
	if len(id) != 32 {
		return false
	}
	_, err := hex.DecodeString(id)
	return err == nil && strings.ToLower(id) == id
}

// ReadStoredOutput reads one bounded byte range from a retained command stream.
func ReadStoredOutput(id, stream string, offset, limit int64) ([]byte, int64, error) {
	if !validOutputID(id) {
		return nil, 0, errors.New("ID de saida invalido")
	}
	if stream != "stdout" && stream != "stderr" {
		return nil, 0, errors.New("stream deve ser stdout ou stderr")
	}
	if offset < 0 {
		return nil, 0, errors.New("offset deve ser maior ou igual a zero")
	}
	if limit <= 0 {
		limit = 8192
	}
	if limit > MaxOutputReadBytes {
		limit = MaxOutputReadBytes
	}
	dir, err := outputStoreDir()
	if err != nil {
		return nil, 0, err
	}
	path := filepath.Join(dir, id+"."+stream)
	entry, err := os.Lstat(path)
	if err != nil {
		return nil, 0, err
	}
	if !entry.Mode().IsRegular() {
		return nil, 0, errors.New("saida armazenada invalida")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, 0, err
	}
	if time.Since(info.ModTime()) > outputRetention {
		return nil, 0, errors.New("saida expirada")
	}
	if offset >= info.Size() {
		return []byte{}, info.Size(), nil
	}
	buf := make([]byte, min(limit, info.Size()-offset))
	n, err := file.ReadAt(buf, offset)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, 0, err
	}
	return buf[:n], info.Size(), nil
}

func pruneStoredOutputs(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		if time.Since(info.ModTime()) > outputRetention {
			os.Remove(path)
		}
	}
}
