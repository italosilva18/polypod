package statebackup

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"gopkg.in/yaml.v3"
)

// Change describes a file that would be created, replaced, or left unchanged.
type Change struct {
	Entry         Entry
	Target        string
	Action        string
	CurrentSHA256 string
}

// Preview verifies every archive member before it plans any file changes.
func Preview(ctx context.Context, layout Layout, archivePath string) (Manifest, []Change, error) {
	return PreviewSelected(ctx, layout, archivePath, nil)
}

// PreviewSelected plans only the requested archive roots or path prefixes.
// The entire archive is still checked for corruption before planning.
func PreviewSelected(ctx context.Context, layout Layout, archivePath string, only []string) (Manifest, []Change, error) {
	layout, err := normalizeLayout(layout)
	if err != nil {
		return Manifest{}, nil, err
	}
	manifest, files, closeArchive, err := openVerified(ctx, archivePath)
	if err != nil {
		return Manifest{}, nil, err
	}
	defer closeArchive()
	changes := make([]Change, 0, len(manifest.Files))
	seenTargets := make(map[string]bool)
	for _, entry := range manifest.Files {
		if !selected(entry, only) {
			continue
		}
		target, err := targetFor(layout, entry)
		if err != nil {
			return Manifest{}, nil, err
		}
		archiveAbsolute, err := filepath.Abs(archivePath)
		if err != nil {
			return Manifest{}, nil, err
		}
		if target == archiveAbsolute {
			return Manifest{}, nil, fmt.Errorf("backup archive cannot be a restore target")
		}
		if err := checkSQLiteSidecars(target); err != nil {
			return Manifest{}, nil, err
		}
		if seenTargets[target] {
			return Manifest{}, nil, fmt.Errorf("archive maps multiple entries to %s", target)
		}
		seenTargets[target] = true
		change := Change{Entry: entry, Target: target, Action: "create"}
		info, err := os.Lstat(target)
		if err == nil {
			if !info.Mode().IsRegular() {
				return Manifest{}, nil, fmt.Errorf("restore target is not a regular file: %s", target)
			}
			current, err := hashFile(target)
			if err != nil {
				return Manifest{}, nil, fmt.Errorf("reading restore target %s: %w", target, err)
			}
			change.CurrentSHA256 = current
			change.Action = "replace"
		} else if !errors.Is(err, os.ErrNotExist) {
			return Manifest{}, nil, fmt.Errorf("checking restore target %s: %w", target, err)
		}
		desired := entry.SHA256
		if entry.Redacted {
			content, err := readEntry(files[entry.Root+"/"+entry.Path], maxManifestBytes)
			if err != nil {
				return Manifest{}, nil, err
			}
			content, err = mergeConfigSecrets(target, content)
			if err != nil {
				return Manifest{}, nil, err
			}
			h := sha256.Sum256(content)
			desired = hex.EncodeToString(h[:])
		}
		if change.CurrentSHA256 != "" && change.CurrentSHA256 == desired {
			change.Action = "unchanged"
		}
		changes = append(changes, change)
	}
	if len(only) > 0 && len(changes) == 0 {
		return Manifest{}, nil, fmt.Errorf("nenhum arquivo corresponde a --only")
	}
	return manifest, changes, nil
}

func selected(entry Entry, only []string) bool {
	if len(only) == 0 {
		return true
	}
	name := entry.Root + "/" + entry.Path
	for _, filter := range only {
		filter = strings.TrimSuffix(strings.TrimSpace(filter), "/")
		if filter == entry.Root || name == filter || strings.HasPrefix(name, filter+"/") {
			return true
		}
	}
	return false
}

// Inspect checks archive integrity without needing a local Polypod config.
func Inspect(ctx context.Context, archivePath string) (Manifest, error) {
	manifest, _, closeArchive, err := openVerified(ctx, archivePath)
	if err != nil {
		return Manifest{}, err
	}
	defer closeArchive()
	return manifest, nil
}

type stagedFile struct {
	change    Change
	temp      string
	old       string
	saved     bool
	installed bool
}

// Restore stages all changed files, rechecks the previewed targets, then installs
// them with per-file atomic renames. Errors during installation trigger rollback.
// The runtime must be stopped before this operation; concurrent writers cannot
// participate in the restore transaction.
func Restore(ctx context.Context, layout Layout, archivePath string) (Manifest, []Change, error) {
	return RestoreSelected(ctx, layout, archivePath, nil)
}

// RestoreSelected applies only the requested roots or path prefixes.
func RestoreSelected(ctx context.Context, layout Layout, archivePath string, only []string) (Manifest, []Change, error) {
	var err error
	layout, err = normalizeLayout(layout)
	if err != nil {
		return Manifest{}, nil, err
	}
	manifest, changes, err := PreviewSelected(ctx, layout, archivePath, only)
	if err != nil {
		return Manifest{}, nil, err
	}
	stagedManifest, files, closeArchive, err := openVerified(ctx, archivePath)
	if err != nil {
		return Manifest{}, nil, err
	}
	defer closeArchive()
	if !reflect.DeepEqual(manifest, stagedManifest) {
		return Manifest{}, nil, fmt.Errorf("backup archive changed since restore preview")
	}
	staged := make([]*stagedFile, 0, len(changes))
	defer func() {
		for _, s := range staged {
			if s.temp != "" {
				os.Remove(s.temp)
			}
			if s.old != "" && !s.saved {
				os.Remove(s.old)
			}
		}
	}()
	for _, change := range changes {
		if change.Action == "unchanged" {
			continue
		}
		if _, err := targetFor(layout, change.Entry); err != nil {
			return Manifest{}, nil, err
		}
		if err := ctx.Err(); err != nil {
			return Manifest{}, nil, err
		}
		if err := os.MkdirAll(filepath.Dir(change.Target), 0700); err != nil {
			return Manifest{}, nil, fmt.Errorf("creating target directory: %w", err)
		}
		tmp, err := os.CreateTemp(filepath.Dir(change.Target), ".polypod-restore-*")
		if err != nil {
			return Manifest{}, nil, fmt.Errorf("staging %s: %w", change.Target, err)
		}
		s := &stagedFile{change: change, temp: tmp.Name()}
		staged = append(staged, s)
		mode := os.FileMode(0600)
		if change.Entry.Mode&0111 != 0 {
			mode = 0700
		}
		if err := tmp.Chmod(mode); err != nil {
			tmp.Close()
			return Manifest{}, nil, err
		}
		zf := files[change.Entry.Root+"/"+change.Entry.Path]
		if change.Entry.Redacted {
			content, err := readEntry(zf, maxManifestBytes)
			if err != nil {
				tmp.Close()
				return Manifest{}, nil, err
			}
			if err := verifyStagedContent(change.Entry, content); err != nil {
				tmp.Close()
				return Manifest{}, nil, err
			}
			content, err = mergeConfigSecrets(change.Target, content)
			if err != nil {
				tmp.Close()
				return Manifest{}, nil, err
			}
			if _, err := tmp.Write(content); err != nil {
				tmp.Close()
				return Manifest{}, nil, err
			}
		} else {
			r, err := zf.Open()
			if err != nil {
				tmp.Close()
				return Manifest{}, nil, err
			}
			hash := sha256.New()
			copied, copyErr := io.Copy(io.MultiWriter(tmp, hash), io.LimitReader(r, change.Entry.Size+1))
			closeErr := r.Close()
			if copyErr != nil {
				tmp.Close()
				return Manifest{}, nil, copyErr
			}
			if closeErr != nil {
				tmp.Close()
				return Manifest{}, nil, closeErr
			}
			if copied != change.Entry.Size || hex.EncodeToString(hash.Sum(nil)) != change.Entry.SHA256 {
				tmp.Close()
				return Manifest{}, nil, fmt.Errorf("backup entry changed while staging: %s/%s",
					change.Entry.Root, change.Entry.Path)
			}
		}
		if err := tmp.Sync(); err != nil {
			tmp.Close()
			return Manifest{}, nil, err
		}
		if err := tmp.Close(); err != nil {
			return Manifest{}, nil, err
		}
	}
	for _, s := range staged {
		if err := ctx.Err(); err != nil {
			return Manifest{}, nil, restoreFailure(err, staged)
		}
		if _, err := targetFor(layout, s.change.Entry); err != nil {
			return Manifest{}, nil, restoreFailure(err, staged)
		}
		if err := checkSQLiteSidecars(s.change.Target); err != nil {
			return Manifest{}, nil, restoreFailure(err, staged)
		}
		current, err := currentHash(s.change.Target)
		if err != nil {
			return Manifest{}, nil, restoreFailure(err, staged)
		}
		if current != s.change.CurrentSHA256 {
			return Manifest{}, nil, restoreFailure(fmt.Errorf("restore target changed since preview: %s", s.change.Target), staged)
		}
		if current != "" {
			oldFile, err := os.CreateTemp(filepath.Dir(s.change.Target), ".polypod-before-restore-*")
			if err != nil {
				return Manifest{}, nil, restoreFailure(err, staged)
			}
			s.old = oldFile.Name()
			if err := oldFile.Close(); err != nil {
				return Manifest{}, nil, restoreFailure(err, staged)
			}
			if err := os.Remove(s.old); err != nil {
				return Manifest{}, nil, restoreFailure(err, staged)
			}
			if err := os.Rename(s.change.Target, s.old); err != nil {
				return Manifest{}, nil, restoreFailure(fmt.Errorf("saving current %s: %w", s.change.Target, err), staged)
			}
			s.saved = true
		}
		if err := os.Rename(s.temp, s.change.Target); err != nil {
			return Manifest{}, nil, restoreFailure(fmt.Errorf("installing %s: %w", s.change.Target, err), staged)
		}
		s.installed, s.temp = true, ""
	}
	for _, s := range staged {
		if s.old != "" {
			if err := os.Remove(s.old); err != nil {
				return manifest, changes, fmt.Errorf("removing rollback file %s: %w", s.old, err)
			}
		}
	}
	return manifest, changes, nil
}

func verifyStagedContent(entry Entry, data []byte) error {
	hash := sha256.Sum256(data)
	if int64(len(data)) != entry.Size || hex.EncodeToString(hash[:]) != entry.SHA256 {
		return fmt.Errorf("backup entry changed while staging: %s/%s", entry.Root, entry.Path)
	}
	return nil
}

func checkSQLiteSidecars(path string) error {
	if !isSQLite(path) {
		return nil
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		sidecar := path + suffix
		if _, err := os.Lstat(sidecar); err == nil {
			return fmt.Errorf("arquivo SQLite auxiliar ainda existe: %s; pare o serviço e faça checkpoint do banco antes de restaurar", sidecar)
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("verificando arquivo SQLite auxiliar %s: %w", sidecar, err)
		}
	}
	return nil
}

func restoreFailure(cause error, staged []*stagedFile) error {
	if rollbackErr := rollback(staged); rollbackErr != nil {
		return fmt.Errorf("%w; rollback incompleto: %v", cause, rollbackErr)
	}
	return cause
}

func rollback(staged []*stagedFile) error {
	var failures []error
	for i := len(staged) - 1; i >= 0; i-- {
		s := staged[i]
		if s.installed {
			if err := os.Remove(s.change.Target); err != nil && !errors.Is(err, os.ErrNotExist) {
				failures = append(failures, fmt.Errorf("removing restored %s: %w", s.change.Target, err))
			}
		}
		if s.saved {
			if err := os.Rename(s.old, s.change.Target); err != nil {
				failures = append(failures, fmt.Errorf("recovering original %s from %s: %w", s.change.Target, s.old, err))
			}
		}
	}
	return errors.Join(failures...)
}

func currentHash(path string) (string, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("restore target is not a regular file: %s", path)
	}
	return hashFile(path)
}

func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func openVerified(ctx context.Context, path string) (Manifest, map[string]*zip.File, func(), error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return Manifest{}, nil, nil, fmt.Errorf("opening backup archive: %w", err)
	}
	closeArchive := func() { zr.Close() }
	if len(zr.File) == 0 || len(zr.File) > maxEntries+1 {
		closeArchive()
		return Manifest{}, nil, nil, fmt.Errorf("invalid archive entry count")
	}
	files := make(map[string]*zip.File, len(zr.File))
	for _, f := range zr.File {
		if _, exists := files[f.Name]; exists {
			closeArchive()
			return Manifest{}, nil, nil, fmt.Errorf("duplicate archive entry: %s", f.Name)
		}
		if f.Mode()&os.ModeSymlink != 0 || !f.Mode().IsRegular() {
			closeArchive()
			return Manifest{}, nil, nil, fmt.Errorf("unsupported archive member %s", f.Name)
		}
		if f.UncompressedSize64 > uint64(maxFileBytes) {
			closeArchive()
			return Manifest{}, nil, nil, fmt.Errorf("archive member too large: %s", f.Name)
		}
		files[f.Name] = f
	}
	manifestFile := files[manifestName]
	if manifestFile == nil || manifestFile.UncompressedSize64 > uint64(maxManifestBytes) {
		closeArchive()
		return Manifest{}, nil, nil, fmt.Errorf("archive manifest missing or too large")
	}
	data, err := readEntry(manifestFile, maxManifestBytes)
	if err != nil {
		closeArchive()
		return Manifest{}, nil, nil, err
	}
	var manifest Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		closeArchive()
		return Manifest{}, nil, nil, fmt.Errorf("decoding archive manifest: %w", err)
	}
	if manifest.Version != archiveVersion || len(manifest.Files) != len(files)-1 {
		closeArchive()
		return Manifest{}, nil, nil, fmt.Errorf("unsupported or incomplete backup manifest")
	}
	listed := make(map[string]bool, len(manifest.Files))
	for _, entry := range manifest.Files {
		if err := ctx.Err(); err != nil {
			closeArchive()
			return Manifest{}, nil, nil, err
		}
		if err := validRelative(entry.Path); err != nil {
			closeArchive()
			return Manifest{}, nil, nil, err
		}
		if !validRoot(entry.Root) {
			closeArchive()
			return Manifest{}, nil, nil, fmt.Errorf("unknown backup root %q", entry.Root)
		}
		if !manifest.SecretsIncluded {
			if entry.Root == "whatsapp" || isSecretPath(entry.Path) {
				closeArchive()
				return Manifest{}, nil, nil, fmt.Errorf("backup contains credentials despite secrets_included=false")
			}
			if isConfigFile(sourceFile{root: entry.Root, rel: entry.Path}) && !entry.Redacted {
				closeArchive()
				return Manifest{}, nil, nil, fmt.Errorf("backup contains unredacted config despite secrets_included=false")
			}
		}
		if entry.Size < 0 || entry.Size > maxFileBytes || entry.Mode > 0777 {
			closeArchive()
			return Manifest{}, nil, nil, fmt.Errorf("invalid metadata for %s", entry.Path)
		}
		name := entry.Root + "/" + entry.Path
		if listed[name] {
			closeArchive()
			return Manifest{}, nil, nil, fmt.Errorf("duplicate manifest entry: %s", name)
		}
		listed[name] = true
		f := files[name]
		if f == nil || int64(f.UncompressedSize64) != entry.Size {
			closeArchive()
			return Manifest{}, nil, nil, fmt.Errorf("missing or size-mismatched backup entry %s", name)
		}
		r, err := f.Open()
		if err != nil {
			closeArchive()
			return Manifest{}, nil, nil, err
		}
		h := sha256.New()
		actual, copyErr := io.Copy(h, io.LimitReader(r, maxFileBytes+1))
		closeErr := r.Close()
		if copyErr != nil || closeErr != nil || actual != entry.Size || hex.EncodeToString(h.Sum(nil)) != entry.SHA256 {
			closeArchive()
			return Manifest{}, nil, nil, fmt.Errorf("backup entry failed integrity check: %s", name)
		}
	}
	return manifest, files, closeArchive, nil
}

func readEntry(f *zip.File, limit int64) ([]byte, error) {
	if f == nil || int64(f.UncompressedSize64) > limit {
		return nil, fmt.Errorf("archive entry unavailable or too large")
	}
	r, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return io.ReadAll(io.LimitReader(r, limit+1))
}

func validRoot(root string) bool {
	switch root {
	case "config", "project", "runtime_data", "agents", "database", "scheduler", "whatsapp":
		return true
	}
	return false
}

func targetFor(l Layout, entry Entry) (string, error) {
	var base string
	switch entry.Root {
	case "config":
		if entry.Path != "config.yaml" {
			return "", fmt.Errorf("invalid config entry %s", entry.Path)
		}
		if err := rejectSymlinkParents(filepath.Dir(l.ConfigPath), l.ConfigPath); err != nil {
			return "", err
		}
		return l.ConfigPath, nil
	case "project":
		if !allowedProjectPath(l, entry.Path) {
			return "", fmt.Errorf("archive path is outside Polypod state: %s", entry.Path)
		}
		base = l.ProjectDir
	case "runtime_data":
		base = l.DataDir
	case "agents":
		base = l.AgentsDir
	case "database":
		if entry.Path != filepath.Base(l.DatabasePath) {
			return "", fmt.Errorf("invalid database entry %s", entry.Path)
		}
		if err := rejectSymlinkParents(filepath.Dir(l.DatabasePath), l.DatabasePath); err != nil {
			return "", err
		}
		return l.DatabasePath, nil
	case "scheduler":
		if entry.Path != filepath.Base(l.SchedulerPath) {
			return "", fmt.Errorf("invalid scheduler entry %s", entry.Path)
		}
		if err := rejectSymlinkParents(filepath.Dir(l.SchedulerPath), l.SchedulerPath); err != nil {
			return "", err
		}
		return l.SchedulerPath, nil
	case "whatsapp":
		base = l.WhatsAppSessionDir
	default:
		return "", fmt.Errorf("unknown backup root %q", entry.Root)
	}
	if base == "" {
		return "", fmt.Errorf("restore destination is not configured for %s", entry.Root)
	}
	target := filepath.Join(base, filepath.FromSlash(entry.Path))
	if !within(base, target) {
		return "", fmt.Errorf("unsafe restore target %s", target)
	}
	if err := rejectSymlinkParents(base, target); err != nil {
		return "", err
	}
	return target, nil
}

func allowedProjectPath(l Layout, rel string) bool {
	if rel == ".env" || rel == ".env.local" {
		return true
	}
	for _, prefix := range []string{".polypod/", "config.d/", "data/"} {
		if strings.HasPrefix(rel, prefix) {
			return true
		}
	}
	for _, path := range []string{l.AgentsDir, l.DataDir} {
		if within(l.ProjectDir, path) {
			prefix, err := filepath.Rel(l.ProjectDir, path)
			if err == nil && (rel == filepath.ToSlash(prefix) || strings.HasPrefix(rel, filepath.ToSlash(prefix)+"/")) {
				return true
			}
		}
	}
	for _, path := range []string{l.DatabasePath, l.SchedulerPath} {
		if within(l.ProjectDir, path) {
			candidate, err := filepath.Rel(l.ProjectDir, path)
			if err == nil && rel == filepath.ToSlash(candidate) {
				return true
			}
		}
	}
	return false
}

func rejectSymlinkParents(base, target string) error {
	base = filepath.Clean(base)
	for ancestor := base; ; ancestor = filepath.Dir(ancestor) {
		info, err := os.Lstat(ancestor)
		if err == nil && info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("restore parent is a symbolic link: %s", ancestor)
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if filepath.Dir(ancestor) == ancestor {
			break
		}
	}
	rel, err := filepath.Rel(base, filepath.Dir(target))
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("restore path escapes its destination root: %s", target)
	}
	paths := []string{base}
	current := base
	if rel != "." {
		for _, part := range strings.Split(rel, string(filepath.Separator)) {
			current = filepath.Join(current, part)
			paths = append(paths, current)
		}
	}
	for _, path := range paths {
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("restore parent is not a plain directory: %s", path)
		}
	}
	return nil
}

func mergeConfigSecrets(target string, saved []byte) ([]byte, error) {
	current, err := os.ReadFile(target)
	if errors.Is(err, os.ErrNotExist) {
		current = nil
	} else if err != nil {
		return nil, err
	}
	if strings.EqualFold(filepath.Ext(target), ".json") {
		var savedValue, currentValue any
		if err := json.Unmarshal(saved, &savedValue); err != nil {
			return nil, err
		}
		if len(current) > 0 {
			if err := json.Unmarshal(current, &currentValue); err != nil {
				return nil, fmt.Errorf("decoding current config: %w", err)
			}
		}
		return json.MarshalIndent(mergeJSONSecrets(savedValue, currentValue), "", "  ")
	}
	var savedNode, currentNode yaml.Node
	if err := yaml.Unmarshal(saved, &savedNode); err != nil {
		return nil, err
	}
	if len(current) > 0 {
		if err := yaml.Unmarshal(current, &currentNode); err != nil {
			return nil, fmt.Errorf("decoding current config: %w", err)
		}
	}
	mergeYAMLSecrets(&savedNode, &currentNode)
	return yaml.Marshal(&savedNode)
}

func mergeJSONSecrets(saved, current any) any {
	if s, ok := saved.(string); ok && s == redactedValue {
		if current != nil {
			return current
		}
		return ""
	}
	switch value := saved.(type) {
	case map[string]any:
		curr, _ := current.(map[string]any)
		for key, child := range value {
			value[key] = mergeJSONSecrets(child, curr[key])
		}
	case []any:
		curr, _ := current.([]any)
		for i, child := range value {
			var old any
			if i < len(curr) {
				old = curr[i]
			}
			value[i] = mergeJSONSecrets(child, old)
		}
	}
	return saved
}

func mergeYAMLSecrets(saved, current *yaml.Node) {
	if saved.Kind == yaml.ScalarNode && saved.Value == redactedValue {
		if current != nil && current.Kind != 0 {
			*saved = *current
		} else {
			saved.Value = ""
		}
		return
	}
	if saved.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(saved.Content); i += 2 {
			var old *yaml.Node
			if current != nil && current.Kind == yaml.MappingNode {
				for j := 0; j+1 < len(current.Content); j += 2 {
					if current.Content[j].Value == saved.Content[i].Value {
						old = current.Content[j+1]
						break
					}
				}
			}
			mergeYAMLSecrets(saved.Content[i+1], old)
		}
		return
	}
	for i, child := range saved.Content {
		var old *yaml.Node
		if current != nil && i < len(current.Content) {
			old = current.Content[i]
		}
		mergeYAMLSecrets(child, old)
	}
}
