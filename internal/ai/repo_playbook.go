package ai

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/costa/polypod/internal/workdir"
)

const (
	maxPlaybookLessonsPerRepo = 40
)

// PlaybookLesson captures one verified error -> root-cause -> fix engineering
// episode learned in a specific repository.
type PlaybookLesson struct {
	ID            string    `json:"id"`
	RepoRoot      string    `json:"repo_root"`
	TaskHint      string    `json:"task_hint"`
	FailureSignal string    `json:"failure_signal"`
	Resolution    string    `json:"resolution"`
	Files         []string  `json:"files,omitempty"`
	Hits          int       `json:"hits"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// RepoPlaybookStore persists secret-scrubbed, verified engineering lessons per
// repository so future turns avoid repeating past compiler/test pitfalls.
type RepoPlaybookStore struct {
	mu      sync.Mutex
	baseDir string
}

// NewRepoPlaybookStore creates a store under <dataDir>/repo-playbooks.
func NewRepoPlaybookStore(dataDir string) *RepoPlaybookStore {
	if strings.TrimSpace(dataDir) == "" {
		return nil
	}
	return &RepoPlaybookStore{
		baseDir: filepath.Join(dataDir, "repo-playbooks"),
	}
}

var playbookSecretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(bearer\s+)[A-Za-z0-9._\-+/=]{12,}`),
	regexp.MustCompile(`\bsk-[A-Za-z0-9_\-]{16,}`),
	regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{16,}`),
	regexp.MustCompile(`\bAIza[0-9A-Za-z_\-]{20,}`),
	regexp.MustCompile(`(?i)(api[_-]?key|token|secret|password|authorization)\s*[:=]\s*["']?[^\s"',;]{8,}`),
	regexp.MustCompile(`eyJ[A-Za-z0-9_\-]{10,}\.[A-Za-z0-9_\-]{10,}\.[A-Za-z0-9_\-]{10,}`),
}

// ScrubPlaybookSecrets redacts API keys, tokens, JWTs, and credentials before persistence.
func ScrubPlaybookSecrets(s string) string {
	out := s
	for _, re := range playbookSecretPatterns {
		out = re.ReplaceAllString(out, "[REDACTED_SECRET]")
	}
	return out
}

func normalizeRepoRoot(dir string) string {
	if strings.TrimSpace(dir) == "" {
		dir = "."
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return filepath.Clean(dir)
	}
	cur := abs
	for {
		if _, err := os.Stat(filepath.Join(cur, ".git")); err == nil {
			return cur
		}
		if _, err := os.Stat(filepath.Join(cur, "go.mod")); err == nil {
			return cur
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return abs
		}
		cur = parent
	}
}

func (s *RepoPlaybookStore) filePathForRepo(repoRoot string) string {
	norm := normalizeRepoRoot(repoRoot)
	sum := sha256.Sum256([]byte(norm))
	return filepath.Join(s.baseDir, hex.EncodeToString(sum[:8])+".json")
}

// RecordVerifiedRecovery saves or reinforces an engineering lesson that
// encountered a build/test/tool error and subsequently passed verification.
func (s *RepoPlaybookStore) RecordVerifiedRecovery(repoRoot, query, failureSignal, resolution string, files []string) error {
	if s == nil || s.baseDir == "" {
		return nil
	}
	failureSignal = strings.TrimSpace(ScrubPlaybookSecrets(failureSignal))
	resolution = strings.TrimSpace(ScrubPlaybookSecrets(resolution))
	query = strings.TrimSpace(ScrubPlaybookSecrets(query))
	if failureSignal == "" || resolution == "" {
		return nil
	}
	if len(query) > 180 {
		query = query[:180] + "..."
	}
	if len(failureSignal) > 280 {
		failureSignal = failureSignal[:280] + "..."
	}
	if len(resolution) > 320 {
		resolution = resolution[:320] + "..."
	}

	normRoot := normalizeRepoRoot(repoRoot)
	relFiles := make([]string, 0, len(files))
	for _, f := range files {
		if rel, err := filepath.Rel(normRoot, f); err == nil && !strings.HasPrefix(rel, "..") {
			relFiles = append(relFiles, filepath.ToSlash(rel))
		} else {
			relFiles = append(relFiles, filepath.Base(f))
		}
	}
	sort.Strings(relFiles)
	if len(relFiles) > 6 {
		relFiles = relFiles[:6]
	}

	idSum := sha256.Sum256([]byte(normRoot + "\n" + failureSignal))
	lessonID := hex.EncodeToString(idSum[:8])

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := os.MkdirAll(s.baseDir, 0700); err != nil {
		return err
	}
	_ = os.Chmod(s.baseDir, 0700)

	storePath := s.filePathForRepo(normRoot)
	lessons := s.loadUnlocked(storePath)

	updated := false
	now := time.Now().UTC()
	for i := range lessons {
		if lessons[i].ID == lessonID {
			lessons[i].Resolution = resolution
			lessons[i].TaskHint = query
			lessons[i].Files = relFiles
			lessons[i].Hits++
			lessons[i].UpdatedAt = now
			updated = true
			break
		}
	}
	if !updated {
		lessons = append(lessons, PlaybookLesson{
			ID:            lessonID,
			RepoRoot:      normRoot,
			TaskHint:      query,
			FailureSignal: failureSignal,
			Resolution:    resolution,
			Files:         relFiles,
			Hits:          1,
			UpdatedAt:     now,
		})
	}

	sort.Slice(lessons, func(i, j int) bool {
		if lessons[i].Hits != lessons[j].Hits {
			return lessons[i].Hits > lessons[j].Hits
		}
		return lessons[i].UpdatedAt.After(lessons[j].UpdatedAt)
	})
	if len(lessons) > maxPlaybookLessonsPerRepo {
		lessons = lessons[:maxPlaybookLessonsPerRepo]
	}

	raw, err := json.MarshalIndent(lessons, "", "  ")
	if err != nil {
		return err
	}
	tmpPath := storePath + ".tmp"
	if err := os.WriteFile(tmpPath, raw, 0600); err != nil {
		return err
	}
	return os.Rename(tmpPath, storePath)
}

// RelevantLessons returns a formatted prompt block with up to maxItems verified
// lessons for repoRoot ranked by lexical relevance to query and recency.
func (s *RepoPlaybookStore) RelevantLessons(repoRoot, query string, maxItems int) string {
	if s == nil || s.baseDir == "" {
		return ""
	}
	if maxItems <= 0 {
		maxItems = 4
	}
	normRoot := normalizeRepoRoot(repoRoot)
	storePath := s.filePathForRepo(normRoot)

	s.mu.Lock()
	lessons := s.loadUnlocked(storePath)
	s.mu.Unlock()
	if len(lessons) == 0 {
		return ""
	}

	tokens := playbookTokens(query)
	type scored struct {
		lesson PlaybookLesson
		score  int
	}
	ranked := make([]scored, 0, len(lessons))
	for _, l := range lessons {
		haystack := strings.ToLower(l.TaskHint + " " + l.FailureSignal + " " + l.Resolution + " " + strings.Join(l.Files, " "))
		sc := min(l.Hits, 5)
		for _, tok := range tokens {
			if strings.Contains(haystack, tok) {
				sc += 4
			}
		}
		ranked = append(ranked, scored{lesson: l, score: sc})
	}
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].score != ranked[j].score {
			return ranked[i].score > ranked[j].score
		}
		return ranked[i].lesson.UpdatedAt.After(ranked[j].lesson.UpdatedAt)
	})
	if len(ranked) > maxItems {
		ranked = ranked[:maxItems]
	}

	var b strings.Builder
	b.WriteString("PLAYBOOK DE ENGENHARIA VERIFICADO DESTE REPOSITÓRIO (lições reais de erros resolvidos anteriormente — evite repetir estas armadilhas):\n")
	for _, item := range ranked {
		l := item.lesson
		filesTag := ""
		if len(l.Files) > 0 {
			filesTag = " [" + strings.Join(l.Files, ", ") + "]"
		}
		fmt.Fprintf(&b, "- Armadilha%s: %s\n  Solução verificada: %s\n", filesTag, l.FailureSignal, l.Resolution)
	}
	return strings.TrimSpace(b.String())
}

// Stats returns total repo playbooks and total verified lessons persisted.
func (s *RepoPlaybookStore) Stats() (repos int, totalLessons int) {
	if s == nil || s.baseDir == "" {
		return 0, 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, err := os.ReadDir(s.baseDir)
	if err != nil {
		return 0, 0
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		lessons := s.loadUnlocked(filepath.Join(s.baseDir, e.Name()))
		if len(lessons) > 0 {
			repos++
			totalLessons += len(lessons)
		}
	}
	return repos, totalLessons
}

func (s *RepoPlaybookStore) loadUnlocked(path string) []PlaybookLesson {
	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 {
		return nil
	}
	var out []PlaybookLesson
	if err := json.Unmarshal(data, &out); err != nil {
		return nil
	}
	return out
}

func playbookTokens(q string) []string {
	fields := strings.FieldsFunc(strings.ToLower(q), func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' || r == '.' || r == '/')
	})
	seen := make(map[string]bool)
	var out []string
	for _, f := range fields {
		if len(f) >= 3 && !seen[f] {
			seen[f] = true
			out = append(out, f)
		}
	}
	return out
}

func (c *Client) recordTurnPlaybook(ctx context.Context, g *cognitiveGuard, finalAnswer string) {
	if c == nil || c.repoPlaybook == nil || g == nil || !g.shouldRecordPlaybook() {
		return
	}
	wd := workdir.Dir(ctx, ".")
	query := requestQueryHintFromContext(ctx)
	resolution := strings.TrimSpace(finalAnswer)
	if resolution == "" {
		resolution = g.lastCleanSummary
	}
	if firstLineIdx := strings.IndexByte(resolution, '\n'); firstLineIdx > 0 && firstLineIdx < 240 {
		// Keep concise first paragraph/sentence.
		lines := strings.SplitN(resolution, "\n\n", 2)
		resolution = strings.ReplaceAll(strings.TrimSpace(lines[0]), "\n", " ")
	}
	if resolution == "" {
		resolution = "Arquivos ajustados e validados sem erros de diagnóstico."
	}
	_ = c.repoPlaybook.RecordVerifiedRecovery(wd, query, g.firstFailure, resolution, g.editedFileList())
}
