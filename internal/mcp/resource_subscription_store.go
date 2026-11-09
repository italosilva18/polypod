package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type subscriptionFile struct {
	Version       int                 `json:"version"`
	Subscriptions map[string][]string `json:"subscriptions"`
}

// SetSubscriptionStore loads desired resource subscriptions from a private
// file. Call it before connecting servers so Connect can restore them. The
// file contains server names and URIs, which may themselves be sensitive.
func (m *Manager) SetSubscriptionStore(path string) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("mcp: caminho do arquivo de assinaturas obrigatorio")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("mcp: resolvendo arquivo de assinaturas: %w", err)
	}
	m.subscriptionSaveMu.Lock()
	defer m.subscriptionSaveMu.Unlock()
	m.mu.RLock()
	closed, connected, configured := m.closed, len(m.sessions) > 0, m.subscriptionStore != ""
	m.mu.RUnlock()
	if closed || connected || configured {
		return fmt.Errorf("mcp: configure o arquivo de assinaturas uma vez, antes de conectar servidores")
	}
	var data subscriptionFile
	unreadable := false
	if err := withSubscriptionFileLock(absolute, func() error {
		loaded, exists, readErr := readSubscriptionFile(absolute)
		if errors.Is(readErr, os.ErrPermission) {
			m.logger.Warn("mcp subscriptions unreadable; continuing without persistence", "path", absolute, "error", readErr)
			unreadable = true
			return nil
		}
		if readErr != nil {
			return readErr
		}
		data = loaded
		if !exists {
			return nil
		}
		if err := inheritSubscriptionOwner(absolute, absolute); err != nil {
			return fmt.Errorf("mcp: corrigindo dono do arquivo de assinaturas: %w", err)
		}
		if err := os.Chmod(absolute, 0o600); err != nil {
			if !errors.Is(err, os.ErrPermission) {
				return fmt.Errorf("mcp: protegendo arquivo de assinaturas: %w", err)
			}
			m.logger.Warn("mcp subscriptions permissions could not be tightened; a future save will replace the file", "path", absolute, "error", err)
		}
		return nil
	}); err != nil {
		if errors.Is(err, os.ErrPermission) {
			m.logger.Warn("mcp subscription store inaccessible; continuing without persistence", "path", absolute, "error", err)
			return nil
		}
		return err
	}
	if unreadable {
		return nil
	}
	m.mu.Lock()
	for server, uris := range data.Subscriptions {
		if strings.TrimSpace(server) == "" {
			continue
		}
		if m.subscriptions[server] == nil {
			m.subscriptions[server] = make(map[string]struct{})
		}
		for _, uri := range uris {
			if strings.TrimSpace(uri) != "" {
				m.subscriptions[server][uri] = struct{}{}
			}
		}
	}
	m.subscriptionStore = absolute
	m.mu.Unlock()
	return nil
}

// persistSubscriptionChange applies one operation to the latest on-disk
// state while holding the interprocess lock. A second Polypod process can
// change other subscriptions without having its work overwritten here.
func (m *Manager) persistSubscriptionChange(server, uri string) error {
	m.subscriptionSaveMu.Lock()
	defer m.subscriptionSaveMu.Unlock()
	m.mu.RLock()
	path := m.subscriptionStore
	_, subscribe := m.subscriptions[server][uri]
	m.mu.RUnlock()
	if path == "" {
		return nil
	}
	return withSubscriptionFileLock(path, func() error {
		data, _, err := readSubscriptionFile(path)
		if err != nil {
			return err
		}
		set := make(map[string]struct{}, len(data.Subscriptions[server])+1)
		for _, current := range data.Subscriptions[server] {
			set[current] = struct{}{}
		}
		if subscribe {
			set[uri] = struct{}{}
		} else {
			delete(set, uri)
		}
		if len(set) == 0 {
			delete(data.Subscriptions, server)
		} else {
			uris := make([]string, 0, len(set))
			for current := range set {
				uris = append(uris, current)
			}
			sort.Strings(uris)
			data.Subscriptions[server] = uris
		}
		return writeSubscriptionFile(path, data)
	})
}

func readSubscriptionFile(path string) (subscriptionFile, bool, error) {
	data := subscriptionFile{Version: 1, Subscriptions: make(map[string][]string)}
	contents, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return data, false, nil
	}
	if err != nil {
		return subscriptionFile{}, false, fmt.Errorf("mcp: lendo assinaturas: %w", err)
	}
	if err := json.Unmarshal(contents, &data); err != nil {
		return subscriptionFile{}, false, fmt.Errorf("mcp: arquivo de assinaturas invalido: %w", err)
	}
	if data.Version != 1 {
		return subscriptionFile{}, false, fmt.Errorf("mcp: versao de arquivo de assinaturas desconhecida: %d", data.Version)
	}
	if data.Subscriptions == nil {
		data.Subscriptions = make(map[string][]string)
	}
	return data, true, nil
}

func writeSubscriptionFile(path string, data subscriptionFile) (resultErr error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".mcp-subscriptions-*")
	if err != nil {
		return err
	}
	defer func() {
		if resultErr != nil {
			_ = os.Remove(f.Name())
		}
	}()
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		return err
	}
	if err := inheritSubscriptionOwner(f.Name(), path); err != nil {
		_ = f.Close()
		return err
	}
	if err := json.NewEncoder(f).Encode(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(f.Name(), path); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
