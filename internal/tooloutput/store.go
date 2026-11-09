// Package tooloutput retains large tool responses outside model context.
package tooloutput

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	PreviewLimit = 50 << 10
	ReadLimit    = 32 << 10
	FileLimit    = 32 << 20
	TotalLimit   = 256 << 20
	Retention    = 24 * time.Hour
)

var storeMu sync.Mutex

// Preview keeps a short head and tail in the model context. The complete
// output is retained in a private cache and can be read with tool_output.
func Preview(content string) string {
	if len(content) <= PreviewLimit {
		return content
	}
	const headBytes = 32 << 10
	const tailBytes = 8 << 10
	head := strings.ToValidUTF8(content[:headBytes], "�")
	tail := strings.ToValidUTF8(content[len(content)-tailBytes:], "�")
	if len(content) > FileLimit {
		return fmt.Sprintf("%s\n... [%d bytes de saida; limite de armazenamento por resultado: %d bytes]\n%s",
			head, len(content), FileLimit, tail)
	}
	id, err := Store(content)
	if err != nil {
		return fmt.Sprintf("%s\n... [%d bytes de saida; falha ao guardar resultado: %v]\n%s",
			head, len(content), err, tail)
	}
	return fmt.Sprintf("%s\n... [%d bytes no total; trecho omitido. Use tool_output com id=%s e offset em bytes para ler por paginas.]\n%s",
		head, len(content), id, tail)
}

// Store writes one complete result with a random opaque identifier. It does
// not accept an oversized result, so callers never mistake partial storage
// for a complete archived response.
func Store(content string) (string, error) {
	if len(content) > FileLimit {
		return "", fmt.Errorf("resultado excede %d bytes", FileLimit)
	}
	storeMu.Lock()
	defer storeMu.Unlock()
	dir, err := storeDir()
	if err != nil {
		return "", err
	}
	if err := pruneLocked(dir, int64(len(content))); err != nil {
		return "", err
	}
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", fmt.Errorf("gerando ID de resultado: %w", err)
	}
	id := hex.EncodeToString(random[:])
	path := filepath.Join(dir, id+".txt")
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return "", fmt.Errorf("criando resultado: %w", err)
	}
	if _, err := io.WriteString(file, content); err != nil {
		file.Close()
		os.Remove(path)
		return "", fmt.Errorf("gravando resultado: %w", err)
	}
	if err := file.Sync(); err != nil {
		file.Close()
		os.Remove(path)
		return "", fmt.Errorf("sincronizando resultado: %w", err)
	}
	if err := file.Close(); err != nil {
		os.Remove(path)
		return "", fmt.Errorf("fechando resultado: %w", err)
	}
	return id, nil
}

// Read returns a bounded byte page, its actual starting offset and the full
// file size. Page boundaries follow UTF-8 runes, so a returned next offset
// never drops a character between adjacent pages.
func Read(id string, offset, limit int64) ([]byte, int64, int64, error) {
	if !validID(id) {
		return nil, 0, 0, errors.New("ID de resultado invalido")
	}
	if offset < 0 {
		return nil, 0, 0, errors.New("offset negativo")
	}
	if limit <= 0 {
		limit = 8192
	}
	if limit < utf8.UTFMax {
		limit = utf8.UTFMax
	}
	if limit > ReadLimit {
		limit = ReadLimit
	}
	dir, err := storeDir()
	if err != nil {
		return nil, 0, 0, err
	}
	path := filepath.Join(dir, id+".txt")
	before, err := os.Lstat(path)
	if err != nil {
		return nil, 0, 0, err
	}
	if !before.Mode().IsRegular() || before.Mode().Perm()&0077 != 0 {
		return nil, 0, 0, errors.New("arquivo de resultado invalido ou nao privado")
	}
	if time.Since(before.ModTime()) > Retention {
		return nil, 0, 0, errors.New("resultado expirado")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, 0, 0, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, 0, 0, err
	}
	if !info.Mode().IsRegular() || !os.SameFile(before, info) {
		return nil, 0, 0, errors.New("arquivo de resultado trocado durante leitura")
	}
	if offset >= info.Size() {
		return []byte{}, info.Size(), info.Size(), nil
	}
	start := offset
	for steps := 0; start > 0 && steps < utf8.UTFMax-1; steps++ {
		var one [1]byte
		if _, err := file.ReadAt(one[:], start); err != nil {
			return nil, 0, 0, fmt.Errorf("alinhando pagina: %w", err)
		}
		if !utf8.RuneStart(one[0]) {
			start--
			continue
		}
		break
	}
	page := make([]byte, min(limit, info.Size()-start))
	n, err := file.ReadAt(page, start)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, 0, 0, fmt.Errorf("lendo pagina: %w", err)
	}
	page = page[:n]
	if start+int64(len(page)) < info.Size() {
		last := len(page) - 1
		for last > 0 && !utf8.RuneStart(page[last]) {
			last--
		}
		if !utf8.FullRune(page[last:]) {
			page = page[:last]
		}
	}
	return page, start, info.Size(), nil
}

func validID(id string) bool {
	if len(id) != 32 || strings.ToLower(id) != id {
		return false
	}
	_, err := hex.DecodeString(id)
	return err == nil
}

func storeDir() (string, error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(cache, "polypod", "tool-output")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", fmt.Errorf("criando cache de resultados: %w", err)
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return "", err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("cache de resultados invalido")
	}
	if err := os.Chmod(dir, 0700); err != nil {
		return "", err
	}
	return dir, nil
}

type storedFile struct {
	path string
	size int64
	when time.Time
}

// pruneLocked removes expired files and then the oldest pages until the next
// result fits. Only opaque IDs from this store are considered for deletion.
func pruneLocked(dir string, nextSize int64) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	files := make([]storedFile, 0, len(entries))
	var total int64
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".txt") || !validID(strings.TrimSuffix(name, ".txt")) {
			continue
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		path := filepath.Join(dir, name)
		if time.Since(info.ModTime()) > Retention {
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("removendo resultado expirado: %w", err)
			}
			continue
		}
		files = append(files, storedFile{path: path, size: info.Size(), when: info.ModTime()})
		total += info.Size()
	}
	sort.Slice(files, func(i, j int) bool { return files[i].when.Before(files[j].when) })
	for _, file := range files {
		if total+nextSize <= TotalLimit {
			break
		}
		if err := os.Remove(file.path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("liberando cache de resultados: %w", err)
		}
		total -= file.size
	}
	if total+nextSize > TotalLimit {
		return errors.New("cache de resultados sem espaco")
	}
	return nil
}
