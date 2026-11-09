package session

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

// ToolTracePhase is an observation, never an instruction to execute a tool.
type ToolTracePhase string

const (
	ToolTraceStart    ToolTracePhase = "start"
	ToolTraceComplete ToolTracePhase = "complete"
	ToolTraceError    ToolTracePhase = "error"

	toolTraceRetention  = 30 * 24 * time.Hour
	maxToolTraceEvents  = 1024
	maxToolTraceFile    = 8 << 10
	maxToolTracePreview = 512
)

// ToolTraceEvent is supplied by the tool loop. Argument values are never
// written to the transcript; only their keys and a digest are retained.
type ToolTraceEvent struct {
	TurnID    string
	CallID    string
	ToolName  string
	Phase     ToolTracePhase
	Arguments map[string]any
	Result    string
	// ResultBytes and ResultSHA256 may describe the full result when Result is
	// only the short display preview carried by a UI event.
	ResultBytes  int
	ResultSHA256 string
	Error        string
	At           time.Time
	Elapsed      time.Duration
	IsReadOnly   bool
}

// ToolTraceRecord is an immutable account of one lifecycle transition. It
// cannot be sent back to the executor as a pending tool request.
type ToolTraceRecord struct {
	Version         int            `json:"version"`
	EventID         string         `json:"event_id"`
	TurnID          string         `json:"turn_id,omitempty"`
	CallID          string         `json:"call_id,omitempty"`
	ToolName        string         `json:"tool_name"`
	Phase           ToolTracePhase `json:"phase"`
	At              time.Time      `json:"at"`
	ElapsedMillis   int64          `json:"elapsed_ms,omitempty"`
	IsReadOnly      bool           `json:"is_read_only,omitempty"`
	ArgumentKeys    []string       `json:"argument_keys,omitempty"`
	ArgumentsSHA256 string         `json:"arguments_sha256,omitempty"`
	ResultBytes     int            `json:"result_bytes,omitempty"`
	ResultSHA256    string         `json:"result_sha256,omitempty"`
	ResultPreview   string         `json:"result_preview,omitempty"`
	ErrorPreview    string         `json:"error_preview,omitempty"`
}

// ToolTranscriptStore keeps bounded, private event files under the Polypod
// data directory. The existing session JSON format remains untouched: older
// sessions simply have no tool transcript to load.
type ToolTranscriptStore struct {
	mu   sync.Mutex
	root string
}

func NewToolTranscriptStore(dataDir string) (*ToolTranscriptStore, error) {
	if strings.TrimSpace(dataDir) == "" {
		return nil, errors.New("diretorio de dados do transcript vazio")
	}
	root := filepath.Join(dataDir, "tool-transcripts.d")
	if err := privateTraceDir(root); err != nil {
		return nil, err
	}
	return &ToolTranscriptStore{root: root}, nil
}

func (s *ToolTranscriptStore) scopeDir(scope string) (string, error) {
	if s == nil || s.root == "" {
		return "", errors.New("transcript de ferramentas indisponivel")
	}
	if strings.TrimSpace(scope) == "" || len(scope) > 1024 {
		return "", errors.New("ID da sessao invalido")
	}
	digest := sha256.Sum256([]byte(scope))
	return filepath.Join(s.root, hex.EncodeToString(digest[:])), nil
}

func privateTraceDir(path string) error {
	if err := os.MkdirAll(path, 0700); err != nil {
		return fmt.Errorf("criando transcript privado: %w", err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("diretorio de transcript invalido")
	}
	if err := os.Chmod(path, 0700); err != nil {
		return fmt.Errorf("protegendo transcript: %w", err)
	}
	return nil
}

func existingTraceDir(path string) (bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0077 != 0 {
		return false, errors.New("diretorio de transcript invalido ou nao privado")
	}
	return true, nil
}

func traceDigest(content []byte) string {
	digest := sha256.Sum256(content)
	return hex.EncodeToString(digest[:])
}

func newTraceID() (string, error) {
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(random[:]), nil
}

var traceSecretPattern = regexp.MustCompile(`(?i)(?:\b(?:sk|ghp|gho|github_pat|xoxb|xoxp)[-_][a-z0-9_-]{12,}\b|\bAKIA[A-Z0-9]{16}\b|\bBearer\s+[a-z0-9._~-]{12,}\b)`)

var traceSensitiveWords = []string{
	"password", "passwd", "passphrase", "senha", "token", "secret", "segredo",
	"api_key", "api key", "authorization", "cookie", "credential", "credencial",
	"private key", "chave privada", "2fa", "otp",
}

// safeTracePreview deliberately omits a whole result when it might contain a
// credential. No raw arguments or output are needed to identify the call,
// outcome and digest after resume.
func safeTracePreview(value string) string {
	if value == "" {
		return ""
	}
	lower := strings.ToLower(value)
	if traceSecretPattern.MatchString(value) {
		return "[conteudo omitido por conter possivel credencial]"
	}
	for _, word := range traceSensitiveWords {
		if strings.Contains(lower, word) {
			return "[conteudo omitido por conter possivel credencial]"
		}
	}
	value = strings.ToValidUTF8(value, "")
	value = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' || r == '\r' {
			return ' '
		}
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, value)
	value = strings.Join(strings.Fields(value), " ")
	if len(value) <= maxToolTracePreview {
		return value
	}
	cut := maxToolTracePreview
	for cut > 0 && !utf8.RuneStart(value[cut]) {
		cut--
	}
	return value[:cut] + "..."
}

func traceRecord(event ToolTraceEvent) (ToolTraceRecord, error) {
	if len(event.ToolName) == 0 || len(event.ToolName) > 128 || strings.ContainsAny(event.ToolName, "\n\r") {
		return ToolTraceRecord{}, errors.New("nome de ferramenta invalido")
	}
	switch event.Phase {
	case ToolTraceStart, ToolTraceComplete, ToolTraceError:
	default:
		return ToolTraceRecord{}, fmt.Errorf("fase de ferramenta invalida: %q", event.Phase)
	}
	if len(event.TurnID) > 128 || len(event.CallID) > 128 {
		return ToolTraceRecord{}, errors.New("ID de turno ou chamada muito longo")
	}
	id, err := newTraceID()
	if err != nil {
		return ToolTraceRecord{}, fmt.Errorf("gerando ID de evento: %w", err)
	}
	at := event.At.UTC()
	if at.IsZero() {
		at = time.Now().UTC()
	}
	record := ToolTraceRecord{
		Version: 1, EventID: id, TurnID: event.TurnID, CallID: event.CallID,
		ToolName: event.ToolName, Phase: event.Phase, At: at,
		IsReadOnly: event.IsReadOnly,
	}
	if event.Elapsed > 0 {
		record.ElapsedMillis = event.Elapsed.Milliseconds()
	}
	if len(event.Arguments) > 0 {
		keys := make([]string, 0, len(event.Arguments))
		for key := range event.Arguments {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		record.ArgumentKeys = keys
		if encoded, err := json.Marshal(event.Arguments); err == nil {
			record.ArgumentsSHA256 = traceDigest(encoded)
		}
	}
	if event.Phase != ToolTraceStart {
		record.ResultBytes = len(event.Result)
		if event.Phase == ToolTraceError && event.Result == "" {
			record.ResultBytes = len(event.Error)
			if event.Error != "" {
				record.ResultSHA256 = traceDigest([]byte(event.Error))
			}
		}
		if event.ResultBytes > record.ResultBytes {
			record.ResultBytes = event.ResultBytes
		}
		if event.Result != "" {
			record.ResultSHA256 = traceDigest([]byte(event.Result))
			record.ResultPreview = safeTracePreview(event.Result)
		}
		if len(event.ResultSHA256) == 64 {
			if _, err := hex.DecodeString(event.ResultSHA256); err == nil {
				record.ResultSHA256 = strings.ToLower(event.ResultSHA256)
			}
		}
		record.ErrorPreview = safeTracePreview(event.Error)
	}
	return record, nil
}

// Append saves one transition before acknowledging it to the caller. Files
// are immutable, so a branch can snapshot history through hard links.
func (s *ToolTranscriptStore) Append(scope string, event ToolTraceEvent) error {
	dir, err := s.scopeDir(scope)
	if err != nil {
		return err
	}
	record, err := traceRecord(event)
	if err != nil {
		return err
	}
	data, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("codificando evento de ferramenta: %w", err)
	}
	if len(data) > maxToolTraceFile {
		return errors.New("evento de ferramenta excede limite de armazenamento")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := privateTraceDir(dir); err != nil {
		return err
	}
	file, err := os.CreateTemp(dir, ".event-*.tmp")
	if err != nil {
		return fmt.Errorf("criando evento temporario: %w", err)
	}
	defer os.Remove(file.Name())
	if err := file.Chmod(0600); err != nil {
		file.Close()
		return err
	}
	if _, err := file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	name := fmt.Sprintf("%020d-%s.json", record.At.UnixNano(), record.EventID)
	if err := os.Rename(file.Name(), filepath.Join(dir, name)); err != nil {
		return fmt.Errorf("publicando evento de ferramenta: %w", err)
	}
	if err := pruneTraceDir(dir); err != nil {
		return fmt.Errorf("evento salvo; limpeza do transcript falhou: %w", err)
	}
	if err := syncTraceDir(dir); err != nil {
		return fmt.Errorf("evento salvo; sincronizacao do diretorio falhou: %w", err)
	}
	return nil
}

type traceFile struct {
	name string
	mod  time.Time
}

func listTraceFiles(dir string) ([]traceFile, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	files := make([]traceFile, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
			continue
		}
		files = append(files, traceFile{name: entry.Name(), mod: info.ModTime()})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].name < files[j].name })
	return files, nil
}

func pruneTraceDir(dir string) error {
	files, err := listTraceFiles(dir)
	if err != nil {
		return err
	}
	kept := make([]traceFile, 0, len(files))
	for _, file := range files {
		if time.Since(file.mod) > toolTraceRetention {
			if err := os.Remove(filepath.Join(dir, file.name)); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			continue
		}
		kept = append(kept, file)
	}
	if len(kept) <= maxToolTraceEvents {
		return nil
	}
	for _, file := range kept[:len(kept)-maxToolTraceEvents] {
		if err := os.Remove(filepath.Join(dir, file.name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func syncTraceDir(dir string) error {
	file, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer file.Close()
	return file.Sync()
}

func readTraceRecord(path string) (ToolTraceRecord, error) {
	before, err := os.Lstat(path)
	if err != nil {
		return ToolTraceRecord{}, err
	}
	if !before.Mode().IsRegular() || before.Mode().Perm()&0077 != 0 || before.Size() > maxToolTraceFile {
		return ToolTraceRecord{}, errors.New("arquivo de transcript invalido")
	}
	file, err := os.Open(path)
	if err != nil {
		return ToolTraceRecord{}, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil {
		return ToolTraceRecord{}, err
	}
	if !os.SameFile(before, opened) {
		return ToolTraceRecord{}, errors.New("arquivo de transcript trocado")
	}
	data, err := io.ReadAll(io.LimitReader(file, maxToolTraceFile+1))
	if err != nil {
		return ToolTraceRecord{}, err
	}
	if len(data) > maxToolTraceFile {
		return ToolTraceRecord{}, errors.New("transcript excede limite")
	}
	var record ToolTraceRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return ToolTraceRecord{}, err
	}
	if record.Version != 1 || record.EventID == "" || record.ToolName == "" {
		return ToolTraceRecord{}, errors.New("evento de transcript invalido")
	}
	return record, nil
}

// Load returns recent records in their original order. A missing journal is
// the migration path for sessions created before tool transcripts existed.
func (s *ToolTranscriptStore) Load(scope string, limit int) ([]ToolTraceRecord, error) {
	dir, err := s.scopeDir(scope)
	if err != nil {
		return nil, err
	}
	if limit <= 0 || limit > maxToolTraceEvents {
		limit = maxToolTraceEvents
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	exists, err := existingTraceDir(dir)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, nil
	}
	if err := pruneTraceDir(dir); err != nil {
		return nil, fmt.Errorf("limpando transcript antigo: %w", err)
	}
	files, err := listTraceFiles(dir)
	if err != nil {
		return nil, err
	}
	if len(files) > limit {
		files = files[len(files)-limit:]
	}
	result := make([]ToolTraceRecord, 0, len(files))
	for _, file := range files {
		if time.Since(file.mod) > toolTraceRetention {
			continue
		}
		record, err := readTraceRecord(filepath.Join(dir, file.name))
		if err != nil {
			return nil, fmt.Errorf("lendo evento %s: %w", file.name, err)
		}
		result = append(result, record)
	}
	return result, nil
}

// Summary is a small, read-only account of completed actions. It never
// contains executable tool calls, raw arguments, results, or error text.
func (s *ToolTranscriptStore) Summary(scope string, maxChars int) (string, error) {
	if maxChars <= 0 || maxChars > 1000 {
		maxChars = 1000
	}
	const heading = "Histórico de ferramentas (confira o estado antes de repetir ações):\n"
	if maxChars <= len(heading) {
		return "", nil
	}
	records, err := s.Load(scope, 64)
	if err != nil {
		return "", err
	}
	var lines []string
	length := 0
	for i := len(records) - 1; i >= 0 && len(lines) < 8; i-- {
		record := records[i]
		if record.Phase != ToolTraceComplete && record.Phase != ToolTraceError {
			continue
		}
		status := "concluiu"
		if record.Phase == ToolTraceError {
			status = "falhou"
		}
		name := strings.Map(func(r rune) rune {
			if unicode.IsLetter(r) || unicode.IsNumber(r) || r == '_' || r == '-' || r == '.' || r == ':' {
				return r
			}
			return -1
		}, record.ToolName)
		if name == "" {
			continue
		}
		line := fmt.Sprintf("- %s %s (%d bytes", name, status, record.ResultBytes)
		if len(record.ResultSHA256) >= 12 {
			line += ", sha256:" + record.ResultSHA256[:12]
		}
		line += ")\n"
		if length+len(line) > maxChars-len(heading) {
			continue
		}
		lines = append(lines, line)
		length += len(line)
	}
	if len(lines) == 0 {
		return "", nil
	}
	var output strings.Builder
	output.WriteString(heading)
	for i := len(lines) - 1; i >= 0; i-- {
		output.WriteString(lines[i])
	}
	return output.String(), nil
}

// Fork snapshots immutable records at branch creation. New events in either
// branch go to different directories after the fork.
func (s *ToolTranscriptStore) Fork(parent, child string) error {
	if parent == child {
		return nil
	}
	parentDir, err := s.scopeDir(parent)
	if err != nil {
		return err
	}
	childDir, err := s.scopeDir(child)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	exists, err := existingTraceDir(parentDir)
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	files, err := listTraceFiles(parentDir)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return nil
	}
	if err := privateTraceDir(childDir); err != nil {
		return err
	}
	childFiles, err := listTraceFiles(childDir)
	if err != nil {
		return err
	}
	if len(childFiles) != 0 {
		return errors.New("transcript da branch filha ja existe")
	}
	for _, file := range files {
		if time.Since(file.mod) > toolTraceRetention {
			continue
		}
		from := filepath.Join(parentDir, file.name)
		to := filepath.Join(childDir, file.name)
		if err := os.Link(from, to); err != nil {
			if err := copyTraceFile(from, to); err != nil {
				return fmt.Errorf("copiando evento da branch: %w", err)
			}
		}
	}
	if err := pruneTraceDir(childDir); err != nil {
		return err
	}
	return syncTraceDir(childDir)
}

func copyTraceFile(from, to string) error {
	data, err := os.ReadFile(from)
	if err != nil {
		return err
	}
	if len(data) > maxToolTraceFile {
		return errors.New("evento da branch excede limite")
	}
	file, err := os.OpenFile(to, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		file.Close()
		os.Remove(to)
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		os.Remove(to)
		return err
	}
	if err := file.Close(); err != nil {
		os.Remove(to)
		return err
	}
	return nil
}

// Delete removes only this hashed session directory. It is intended for a
// failed branch creation, before the new branch becomes visible.
func (s *ToolTranscriptStore) Delete(scope string) error {
	dir, err := s.scopeDir(scope)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return os.RemoveAll(dir)
}
