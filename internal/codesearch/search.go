package codesearch

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/costa/polypod/internal/memory"
	"gopkg.in/yaml.v3"
)

type Config struct {
	Enabled    bool     `yaml:"enabled"`
	MaxFiles   int      `yaml:"max_files"`
	MaxChunks  int      `yaml:"max_chunks"`
	Extensions []string `yaml:"extensions"`
}

func configPath(root string) string { return filepath.Join(root, ".polypod", "code-index.yaml") }

func LoadConfig(root string) (Config, error) {
	data, err := os.ReadFile(configPath(root))
	if os.IsNotExist(err) {
		return Config{}, nil
	}
	if err != nil {
		return Config{}, err
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return Config{}, err
	}
	if cfg.Enabled {
		path, err := consentPath(root)
		if err != nil {
			return Config{}, err
		}
		consent, err := os.ReadFile(path)
		hash := sha256.Sum256(data)
		if err != nil || strings.TrimSpace(string(consent)) != hex.EncodeToString(hash[:]) {
			cfg.Enabled = false
		}
	}
	if cfg.MaxFiles <= 0 || cfg.MaxFiles > 3000 {
		cfg.MaxFiles = 500
	}
	if cfg.MaxChunks <= 0 || cfg.MaxChunks > 6000 {
		cfg.MaxChunks = 1000
	}
	return cfg, nil
}

func SetEnabled(root string, enabled bool) error {
	cfg, err := LoadConfig(root)
	if err != nil {
		return err
	}
	cfg.Enabled = enabled
	if cfg.MaxFiles == 0 {
		cfg.MaxFiles = 500
	}
	if cfg.MaxChunks == 0 {
		cfg.MaxChunks = 1000
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	path, err := consentPath(root)
	if err != nil {
		return err
	}
	if !enabled {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	if err := writePrivateAtomic(configPath(root), data); err != nil {
		return err
	}
	if enabled {
		hash := sha256.Sum256(data)
		return writePrivateAtomic(path, []byte(hex.EncodeToString(hash[:])))
	}
	return nil
}

func consentPath(root string) (string, error) {
	project, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(project); err == nil {
		project = resolved
	}
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256([]byte(project))
	return filepath.Join(configDir, "polypod", "code-index-consent", hex.EncodeToString(hash[:])), nil
}

func writePrivateAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".polypod-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

type Match struct {
	Path      string
	StartLine int
	EndLine   int
	Score     float64
	Snippet   string
}

type chunk struct {
	match Match
	text  string
	hash  [32]byte
}

// Engine caches vectors by content, so repeated searches only embed changed snippets.
type Engine struct {
	mu       sync.Mutex
	embedder memory.Embedder
	vectors  map[[32]byte][]float64
	inFlight map[[32]byte]*embeddingCall
}

type embeddingCall struct {
	done   chan struct{}
	vector []float64
	err    error
}

type pendingEmbedding struct {
	item chunk
	call *embeddingCall
}

func New(embedder memory.Embedder) *Engine {
	return &Engine{embedder: embedder, vectors: make(map[[32]byte][]float64), inFlight: make(map[[32]byte]*embeddingCall)}
}

func (e *Engine) Search(ctx context.Context, root, query string, limit int) ([]Match, error) {
	if e == nil || e.embedder == nil {
		return nil, fmt.Errorf("configure embeddings antes de buscar código por significado")
	}
	cfg, err := LoadConfig(root)
	if err != nil {
		return nil, err
	}
	if !cfg.Enabled {
		return nil, fmt.Errorf("índice semântico desativado; use /index on")
	}
	if strings.TrimSpace(query) == "" {
		return nil, fmt.Errorf("informe uma busca")
	}
	if limit <= 0 || limit > 20 {
		limit = 8
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	chunks, err := collectChunks(ctx, root, cfg)
	if err != nil {
		return nil, err
	}
	if len(chunks) == 0 {
		return nil, nil
	}
	vectors, err := e.ensureVectors(ctx, chunks)
	if err != nil {
		return nil, err
	}
	queryVectors, err := e.embedder.Embed(ctx, []string{query})
	if err != nil {
		return nil, fmt.Errorf("vetor da busca indisponível: %w", err)
	}
	if len(queryVectors) != 1 {
		return nil, fmt.Errorf("provedor retornou %d vetores para a busca", len(queryVectors))
	}
	for i := range chunks {
		chunks[i].match.Score = cosine(queryVectors[0], vectors[chunks[i].hash])
	}
	sort.SliceStable(chunks, func(i, j int) bool { return chunks[i].match.Score > chunks[j].match.Score })
	matches := make([]Match, 0, limit)
	for _, item := range chunks {
		if len(matches) == limit {
			break
		}
		if item.match.Score > 0 {
			matches = append(matches, item.match)
		}
	}
	e.mu.Lock()
	if len(e.vectors) > 12000 {
		clear(e.vectors)
	}
	e.mu.Unlock()
	return matches, nil
}

// ensureVectors shares work for identical snippets without holding the cache
// mutex across embedding calls. Results are pinned locally for this search so
// cache eviction by another search cannot change its scores.
func (e *Engine) ensureVectors(ctx context.Context, chunks []chunk) (map[[32]byte][]float64, error) {
	result := make(map[[32]byte][]float64, len(chunks))
	work := append([]chunk(nil), chunks...)
	retries := make(map[[32]byte]bool)
	for start := 0; start < len(work); {
		var owned []chunk
		var waiting []pendingEmbedding
		seenWaiting := make(map[[32]byte]bool)
		e.mu.Lock()
		for processed := 0; start < len(work) && processed < 32; processed++ {
			item := work[start]
			start++
			if _, found := result[item.hash]; found {
				continue
			}
			if vector, found := e.vectors[item.hash]; found {
				result[item.hash] = vector
				continue
			}
			if call, found := e.inFlight[item.hash]; found {
				if !seenWaiting[item.hash] {
					waiting = append(waiting, pendingEmbedding{item: item, call: call})
					seenWaiting[item.hash] = true
				}
				continue
			}
			e.inFlight[item.hash] = &embeddingCall{done: make(chan struct{})}
			owned = append(owned, item)
		}
		e.mu.Unlock()

		if len(owned) > 0 {
			texts := make([]string, len(owned))
			for i, item := range owned {
				texts[i] = item.text
			}
			embedded, embedErr := e.embedder.Embed(ctx, texts)
			if embedErr == nil && len(embedded) != len(owned) {
				embedErr = fmt.Errorf("provedor retornou %d vetores para %d trechos", len(embedded), len(owned))
			}
			e.mu.Lock()
			for i, item := range owned {
				call := e.inFlight[item.hash]
				if embedErr == nil {
					call.vector = embedded[i]
					e.vectors[item.hash] = embedded[i]
					result[item.hash] = embedded[i]
				} else {
					call.err = embedErr
				}
				close(call.done)
				delete(e.inFlight, item.hash)
			}
			e.mu.Unlock()
			if embedErr != nil {
				return nil, embedErr
			}
		}
		for _, pending := range waiting {
			select {
			case <-pending.call.done:
				if pending.call.err != nil {
					if ctx.Err() == nil && !retries[pending.item.hash] &&
						(errors.Is(pending.call.err, context.Canceled) || errors.Is(pending.call.err, context.DeadlineExceeded)) {
						retries[pending.item.hash] = true
						work = append(work, pending.item)
						continue
					}
					return nil, pending.call.err
				}
				result[pending.item.hash] = pending.call.vector
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
	}
	return result, nil
}

func cosine(a, b []float64) float64 {
	if len(a) == 0 || len(a) != len(b) {
		return 0
	}
	var dot, aa, bb float64
	for i := range a {
		if math.IsNaN(a[i]) || math.IsNaN(b[i]) || math.IsInf(a[i], 0) || math.IsInf(b[i], 0) {
			return 0
		}
		dot += a[i] * b[i]
		aa += a[i] * a[i]
		bb += b[i] * b[i]
	}
	if aa == 0 || bb == 0 {
		return 0
	}
	return dot / math.Sqrt(aa*bb)
}

func collectChunks(ctx context.Context, root string, cfg Config) ([]chunk, error) {
	extensions := map[string]bool{}
	for _, ext := range cfg.Extensions {
		extensions[ext] = true
	}
	if len(extensions) == 0 {
		for _, ext := range []string{".go", ".py", ".js", ".jsx", ".ts", ".tsx", ".rs", ".java", ".kt", ".c", ".h", ".cpp", ".cs", ".rb", ".php", ".sh", ".sql"} {
			extensions[ext] = true
		}
	}
	// Let Git discard other file types before returning paths. This matters in
	// monorepos with many generated or non-code files.
	patterns := make([]string, 0, len(extensions))
	for ext := range extensions {
		if ext == "" || !strings.HasPrefix(ext, ".") || strings.ContainsAny(ext, "/\\*?[]") {
			continue
		}
		patterns = append(patterns, "*"+ext)
	}
	sort.Strings(patterns)
	if len(patterns) == 0 {
		return nil, nil
	}
	args := append([]string{"ls-files", "-co", "--exclude-standard", "-z", "--"}, patterns...)
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = root
	paths, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("busca semântica exige um projeto Git: %w", err)
	}
	var chunks []chunk
	files := 0
	for _, raw := range strings.Split(string(paths), "\x00") {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if raw == "" || !extensions[filepath.Ext(raw)] || strings.Contains(raw, "..") || filepath.IsAbs(raw) {
			continue
		}
		if files >= cfg.MaxFiles || len(chunks) >= cfg.MaxChunks {
			break
		}
		path := filepath.Join(root, raw)
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || info.Size() > 128*1024 {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil || !utf8.Valid(data) || strings.ContainsRune(string(data), '\x00') {
			continue
		}
		files++
		lines := strings.Split(string(data), "\n")
		for start := 0; start < len(lines) && len(chunks) < cfg.MaxChunks; start += 65 {
			end := min(start+80, len(lines))
			body := strings.Join(lines[start:end], "\n")
			if strings.TrimSpace(body) == "" {
				continue
			}
			if len(body) > 8000 {
				body = body[:8000]
			}
			text := raw + "\n" + body
			chunks = append(chunks, chunk{match: Match{Path: raw, StartLine: start + 1, EndLine: end, Snippet: body}, text: text, hash: sha256.Sum256([]byte(text))})
		}
	}
	return chunks, nil
}

func Format(matches []Match) string {
	if len(matches) == 0 {
		return "Nenhum trecho de código encontrado."
	}
	var out strings.Builder
	for _, match := range matches {
		snippet := []rune(match.Snippet)
		if len(snippet) > 700 {
			snippet = append(snippet[:700], []rune("\n… [trecho abreviado; abra o arquivo para ver mais]")...)
		}
		fmt.Fprintf(&out, "%s:%d-%d (similaridade %.2f)\n%s\n\n", match.Path, match.StartLine, match.EndLine, match.Score, string(snippet))
	}
	return strings.TrimSpace(out.String())
}
