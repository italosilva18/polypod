package mcp

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const (
	resourceWakeStoreVersion = 1
	maxResourceWakeFile      = 16 << 20
	maxResourceWakePending   = 4096
)

type resourceWakeFile struct {
	Version int                             `json:"version"`
	Pending map[string][]resourceWakeRecord `json:"pending"`
}

// A resource has at most one wake being handed to the task queue and one
// coalesced successor. The handoff marker is persisted before invoking the
// queue, so a notification during that call always gets a different WakeID.
type resourceWakeRecord struct {
	ResourceUpdate
	HandoffStarted bool `json:"handoff_started,omitempty"`
}

func newResourceWakeFile() resourceWakeFile {
	return resourceWakeFile{Version: resourceWakeStoreVersion, Pending: make(map[string][]resourceWakeRecord)}
}

// SetResourceWakeStore restores accepted resource notifications before the
// handler starts. Call it once, before SetResourceUpdateHandler. Notifications
// are kept in a private atomic snapshot until the durable task queue accepts
// them; a crash can therefore retry the same WakeID without creating a second
// task. The MCP protocol cannot acknowledge notifications at the server, so a
// notification that was never delivered to this callback cannot be recovered.
func (m *Manager) SetResourceWakeStore(path string) error {
	if m == nil || m.resourceWake == nil {
		return errors.New("mcp: gerenciador de recursos indisponivel")
	}
	return m.resourceWake.setStore(path)
}

func (d *resourceWakeDispatcher) setStore(path string) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("mcp: caminho do diario de recursos obrigatorio")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("mcp: resolvendo diario de recursos: %w", err)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed || d.handler != nil || d.storePath != "" {
		return errors.New("mcp: configure o diario de recursos uma vez antes do handler")
	}
	var state resourceWakeFile
	err = withResourceWakeFileLock(abs, func() error {
		loaded, exists, readErr := readResourceWakeFile(abs)
		if readErr != nil {
			return readErr
		}
		state = loaded
		if exists {
			return nil
		}
		return writeResourceWakeFile(abs, state)
	})
	if err != nil {
		return fmt.Errorf("mcp: abrindo diario de recursos: %w", err)
	}
	d.storePath = abs
	for key, queue := range state.Pending {
		if len(queue) == 0 {
			continue
		}
		d.nextGen++
		d.generation[key] = d.nextGen
		d.pending[key] = queue[0].ResourceUpdate
	}
	return nil
}

// updateResourceWakeFile always loads the latest snapshot while holding the
// directory lock. Several Polypod processes can therefore add or complete
// different notifications without overwriting one another's pending work.
func updateResourceWakeFile(path string, change func(*resourceWakeFile) (bool, error)) error {
	return withResourceWakeFileLock(path, func() error {
		state, _, err := readResourceWakeFile(path)
		if err != nil {
			return err
		}
		changed, err := change(&state)
		if err != nil || !changed {
			return err
		}
		return writeResourceWakeFile(path, state)
	})
}

func readResourceWakeFile(path string) (resourceWakeFile, bool, error) {
	state := newResourceWakeFile()
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return state, false, nil
	}
	if err != nil {
		return resourceWakeFile{}, false, fmt.Errorf("mcp: lendo diario de recursos: %w", err)
	}
	data, readErr := io.ReadAll(io.LimitReader(file, maxResourceWakeFile+1))
	closeErr := file.Close()
	if readErr != nil {
		return resourceWakeFile{}, false, fmt.Errorf("mcp: lendo diario de recursos: %w", readErr)
	}
	if closeErr != nil {
		return resourceWakeFile{}, false, fmt.Errorf("mcp: fechando diario de recursos: %w", closeErr)
	}
	if len(data) > maxResourceWakeFile {
		return resourceWakeFile{}, false, errors.New("mcp: diario de recursos excede 16 MiB")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&state); err != nil {
		return resourceWakeFile{}, false, fmt.Errorf("mcp: diario de recursos invalido: %w", err)
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return resourceWakeFile{}, false, errors.New("mcp: diario de recursos contem dados extras")
	}
	if state.Version != resourceWakeStoreVersion {
		return resourceWakeFile{}, false, fmt.Errorf("mcp: versao do diario de recursos desconhecida: %d", state.Version)
	}
	if resourceWakeRecordCount(state) > maxResourceWakePending {
		return resourceWakeFile{}, false, errors.New("mcp: diario de recursos excede o limite de notificacoes")
	}
	if state.Pending == nil {
		state.Pending = make(map[string][]resourceWakeRecord)
	}
	for key, queue := range state.Pending {
		if len(queue) < 1 || len(queue) > 2 || (len(queue) == 2 && (!queue[0].HandoffStarted || queue[1].HandoffStarted)) {
			return resourceWakeFile{}, false, errors.New("mcp: diario de recursos contem fila invalida")
		}
		for i, record := range queue {
			update := record.ResourceUpdate
			if update.Server == "" || update.URI == "" || update.WakeID == "" || update.WakeRevision == 0 || update.At.IsZero() ||
				key != resourceWakeKey(update.Server, update.URI) || (i > 0 && update.WakeID == queue[0].WakeID) {
				return resourceWakeFile{}, false, errors.New("mcp: diario de recursos contem notificacao invalida")
			}
		}
	}
	return state, true, nil
}

func writeResourceWakeFile(path string, state resourceWakeFile) (resultErr error) {
	if resourceWakeRecordCount(state) > maxResourceWakePending {
		return errors.New("mcp: limite de notificacoes MCP pendentes excedido")
	}
	data, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("mcp: codificando diario de recursos: %w", err)
	}
	if len(data) > maxResourceWakeFile {
		return errors.New("mcp: diario de recursos excede 16 MiB")
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("mcp: criando diretorio de recursos: %w", err)
	}
	file, err := os.CreateTemp(dir, ".mcp-resource-wakes-*")
	if err != nil {
		return fmt.Errorf("mcp: criando diario temporario: %w", err)
	}
	tmp := file.Name()
	defer func() {
		if resultErr == nil {
			return
		}
		if removeErr := os.Remove(tmp); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			resultErr = errors.Join(resultErr, removeErr)
		}
	}()
	if err := file.Chmod(0o600); err != nil {
		return errors.Join(err, file.Close())
	}
	if err := inheritSubscriptionOwner(tmp, path); err != nil {
		return errors.Join(err, file.Close())
	}
	if _, err := file.Write(data); err != nil {
		return errors.Join(err, file.Close())
	}
	if err := file.Sync(); err != nil {
		return errors.Join(err, file.Close())
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("mcp: publicando diario de recursos: %w", err)
	}
	parent, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("mcp: abrindo diretorio do diario: %w", err)
	}
	syncErr := parent.Sync()
	closeErr := parent.Close()
	if syncErr != nil || closeErr != nil {
		return errors.Join(syncErr, closeErr)
	}
	return nil
}

func resourceWakeRecordCount(state resourceWakeFile) int {
	count := 0
	for _, queue := range state.Pending {
		count += len(queue)
	}
	return count
}

func latestWake(a, b ResourceUpdate) ResourceUpdate {
	if a.At.After(b.At) {
		return a
	}
	return b
}
