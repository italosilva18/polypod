package memory

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	localEmbeddingModel      = "sentence-transformers/paraphrase-multilingual-MiniLM-L12-v2"
	localEmbeddingRepo       = "models--Qdrant--paraphrase-multilingual-MiniLM-L12-v2-onnx-Q"
	localEmbeddingModelFile  = "model_optimized.onnx"
	localEmbeddingDimensions = 384
	localEmbeddingBatchSize  = 32
	localEmbeddingMaxOutput  = 4 << 20
	localEmbeddingTimeout    = 90 * time.Second
)

//go:embed local_embedding_worker.py
var localEmbeddingWorker string

// LocalEmbeddingConfig selects a private, preinstalled FastEmbed environment.
// The model must already exist in CacheDir; Embed never downloads it.
type LocalEmbeddingConfig struct {
	PythonBinary string
	CacheDir     string
	Timeout      time.Duration
}

// LocalEmbedder keeps one CPU worker loaded between calls. All communication
// stays on private pipes. Worker diagnostics never include memory contents.
type LocalEmbedder struct {
	python  string
	cache   string
	timeout time.Duration
	name    string
	worker  string

	mu     sync.Mutex
	closed bool
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Reader
}

var _ Embedder = (*LocalEmbedder)(nil)
var _ io.Closer = (*LocalEmbedder)(nil)

// NewLocalEmbedder validates the private runtime and fingerprints the active
// snapshot. The fingerprint keeps document vectors separated across upgrades.
func NewLocalEmbedder(cfg LocalEmbeddingConfig) (*LocalEmbedder, error) {
	python, err := expandLocalEmbeddingHome(strings.TrimSpace(cfg.PythonBinary))
	if err != nil {
		return nil, err
	}
	if python == "" {
		return nil, errors.New("Python de embeddings locais obrigatório")
	}
	resolved, err := exec.LookPath(python)
	if err != nil {
		return nil, errors.New("Python de embeddings locais indisponível")
	}
	python, err = filepath.Abs(resolved)
	if err != nil {
		return nil, errors.New("caminho do Python de embeddings inválido")
	}
	cache, err := expandLocalEmbeddingHome(strings.TrimSpace(cfg.CacheDir))
	if err != nil {
		return nil, err
	}
	if cache == "" {
		return nil, errors.New("cache de embeddings locais obrigatório")
	}
	cache, err = filepath.Abs(cache)
	if err != nil {
		return nil, errors.New("caminho do cache de embeddings inválido")
	}
	info, err := os.Stat(cache)
	if err != nil || !info.IsDir() {
		return nil, errors.New("cache de embeddings locais indisponível")
	}
	modelFingerprint, err := fingerprintLocalEmbeddingModel(cache)
	if err != nil {
		return nil, err
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = localEmbeddingTimeout
	}
	return &LocalEmbedder{
		python: python, cache: cache, timeout: timeout,
		name:   "fastembed-multilingual-minilm-v0.9.1-" + modelFingerprint,
		worker: localEmbeddingWorker,
	}, nil
}

func expandLocalEmbeddingHome(path string) (string, error) {
	if path == "~" || strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", errors.New("diretório pessoal indisponível")
		}
		if path == "~" {
			return home, nil
		}
		return filepath.Join(home, path[2:]), nil
	}
	return path, nil
}

// fingerprintLocalEmbeddingModel follows the same refs/main snapshot chosen
// by Hugging Face's offline snapshot_download for FastEmbed's canonical repo.
// Old snapshots may coexist in the cache and must not affect the identity.
func fingerprintLocalEmbeddingModel(cache string) (string, error) {
	const maxRefBytes = 128
	files := [...]string{
		localEmbeddingModelFile,
		"tokenizer.json",
		"config.json",
		"tokenizer_config.json",
		"special_tokens_map.json",
	}
	canonicalCache, err := filepath.EvalSymlinks(cache)
	if err != nil {
		return "", errors.New("cache de embeddings locais indisponível")
	}
	repo := filepath.Join(canonicalCache, localEmbeddingRepo)
	ref, err := os.Open(filepath.Join(repo, "refs", "main"))
	if err != nil {
		return "", errors.New("revisão ativa do modelo local indisponível")
	}
	defer ref.Close()
	refData, err := io.ReadAll(io.LimitReader(ref, maxRefBytes+1))
	if err != nil || len(refData) > maxRefBytes {
		return "", errors.New("revisão ativa do modelo local inválida")
	}
	revision := strings.TrimSpace(string(refData))
	if len(revision) != 40 {
		return "", errors.New("revisão ativa do modelo local inválida")
	}
	for _, char := range revision {
		if char < '0' || char > '9' && char < 'a' || char > 'f' {
			return "", errors.New("revisão ativa do modelo local inválida")
		}
	}
	snapshot := filepath.Join(repo, "snapshots", revision)
	info, err := os.Stat(snapshot)
	if err != nil || !info.IsDir() {
		return "", errors.New("snapshot ativo do modelo local indisponível")
	}
	hash := sha256.New()
	_, _ = hash.Write([]byte(localEmbeddingModel + "\x00fastembed-0.9.1\x00" + revision + "\x00"))
	for _, name := range files {
		path := filepath.Join(snapshot, name)
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil {
			return "", errors.New("artefato do modelo local indisponível")
		}
		rel, err := filepath.Rel(canonicalCache, resolved)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
			return "", errors.New("artefato do modelo local fora do cache")
		}
		file, err := os.Open(path)
		if err != nil {
			return "", errors.New("artefato do modelo local indisponível")
		}
		info, err := file.Stat()
		if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > 1<<30 {
			file.Close()
			return "", errors.New("artefato do modelo local inválido")
		}
		_, _ = hash.Write([]byte(name + "\x00"))
		if _, err := io.CopyN(hash, file, info.Size()); err != nil {
			file.Close()
			return "", errors.New("falha ao identificar o modelo local")
		}
		if err := file.Close(); err != nil {
			return "", errors.New("falha ao identificar o modelo local")
		}
	}
	return hex.EncodeToString(hash.Sum(nil)[:12]), nil
}

// Name is stable for these weights and worker version; persisted vectors use it.
func (e *LocalEmbedder) Name() string { return e.name }

// Embed returns vectors in input order. A worker error returns no partial
// vectors, so callers can retain their existing lexical fallback.
func (e *LocalEmbedder) Embed(ctx context.Context, texts []string) ([][]float64, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	if len(texts) > 10000 {
		return nil, errors.New("muitos textos para embeddings")
	}
	for _, value := range texts {
		if strings.TrimSpace(value) == "" || len(value) > 128_000 {
			return nil, errors.New("texto de embedding vazio ou muito longo")
		}
	}
	ctx, cancel := context.WithTimeout(ctx, e.timeout)
	defer cancel()
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return nil, errors.New("embedder local fechado")
	}
	if err := e.startLocked(ctx); err != nil {
		return nil, err
	}
	vectors := make([][]float64, 0, len(texts))
	for start := 0; start < len(texts); start += localEmbeddingBatchSize {
		end := min(start+localEmbeddingBatchSize, len(texts))
		batch, err := e.embedBatchLocked(ctx, texts[start:end])
		if err != nil {
			e.stopLocked()
			return nil, err
		}
		vectors = append(vectors, batch...)
	}
	return vectors, nil
}

func (e *LocalEmbedder) startLocked(ctx context.Context) error {
	if e.cmd != nil {
		return nil
	}
	cmd := exec.Command(e.python, "-u", "-c", e.worker, e.cache)
	cmd.Dir = e.cache
	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + os.Getenv("HOME"),
		"LANG=" + os.Getenv("LANG"),
		"TMPDIR=" + os.TempDir(),
		"HF_HUB_OFFLINE=1",
		"HF_HUB_DISABLE_TELEMETRY=1",
		"HF_HUB_DISABLE_PROGRESS_BARS=1",
		"PYTHONNOUSERSITE=1",
		"OMP_NUM_THREADS=2",
	}
	cmd.Stderr = io.Discard
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return errors.New("falha ao abrir entrada do embedder local")
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		stdin.Close()
		return errors.New("falha ao abrir saída do embedder local")
	}
	if err := cmd.Start(); err != nil {
		stdin.Close()
		return errors.New("falha ao iniciar embedder local")
	}
	e.cmd = cmd
	e.stdin = stdin
	e.stdout = bufio.NewReaderSize(stdout, 64<<10)
	line, err := e.readLineLocked(ctx)
	if err != nil {
		e.stopLocked()
		return err
	}
	var ready struct {
		Ready bool   `json:"ready"`
		Error string `json:"error"`
	}
	if json.Unmarshal(line, &ready) != nil || !ready.Ready {
		e.stopLocked()
		return errors.New("modelo local de embeddings indisponível")
	}
	return nil
}

func (e *LocalEmbedder) embedBatchLocked(ctx context.Context, texts []string) ([][]float64, error) {
	request, err := json.Marshal(struct {
		Texts []string `json:"texts"`
	}{texts})
	if err != nil {
		return nil, errors.New("falha ao codificar embeddings locais")
	}
	request = append(request, '\n')
	type response struct {
		line []byte
		err  error
	}
	done := make(chan response, 1)
	go func() {
		if _, writeErr := e.stdin.Write(request); writeErr != nil {
			done <- response{err: writeErr}
			return
		}
		line, readErr := readLocalEmbeddingLine(e.stdout)
		done <- response{line: line, err: readErr}
	}()
	var result response
	select {
	case result = <-done:
	case <-ctx.Done():
		e.stopLocked()
		<-done
		return nil, ctx.Err()
	}
	if result.err != nil {
		return nil, errors.New("falha de comunicação com embedder local")
	}
	var decoded struct {
		Vectors [][]float64 `json:"vectors"`
		Error   string      `json:"error"`
	}
	if json.Unmarshal(result.line, &decoded) != nil || decoded.Error != "" || len(decoded.Vectors) != len(texts) {
		return nil, errors.New("resposta inválida do embedder local")
	}
	for _, vector := range decoded.Vectors {
		if len(vector) != localEmbeddingDimensions || !validLocalEmbeddingVector(vector) {
			return nil, errors.New("vetor inválido do embedder local")
		}
	}
	return decoded.Vectors, nil
}

func validLocalEmbeddingVector(vector []float64) bool {
	var norm float64
	for _, value := range vector {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return false
		}
		norm += value * value
	}
	return norm > 0 && !math.IsInf(norm, 0)
}

func (e *LocalEmbedder) readLineLocked(ctx context.Context) ([]byte, error) {
	type response struct {
		line []byte
		err  error
	}
	done := make(chan response, 1)
	go func() {
		line, err := readLocalEmbeddingLine(e.stdout)
		done <- response{line: line, err: err}
	}()
	select {
	case result := <-done:
		if result.err != nil {
			return nil, errors.New("falha ao carregar embedder local")
		}
		return result.line, nil
	case <-ctx.Done():
		e.stopLocked()
		<-done
		return nil, ctx.Err()
	}
}

func readLocalEmbeddingLine(reader *bufio.Reader) ([]byte, error) {
	var out bytes.Buffer
	for out.Len() <= localEmbeddingMaxOutput {
		fragment, err := reader.ReadSlice('\n')
		if out.Len()+len(fragment) > localEmbeddingMaxOutput {
			return nil, errors.New("resposta de embeddings locais muito grande")
		}
		out.Write(fragment)
		if err == nil {
			return bytes.TrimSuffix(out.Bytes(), []byte{'\n'}), nil
		}
		if !errors.Is(err, bufio.ErrBufferFull) {
			return nil, err
		}
	}
	return nil, errors.New("resposta de embeddings locais muito grande")
}

func (e *LocalEmbedder) stopLocked() {
	if e.cmd == nil {
		return
	}
	e.stdin.Close()
	_ = e.cmd.Process.Kill()
	_ = e.cmd.Wait()
	e.cmd = nil
	e.stdin = nil
	e.stdout = nil
}

// Close stops the worker. The embedder cannot be used afterward.
func (e *LocalEmbedder) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.closed = true
	e.stopLocked()
	return nil
}
