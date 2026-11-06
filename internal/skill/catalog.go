package skill

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
)

const (
	maxCatalogFiles = 128
	maxCatalogBytes = 8 << 20
	maxCatalogFile  = 2 << 20
)

// Catalog imports only the checked-in PolyClaw skill snapshot. It never
// contacts a network registry or executes installation scripts.
type Catalog struct {
	sourceRoot string
	activeRoot string
	registry   *Registry
	mu         sync.Mutex
}

type CatalogEntry struct {
	Slug        string  `json:"slug"`
	Name        string  `json:"name"`
	Description string  `json:"description"`
	Origin      string  `json:"origin"`
	Status      string  `json:"status"`
	ActiveAs    string  `json:"active_as,omitempty"`
	Score       float64 `json:"score"`
}

type InstallResult struct {
	Slug         string `json:"slug"`
	Name         string `json:"name"`
	Status       string `json:"status"`
	Path         string `json:"path"`
	SourceSHA256 string `json:"source_sha256,omitempty"`
}

type originMetadata struct {
	Version      int    `json:"version"`
	OriginKind   string `json:"origin_kind"`
	Catalog      string `json:"catalog"`
	Slug         string `json:"slug"`
	SourceSHA256 string `json:"source_sha256"`
	InstalledAt  string `json:"installed_at"`
}

// NewLocalCatalog accepts directories supplied by the application, not by a
// tool call. The active registry is notified immediately after an install;
// filesystem watcher scans will also find the new complete directory.
func NewLocalCatalog(sourceRoot, activeRoot string, registry *Registry) *Catalog {
	return &Catalog{sourceRoot: sourceRoot, activeRoot: activeRoot, registry: registry}
}

var adaptedPolyClawNames = map[string]string{
	"agent-browser": "polyclaw_browser",
	"github":        "polyclaw_github",
	"hardware":      "polyclaw_hardware",
	"skill-creator": "polyclaw_skill_creator",
	"summarize":     "polyclaw_summarize",
	"tmux":          "polyclaw_tmux",
	"weather":       "polyclaw_weather",
}

// Find searches both the reference catalog and active local skills. Source
// entries already adapted by Polypod point to the active adaptation instead
// of suggesting a duplicate raw installation.
func (c *Catalog) Find(ctx context.Context, query string, limit int) ([]CatalogEntry, error) {
	if c == nil {
		return nil, errors.New("catálogo de skills indisponível")
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, errors.New("consulta de skills obrigatória")
	}
	if limit <= 0 {
		limit = 5
	}
	if limit > 20 {
		limit = 20
	}
	sourceRoot, err := safeRoot(c.sourceRoot, false)
	if err != nil {
		return nil, fmt.Errorf("catálogo PolyClaw: %w", err)
	}
	activeRoot, err := safeRoot(c.activeRoot, true)
	if err != nil {
		return nil, fmt.Errorf("skills locais: %w", err)
	}
	active, activeErr := readCatalogRoot(ctx, activeRoot, false)
	source, sourceErr := readCatalogRoot(ctx, sourceRoot, true)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	activeByName := make(map[string]CatalogEntry, len(active))
	for _, entry := range active {
		activeByName[entry.Name] = entry
	}
	accounted := make(map[string]bool)
	entries := make([]CatalogEntry, 0, len(source)+len(active))
	for _, entry := range source {
		entry.Origin = "polyclaw"
		entry.Status = "available"
		if adapted := adaptedPolyClawNames[entry.Slug]; adapted != "" {
			if _, ok := activeByName[adapted]; ok {
				entry.Status, entry.ActiveAs = "active_adapter", adapted
				accounted[adapted] = true
			}
		}
		if _, ok := activeByName[entry.Slug]; ok {
			entry.Status, entry.ActiveAs = "installed", entry.Slug
			accounted[entry.Slug] = true
		}
		entries = append(entries, entry)
	}
	for _, entry := range active {
		if accounted[entry.Name] {
			continue
		}
		entry.Origin, entry.Status, entry.ActiveAs = "local", "active", entry.Name
		entries = append(entries, entry)
	}
	filtered := entries[:0]
	for _, entry := range entries {
		entry.Score = catalogScore(query, entry)
		if entry.Score > 0 {
			filtered = append(filtered, entry)
		}
	}
	sort.Slice(filtered, func(i, j int) bool {
		if filtered[i].Score != filtered[j].Score {
			return filtered[i].Score > filtered[j].Score
		}
		if filtered[i].Origin != filtered[j].Origin {
			return filtered[i].Origin < filtered[j].Origin
		}
		return filtered[i].Slug < filtered[j].Slug
	})
	if len(filtered) > limit {
		filtered = filtered[:limit]
	}
	return filtered, errors.Join(activeErr, sourceErr)
}

// Install copies one unadapted source skill and its resources to the active
// directory. A fully staged, validated directory is renamed into place, so
// the watcher never sees partial files. Existing skills are never overwritten.
func (c *Catalog) Install(ctx context.Context, slug string) (InstallResult, error) {
	if c == nil {
		return InstallResult{}, errors.New("catálogo de skills indisponível")
	}
	if !validCatalogSlug(slug) {
		return InstallResult{}, errors.New("identificador de skill inválido")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return InstallResult{}, err
	}
	sourceRoot, err := safeRoot(c.sourceRoot, false)
	if err != nil {
		return InstallResult{}, fmt.Errorf("catálogo PolyClaw: %w", err)
	}
	activeRoot, err := safeRoot(c.activeRoot, true)
	if err != nil {
		return InstallResult{}, fmt.Errorf("skills locais: %w", err)
	}
	sourceDir := filepath.Join(sourceRoot, slug)
	sourceSkill, err := readCatalogSkill(sourceDir, slug, true)
	if err != nil {
		return InstallResult{}, fmt.Errorf("skill %q não disponível no catálogo: %w", slug, err)
	}
	if adapted := adaptedPolyClawNames[slug]; adapted != "" {
		adaptedPath := filepath.Join(activeRoot, adapted)
		if _, statErr := os.Lstat(adaptedPath); statErr == nil {
			if _, err := readCatalogSkill(adaptedPath, adapted, false); err != nil {
				return InstallResult{}, fmt.Errorf("adaptação %q inválida: %w", adapted, err)
			}
			return InstallResult{Slug: slug, Name: adapted, Status: "already_active", Path: adaptedPath}, nil
		} else if !errors.Is(statErr, os.ErrNotExist) {
			return InstallResult{}, statErr
		}
	}
	targetDir := filepath.Join(activeRoot, slug)
	finalOrigin := FileSkillOrigin(filepath.Join(targetDir, "SKILL.md"))
	if c.registry != nil {
		if existing, ok := c.registry.Get(sourceSkill.Name); ok && existing.Origin != finalOrigin {
			return InstallResult{}, fmt.Errorf("skill %q já pertence a outra origem", sourceSkill.Name)
		}
	}
	if _, statErr := os.Lstat(targetDir); statErr == nil {
		if _, err := readCatalogSkill(targetDir, slug, false); err != nil {
			return InstallResult{}, fmt.Errorf("destino existente inválido: %w", err)
		}
		return InstallResult{Slug: slug, Name: sourceSkill.Name, Status: "already_installed", Path: targetDir}, nil
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return InstallResult{}, statErr
	}
	parent := filepath.Dir(activeRoot)
	if err := os.MkdirAll(parent, 0755); err != nil {
		return InstallResult{}, err
	}
	if _, err := safeRoot(parent, false); err != nil {
		return InstallResult{}, err
	}
	if err := os.MkdirAll(activeRoot, 0755); err != nil {
		return InstallResult{}, err
	}
	if _, err := safeRoot(activeRoot, false); err != nil {
		return InstallResult{}, err
	}
	stage, err := os.MkdirTemp(parent, ".polypod-skill-stage-*")
	if err != nil {
		return InstallResult{}, err
	}
	defer os.RemoveAll(stage)
	digest, err := copyCatalogTree(ctx, sourceDir, stage)
	if err != nil {
		return InstallResult{}, err
	}
	if _, err := readCatalogSkill(stage, slug, true); err != nil {
		return InstallResult{}, fmt.Errorf("skill copiada inválida: %w", err)
	}
	data, err := os.ReadFile(filepath.Join(stage, "SKILL.md"))
	if err != nil {
		return InstallResult{}, err
	}
	loaded, err := skillFromParsed(filepath.Join(stage, "SKILL.md"), data)
	if err != nil {
		return InstallResult{}, fmt.Errorf("registrando skill copiada: %w", err)
	}
	meta := originMetadata{Version: 1, OriginKind: "local_catalog", Catalog: "polyclaw", Slug: slug,
		SourceSHA256: digest, InstalledAt: time.Now().UTC().Format(time.RFC3339)}
	raw, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return InstallResult{}, err
	}
	if err := os.WriteFile(filepath.Join(stage, ".skill-origin.json"), raw, 0644); err != nil {
		return InstallResult{}, err
	}
	if err := os.Chmod(stage, 0755); err != nil {
		return InstallResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return InstallResult{}, err
	}
	if err := os.Rename(stage, targetDir); err != nil {
		return InstallResult{}, fmt.Errorf("ativando skill %q: %w", slug, err)
	}
	if c.registry != nil {
		loaded.Origin = finalOrigin
		if err := c.registry.TryRegister(loaded); err != nil {
			return InstallResult{}, fmt.Errorf("registrando skill %q: %w", slug, err)
		}
	}
	return InstallResult{Slug: slug, Name: sourceSkill.Name, Status: "installed", Path: targetDir,
		SourceSHA256: digest}, nil
}

func safeRoot(path string, mayBeMissing bool) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("caminho do catálogo vazio")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	info, err := os.Lstat(abs)
	if errors.Is(err, os.ErrNotExist) && mayBeMissing {
		return abs, nil
	}
	if err != nil {
		return "", err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("raiz de skills deve ser diretório real")
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", err
	}
	if resolved != abs {
		return "", errors.New("raiz de skills não pode conter links simbólicos")
	}
	return abs, nil
}

func readCatalogRoot(ctx context.Context, root string, requireExactName bool) ([]CatalogEntry, error) {
	dirs, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	entries := make([]CatalogEntry, 0, len(dirs))
	var errs []error
	for _, dir := range dirs {
		if err := ctx.Err(); err != nil {
			return entries, err
		}
		if !dir.IsDir() || !validCatalogSlug(dir.Name()) {
			continue
		}
		entry, err := readCatalogSkill(filepath.Join(root, dir.Name()), dir.Name(), requireExactName)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", dir.Name(), err))
			continue
		}
		entries = append(entries, entry)
	}
	return entries, errors.Join(errs...)
}

func readCatalogSkill(dir, slug string, requireExactName bool) (CatalogEntry, error) {
	info, err := os.Lstat(dir)
	if err != nil {
		return CatalogEntry{}, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return CatalogEntry{}, errors.New("diretório de skill inválido ou link simbólico")
	}
	path := filepath.Join(dir, "SKILL.md")
	info, err = os.Lstat(path)
	if err != nil {
		return CatalogEntry{}, err
	}
	if !info.Mode().IsRegular() || info.Size() > maxCatalogFile {
		return CatalogEntry{}, errors.New("SKILL.md inválido ou grande demais")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return CatalogEntry{}, err
	}
	parsed, err := ParseSkillFile(path, raw)
	if err != nil {
		return CatalogEntry{}, err
	}
	if err := parsed.Frontmatter.Validate(); err != nil {
		return CatalogEntry{}, err
	}
	if requireExactName && parsed.Frontmatter.Name != slug {
		return CatalogEntry{}, errors.New("nome do frontmatter difere do identificador")
	}
	return CatalogEntry{Slug: slug, Name: parsed.Frontmatter.Name, Description: parsed.Frontmatter.Description}, nil
}

func validCatalogSlug(slug string) bool {
	if len(slug) == 0 || len(slug) > 80 || slug[0] < 'a' || slug[0] > 'z' {
		return false
	}
	for _, r := range slug {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			continue
		}
		return false
	}
	return true
}

func catalogScore(query string, entry CatalogEntry) float64 {
	if query == "*" || strings.EqualFold(query, "all") {
		return 1
	}
	terms := strings.FieldsFunc(strings.ToLower(query), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	if len(terms) == 0 {
		return 0
	}
	slug := strings.ToLower(entry.Slug)
	name := strings.ToLower(entry.Name + " " + entry.ActiveAs)
	description := strings.ToLower(entry.Description)
	score := 0.0
	for _, term := range terms {
		switch {
		case strings.Contains(slug, term):
			score += 5
		case strings.Contains(name, term):
			score += 4
		case strings.Contains(description, term):
			score += 2
		default:
			return 0
		}
	}
	return score / float64(len(terms))
}

func copyCatalogTree(ctx context.Context, source, stage string) (string, error) {
	h := sha256.New()
	files, bytesCopied := 0, int64(0)
	err := filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		rel, err := filepath.Rel(source, path)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
			return errors.New("caminho fora da skill")
		}
		if rel == "." {
			return nil
		}
		if rel == ".skill-origin.json" {
			return errors.New("metadados de proveniência são reservados")
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("link simbólico proibido: %s", rel)
		}
		target := filepath.Join(stage, rel)
		if entry.IsDir() {
			return os.Mkdir(target, 0755)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Size() > maxCatalogFile || files >= maxCatalogFiles || bytesCopied+info.Size() > maxCatalogBytes {
			return fmt.Errorf("arquivo de skill inválido ou excede limite: %s", rel)
		}
		files++
		sourceFile, err := os.Open(path)
		if err != nil {
			return err
		}
		opened, err := sourceFile.Stat()
		if err != nil || !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
			sourceFile.Close()
			return fmt.Errorf("arquivo de origem mudou durante a cópia: %s", rel)
		}
		mode := fs.FileMode(0644)
		if info.Mode()&0111 != 0 {
			mode = 0755
		}
		targetFile, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
		if err != nil {
			sourceFile.Close()
			return err
		}
		if _, err := io.WriteString(h, filepath.ToSlash(rel)+"\x00"); err != nil {
			targetFile.Close()
			sourceFile.Close()
			return err
		}
		n, copyErr := io.Copy(io.MultiWriter(targetFile, h), io.LimitReader(sourceFile, maxCatalogFile+1))
		if _, err := io.WriteString(h, "\x00"); copyErr == nil {
			copyErr = err
		}
		bytesCopied += n
		if copyErr == nil && (n > maxCatalogFile || bytesCopied > maxCatalogBytes || n != info.Size()) {
			copyErr = fmt.Errorf("tamanho do arquivo mudou durante a cópia: %s", rel)
		}
		if syncErr := targetFile.Sync(); copyErr == nil {
			copyErr = syncErr
		}
		if closeErr := targetFile.Close(); copyErr == nil {
			copyErr = closeErr
		}
		if closeErr := sourceFile.Close(); copyErr == nil {
			copyErr = closeErr
		}
		return copyErr
	})
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
