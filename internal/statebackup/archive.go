// Package statebackup creates portable snapshots of the Polypod runtime state.
package statebackup

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
	_ "modernc.org/sqlite"
)

const (
	manifestName           = "manifest.json"
	archiveVersion         = 1
	maxEntries             = 100000
	maxFileBytes     int64 = 20 << 30
	maxManifestBytes int64 = 16 << 20
	redactedValue          = "__POLYPOD_SECRET_OMITTED__"
)

// Layout identifies the locations that can be included in a snapshot.
type Layout struct {
	ProjectDir         string
	ConfigPath         string
	DataDir            string
	DatabasePath       string
	AgentsDir          string
	SchedulerPath      string
	WhatsAppSessionDir string
}

// Entry is a file in the archive. Paths are relative to a named destination root.
type Entry struct {
	Root     string `json:"root"`
	Path     string `json:"path"`
	Size     int64  `json:"size"`
	SHA256   string `json:"sha256"`
	Mode     uint32 `json:"mode"`
	Redacted bool   `json:"redacted,omitempty"`
}

// Manifest lists every file and the archive's security scope.
type Manifest struct {
	Version         int       `json:"version"`
	CreatedAt       time.Time `json:"created_at"`
	SecretsIncluded bool      `json:"secrets_included"`
	Files           []Entry   `json:"files"`
}

type sourceFile struct {
	root string
	rel  string
	path string
}

// Create writes a complete archive to a temporary file and atomically renames it
// to output only when every source has been copied successfully.
func Create(ctx context.Context, layout Layout, output string, includeSecrets bool) (Manifest, error) {
	layout, err := normalizeLayout(layout)
	if err != nil {
		return Manifest{}, err
	}
	output, err = filepath.Abs(output)
	if err != nil {
		return Manifest{}, fmt.Errorf("resolving backup output: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(output), 0700); err != nil {
		return Manifest{}, fmt.Errorf("creating backup directory: %w", err)
	}
	if _, err := os.Lstat(output); err == nil {
		return Manifest{}, fmt.Errorf("backup already exists: %s", output)
	} else if !errors.Is(err, os.ErrNotExist) {
		return Manifest{}, fmt.Errorf("checking backup output: %w", err)
	}
	sources, err := collectSources(ctx, layout, output, includeSecrets)
	if err != nil {
		return Manifest{}, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(output), ".polypod-backup-*.zip")
	if err != nil {
		return Manifest{}, fmt.Errorf("creating temporary backup: %w", err)
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		return Manifest{}, err
	}
	zw := zip.NewWriter(tmp)
	manifest := Manifest{Version: archiveVersion, CreatedAt: time.Now().UTC(), SecretsIncluded: includeSecrets}
	for _, source := range sources {
		if err := ctx.Err(); err != nil {
			zw.Close()
			tmp.Close()
			return Manifest{}, err
		}
		entry, err := addSource(ctx, zw, source, includeSecrets)
		if err != nil {
			zw.Close()
			tmp.Close()
			return Manifest{}, err
		}
		manifest.Files = append(manifest.Files, entry)
	}
	manifestData, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		zw.Close()
		tmp.Close()
		return Manifest{}, fmt.Errorf("encoding backup manifest: %w", err)
	}
	if len(manifestData) > int(maxManifestBytes) {
		zw.Close()
		tmp.Close()
		return Manifest{}, fmt.Errorf("backup manifest exceeds size limit")
	}
	mw, err := zw.Create(manifestName)
	if err != nil {
		zw.Close()
		tmp.Close()
		return Manifest{}, fmt.Errorf("creating manifest entry: %w", err)
	}
	if _, err := mw.Write(manifestData); err != nil {
		zw.Close()
		tmp.Close()
		return Manifest{}, fmt.Errorf("writing manifest: %w", err)
	}
	if err := zw.Close(); err != nil {
		tmp.Close()
		return Manifest{}, fmt.Errorf("closing backup archive: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return Manifest{}, fmt.Errorf("syncing backup archive: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return Manifest{}, fmt.Errorf("closing backup file: %w", err)
	}
	if err := os.Rename(tmp.Name(), output); err != nil {
		return Manifest{}, fmt.Errorf("publishing backup archive: %w", err)
	}
	return manifest, nil
}

func normalizeLayout(l Layout) (Layout, error) {
	if l.ProjectDir == "" || l.ConfigPath == "" {
		return Layout{}, fmt.Errorf("project directory and config path are required")
	}
	var err error
	l.ProjectDir, err = filepath.Abs(l.ProjectDir)
	if err != nil {
		return Layout{}, err
	}
	if l.ConfigPath, err = absoluteFrom(l.ProjectDir, l.ConfigPath); err != nil {
		return Layout{}, err
	}
	if l.DataDir, err = absoluteFrom(l.ProjectDir, l.DataDir); err != nil {
		return Layout{}, err
	}
	if l.DatabasePath, err = absoluteFrom(l.ProjectDir, l.DatabasePath); err != nil {
		return Layout{}, err
	}
	if l.AgentsDir, err = absoluteFrom(l.ProjectDir, l.AgentsDir); err != nil {
		return Layout{}, err
	}
	if l.SchedulerPath, err = absoluteFrom(l.ProjectDir, l.SchedulerPath); err != nil {
		return Layout{}, err
	}
	if l.WhatsAppSessionDir, err = absoluteFrom(l.ProjectDir, l.WhatsAppSessionDir); err != nil {
		return Layout{}, err
	}
	return l, nil
}

func absoluteFrom(base, path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", nil
	}
	if filepath.IsAbs(path) {
		return filepath.Clean(path), nil
	}
	return filepath.Abs(filepath.Join(base, path))
}

func collectSources(ctx context.Context, l Layout, output string, secrets bool) ([]sourceFile, error) {
	seen := make(map[string]sourceFile)
	seenPaths := make(map[string]string)
	add := func(root, rel, path string) error {
		if path == output || path == "" {
			return nil
		}
		rel = filepath.ToSlash(filepath.Clean(rel))
		if err := validRelative(rel); err != nil {
			return err
		}
		key := root + "/" + rel
		if prev, ok := seen[key]; ok && prev.path != path {
			return fmt.Errorf("duplicate backup destination %s", key)
		}
		// The agents directory can sit inside the runtime data directory (or
		// the reverse) and be discovered under two archive roots. Keep the
		// first mapping so restore never targets one file twice.
		if previousKey, ok := seenPaths[path]; ok && previousKey != key {
			return nil
		}
		seen[key] = sourceFile{root: root, rel: rel, path: path}
		seenPaths[path] = key
		return nil
	}
	addFile := func(root, rel, path string) error {
		if path == "" {
			return nil
		}
		// A WhatsApp session can live below data/ or .polypod/. Keep it in
		// its own archive root so the broad project walks never export its
		// credentials without --include-secrets or restore it twice.
		if root != "whatsapp" && root != "config" && within(l.WhatsAppSessionDir, path) {
			return nil
		}
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("inspecting %s: %w", path, err)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("unsupported backup file %s: only regular files are allowed", path)
		}
		if !secrets && isSecretPath(rel) {
			return nil
		}
		if isEphemeral(rel) {
			return nil
		}
		if isPriorPolypodBackup(path) {
			return nil
		}
		return add(root, rel, path)
	}
	var walk func(string, string, string) error
	walk = func(root, relBase, source string) error {
		if source == "" {
			return nil
		}
		if root != "whatsapp" && within(l.WhatsAppSessionDir, source) {
			return nil
		}
		info, err := os.Lstat(source)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("inspecting %s: %w", source, err)
		}
		if !info.IsDir() {
			return addFile(root, relBase, source)
		}
		return filepath.WalkDir(source, func(path string, d os.DirEntry, walkErr error) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			if walkErr != nil {
				return walkErr
			}
			if root != "whatsapp" && within(l.WhatsAppSessionDir, path) {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			rel, err := filepath.Rel(source, path)
			if err != nil {
				return err
			}
			archiveRel := filepath.Join(relBase, rel)
			if path == source {
				return nil
			}
			if isEphemeral(archiveRel) || (!secrets && isSecretPath(archiveRel)) {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if d.IsDir() {
				return nil
			}
			return addFile(root, archiveRel, path)
		})
	}
	if err := addFile("config", "config.yaml", l.ConfigPath); err != nil {
		return nil, err
	}
	if secrets {
		for _, name := range []string{".env", ".env.local"} {
			if err := addFile("project", name, filepath.Join(l.ProjectDir, name)); err != nil {
				return nil, err
			}
		}
	}
	if err := walk("project", "config.d", filepath.Join(l.ProjectDir, "config.d")); err != nil {
		return nil, err
	}
	if err := walk("project", ".polypod", filepath.Join(l.ProjectDir, ".polypod")); err != nil {
		return nil, err
	}
	if l.AgentsDir != "" {
		root, rel := mappedRoot(l.ProjectDir, l.AgentsDir, "agents")
		if err := walk(root, rel, l.AgentsDir); err != nil {
			return nil, err
		}
	}
	if err := walk("project", "data", filepath.Join(l.ProjectDir, "data")); err != nil {
		return nil, err
	}
	if l.DataDir != "" && !within(filepath.Join(l.ProjectDir, "data"), l.DataDir) {
		root, rel := mappedRoot(l.ProjectDir, l.DataDir, "runtime_data")
		if err := walk(root, rel, l.DataDir); err != nil {
			return nil, err
		}
	}
	for _, extra := range []struct{ path, external string }{{l.DatabasePath, "database"}, {l.SchedulerPath, "scheduler"}} {
		if extra.path == "" || hasSourcePath(seen, extra.path) {
			continue
		}
		root, rel := mappedRoot(l.ProjectDir, extra.path, extra.external)
		if err := addFile(root, rel, extra.path); err != nil {
			return nil, err
		}
	}
	if secrets && l.WhatsAppSessionDir != "" {
		if err := walk("whatsapp", ".", l.WhatsAppSessionDir); err != nil {
			return nil, err
		}
	}
	if len(seen) > maxEntries {
		return nil, fmt.Errorf("backup contains too many files")
	}
	sources := make([]sourceFile, 0, len(seen))
	for _, s := range seen {
		sources = append(sources, s)
	}
	sort.Slice(sources, func(i, j int) bool {
		if sources[i].root != sources[j].root {
			return sources[i].root < sources[j].root
		}
		return sources[i].rel < sources[j].rel
	})
	return sources, nil
}

func mappedRoot(project, path, external string) (string, string) {
	if within(project, path) {
		rel, _ := filepath.Rel(project, path)
		return "project", rel
	}
	if external == "runtime_data" || external == "agents" {
		return external, "."
	}
	return external, filepath.Base(path)
}

func hasSourcePath(seen map[string]sourceFile, path string) bool {
	for _, s := range seen {
		if s.path == path {
			return true
		}
	}
	return false
}

func within(root, path string) bool {
	if root == "" || path == "" {
		return false
	}
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func validRelative(rel string) error {
	if rel == "" || rel == "." || filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, "../") || strings.Contains(rel, "\\") || filepath.ToSlash(filepath.Clean(rel)) != rel {
		return fmt.Errorf("unsafe archive path %q", rel)
	}
	return nil
}

func isEphemeral(rel string) bool {
	base := strings.ToLower(filepath.Base(rel))
	if base == ".git" || base == "node_modules" || base == "webcache" || base == "__pycache__" || base == ".cache" {
		return true
	}
	return strings.HasSuffix(base, ".log") || strings.HasSuffix(base, ".lock") || strings.HasSuffix(base, ".db-wal") || strings.HasSuffix(base, ".db-shm") || strings.HasSuffix(base, ".pending") || strings.HasSuffix(base, ".tmp") ||
		((strings.HasPrefix(base, "polypod-backup-") || strings.HasPrefix(base, ".polypod-backup-")) && strings.HasSuffix(base, ".zip"))
}

// isPriorPolypodBackup recognizes our manifest in a ZIP with any filename.
// Ordinary ZIPs in the project remain user data and are still backed up.
func isPriorPolypodBackup(path string) bool {
	if !strings.EqualFold(filepath.Ext(path), ".zip") {
		return false
	}
	zr, err := zip.OpenReader(path)
	if err != nil {
		return false
	}
	defer zr.Close()
	for _, member := range zr.File {
		if member.Name != manifestName || member.UncompressedSize64 > uint64(maxManifestBytes) {
			continue
		}
		data, err := readEntry(member, maxManifestBytes)
		if err != nil {
			return false
		}
		var manifest Manifest
		if err := json.Unmarshal(data, &manifest); err != nil {
			return false
		}
		return manifest.Version == archiveVersion && !manifest.CreatedAt.IsZero() &&
			len(manifest.Files) > 0 && len(manifest.Files) == len(zr.File)-1
	}
	return false
}

func isSecretPath(rel string) bool {
	base := strings.ToLower(filepath.Base(rel))
	if base == ".env" || strings.HasPrefix(base, ".env.") {
		return true
	}
	switch base {
	case "credentials.enc", "credentials.json", "secrets.enc", "chatgpt-auth.enc",
		"auth_token", "oauth-token.json", "oauth_tokens.json", "token.json":
		return true
	}
	return strings.Contains(base, "private-key") || strings.Contains(base, "private_key") || strings.HasSuffix(base, ".pem") || strings.HasSuffix(base, ".key")
}

func addSource(ctx context.Context, zw *zip.Writer, source sourceFile, secrets bool) (Entry, error) {
	info, err := os.Lstat(source.path)
	if err != nil {
		return Entry{}, fmt.Errorf("inspecting %s: %w", source.path, err)
	}
	if !info.Mode().IsRegular() || info.Size() > maxFileBytes {
		return Entry{}, fmt.Errorf("unsupported backup source %s", source.path)
	}
	entry := Entry{Root: source.root, Path: filepath.ToSlash(source.rel), Mode: uint32(info.Mode().Perm())}
	header := &zip.FileHeader{Name: source.root + "/" + entry.Path, Method: zip.Deflate}
	header.SetMode(info.Mode().Perm())
	w, err := zw.CreateHeader(header)
	if err != nil {
		return Entry{}, fmt.Errorf("creating archive entry %s: %w", header.Name, err)
	}
	h := sha256.New()
	var reader io.Reader
	var cleanup func()
	if isSQLite(source.path) {
		path, err := snapshotSQLite(ctx, source.path)
		if err != nil {
			return Entry{}, err
		}
		f, err := os.Open(path)
		if err != nil {
			os.Remove(path)
			return Entry{}, err
		}
		reader = f
		cleanup = func() { f.Close(); os.Remove(path) }
	} else if !secrets && isConfigFile(source) {
		data, err := os.ReadFile(source.path)
		if err != nil {
			return Entry{}, fmt.Errorf("reading config %s: %w", source.path, err)
		}
		if int64(len(data)) > maxManifestBytes {
			return Entry{}, fmt.Errorf("config file too large: %s", source.path)
		}
		data, err = redactConfig(source.path, data)
		if err != nil {
			return Entry{}, fmt.Errorf("redacting config %s: %w", source.path, err)
		}
		reader = strings.NewReader(string(data))
		entry.Redacted = true
	} else {
		f, err := os.Open(source.path)
		if err != nil {
			return Entry{}, fmt.Errorf("opening %s: %w", source.path, err)
		}
		reader = f
		cleanup = func() { f.Close() }
	}
	if cleanup != nil {
		defer cleanup()
	}
	n, err := io.Copy(io.MultiWriter(w, h), reader)
	if err != nil {
		return Entry{}, fmt.Errorf("archiving %s: %w", source.path, err)
	}
	if n > maxFileBytes {
		return Entry{}, fmt.Errorf("archive file exceeds size limit: %s", source.path)
	}
	if !isSQLite(source.path) {
		after, err := os.Lstat(source.path)
		if err != nil {
			return Entry{}, fmt.Errorf("checking source after copy %s: %w", source.path, err)
		}
		if !os.SameFile(info, after) || info.Size() != after.Size() || !info.ModTime().Equal(after.ModTime()) {
			return Entry{}, fmt.Errorf("source changed during backup: %s", source.path)
		}
	}
	entry.Size, entry.SHA256 = n, hex.EncodeToString(h.Sum(nil))
	return entry, nil
}

func isSQLite(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".db", ".sqlite", ".sqlite3":
		return true
	}
	return false
}

func snapshotSQLite(ctx context.Context, source string) (string, error) {
	f, err := os.CreateTemp("", "polypod-sqlite-*.db")
	if err != nil {
		return "", fmt.Errorf("creating SQLite snapshot: %w", err)
	}
	path := f.Name()
	if err := f.Close(); err != nil {
		os.Remove(path)
		return "", err
	}
	if err := os.Remove(path); err != nil {
		return "", err
	}
	db, err := sql.Open("sqlite", source)
	if err != nil {
		return "", fmt.Errorf("opening SQLite %s: %w", source, err)
	}
	defer db.Close()
	if err := db.PingContext(ctx); err != nil {
		return "", fmt.Errorf("opening SQLite %s: %w", source, err)
	}
	escaped := strings.ReplaceAll(path, "'", "''")
	if _, err := db.ExecContext(ctx, "VACUUM INTO '"+escaped+"'"); err != nil {
		os.Remove(path)
		return "", fmt.Errorf("snapshotting SQLite %s: %w", source, err)
	}
	return path, nil
}

func isConfigFile(s sourceFile) bool {
	if s.root == "config" {
		return true
	}
	if s.root != "project" {
		return false
	}
	if strings.HasPrefix(s.rel, "config.d/") {
		return true
	}
	return s.rel == ".polypod/settings.json"
}

func redactConfig(path string, data []byte) ([]byte, error) {
	if strings.EqualFold(filepath.Ext(path), ".json") {
		var value any
		if err := json.Unmarshal(data, &value); err != nil {
			return nil, err
		}
		redactJSON(value)
		return json.MarshalIndent(value, "", "  ")
	}
	var node yaml.Node
	if err := yaml.Unmarshal(data, &node); err != nil {
		return nil, err
	}
	redactYAML(&node)
	return yaml.Marshal(&node)
}

func sensitiveKey(key string) bool {
	// Normalize snake_case, kebab-case and camelCase alike. Config snippets
	// and settings.json use different naming conventions for the same secrets.
	key = strings.Map(func(r rune) rune {
		switch r {
		case '_', '-', ' ', '.':
			return -1
		}
		return r
	}, strings.ToLower(key))
	switch key {
	case "apikey", "bottoken", "accesstoken", "refreshtoken", "clientsecret",
		"appsecret", "password", "passwd", "passphrase", "secret", "secretkey",
		"token", "privatekey", "authorization", "cookie", "authtoken", "credential", "credentials":
		return true
	}
	return strings.HasSuffix(key, "apikey") || strings.HasSuffix(key, "secret") ||
		strings.HasSuffix(key, "secretkey") || strings.HasSuffix(key, "token") ||
		strings.HasSuffix(key, "password") || strings.HasSuffix(key, "passphrase") ||
		strings.HasSuffix(key, "privatekey")
}

func redactJSON(value any) {
	switch v := value.(type) {
	case map[string]any:
		for key, child := range v {
			if sensitiveKey(key) {
				v[key] = redactedValue
			} else {
				redactJSON(child)
			}
		}
	case []any:
		for _, child := range v {
			redactJSON(child)
		}
	}
}

func redactYAML(node *yaml.Node) {
	if node.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(node.Content); i += 2 {
			key, value := node.Content[i], node.Content[i+1]
			if sensitiveKey(key.Value) {
				value.Kind, value.Tag, value.Value, value.Content = yaml.ScalarNode, "!!str", redactedValue, nil
			} else {
				redactYAML(value)
			}
		}
		return
	}
	for _, child := range node.Content {
		redactYAML(child)
	}
}
