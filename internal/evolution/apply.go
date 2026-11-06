package evolution

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/costa/polypod/internal/skill"
	"gopkg.in/yaml.v3"
)

const maxSkillBodyBytes = 16 << 10

func renderDraft(name string, sequence []string, successes, total int, evidence []Evidence) string {
	var body strings.Builder
	fmt.Fprintf(&body, "---\nname: %s\ndescription: Use este fluxo quando uma rotina da categoria %s se repetir.\n---\n\n", name, name)
	body.WriteString("# Procedimento verificado\n\n")
	body.WriteString("## Quando usar\n\nConfirme que o objetivo atual corresponde a esta rotina antes de agir.\n\n")
	body.WriteString("## Etapas observadas\n\n")
	body.WriteString("1. Observe o estado atual e confirme as entradas necessárias.\n")
	body.WriteString("2. Execute a sequência de ferramentas que se repetiu nos resultados verificados: ")
	for i, tool := range sequence {
		if i > 0 {
			body.WriteString(" → ")
		}
		fmt.Fprintf(&body, "`%s`", tool)
	}
	body.WriteString(". Use parâmetros do pedido atual; não reutilize argumentos, credenciais ou respostas históricas.\n")
	body.WriteString("3. Confira o resultado no sistema de destino antes de concluir a tarefa.\n\n")
	fmt.Fprintf(&body, "## Evidência para revisão\n\n%d resultados confirmados em %d registros. IDs de origem recentes:\n\n", successes, total)
	for _, item := range evidence {
		fmt.Fprintf(&body, "- `%s` (%s)\n", item.OutcomeID, outcomeLabel(item.Success))
	}
	return body.String()
}

func outcomeLabel(success bool) string {
	if success {
		return "sucesso"
	}
	return "falha"
}

// validateSkillBody keeps generated and reviewed drafts within the safe subset
// that the filesystem skill loader understands. Only name and description are
// allowed in frontmatter; arbitrary permission fields cannot be smuggled in.
func validateSkillBody(body, targetName string) error {
	if len(body) == 0 || len(body) > maxSkillBodyBytes || strings.ContainsRune(body, '\x00') {
		return errors.New("corpo da skill vazio, grande demais ou inválido")
	}
	if !strings.HasPrefix(body, "---\n") {
		return errors.New("SKILL.md exige frontmatter YAML")
	}
	end := strings.Index(body[4:], "\n---\n")
	if end < 0 {
		return errors.New("SKILL.md exige fechamento do frontmatter")
	}
	frontmatter := body[4 : 4+end]
	var fields map[string]any
	if err := yaml.Unmarshal([]byte(frontmatter), &fields); err != nil {
		return fmt.Errorf("frontmatter inválido: %w", err)
	}
	if len(fields) != 2 {
		return errors.New("frontmatter permite apenas name e description")
	}
	name, ok := fields["name"].(string)
	if !ok || name != targetName || !toolNamePattern.MatchString(name) {
		return errors.New("name deve ser igual ao alvo da skill")
	}
	description, ok := fields["description"].(string)
	if !ok || strings.TrimSpace(description) == "" || len(description) > 300 {
		return errors.New("description é obrigatória e limitada a 300 caracteres")
	}
	parsed, err := skill.ParseSkillFile("SKILL.md", []byte(body))
	if err != nil {
		return err
	}
	if err := parsed.Frontmatter.Validate(); err != nil {
		return err
	}
	if !strings.Contains(parsed.Body, "# ") || len(strings.TrimSpace(parsed.Body)) < 40 {
		return errors.New("a skill precisa de um procedimento em Markdown")
	}
	lower := strings.ToLower(body)
	for _, marker := range []string{"-----begin private key-----", "sk-live-", "sk_test_", "api_key="} {
		if strings.Contains(lower, marker) {
			return errors.New("material de credencial não deve ser armazenado em SKILL.md")
		}
	}
	return nil
}

// Apply publishes exactly the approved body. A hard link creates SKILL.md
// without overwriting an existing skill; SQLite's applying state lets Open
// reconcile a crash after publication but before the final status commit.
func (s *Store) Apply(ctx context.Context, id string) (Draft, error) {
	s.applyMu.Lock()
	defer s.applyMu.Unlock()
	draft, err := s.prepareApply(ctx, id)
	if err != nil {
		return Draft{}, err
	}
	if draft.Status == Applied {
		current, err := regularFileHash(s.skillPath(draft.TargetSkill))
		if err != nil {
			return Draft{}, err
		}
		if current != draft.BodySHA256 {
			return Draft{}, fmt.Errorf("SKILL.md aplicado foi alterado: %w", ErrStateConflict)
		}
		return draft, nil
	}
	if err := s.publishSkill(ctx, draft); err != nil {
		// A failed publication that did not create the intended file is safe
		// to return to Approved, so the reviewer can retry or discard it.
		current, checkErr := regularFileHash(s.skillPath(draft.TargetSkill))
		if errors.Is(checkErr, os.ErrNotExist) || (checkErr == nil && current != draft.BodySHA256) {
			if _, resetErr := s.db.ExecContext(context.Background(), `UPDATE drafts SET status='approved',updated_at=?
 WHERE id=? AND status='applying'`, time.Now().UnixMilli(), draft.ID); resetErr != nil {
				return Draft{}, errors.Join(err, resetErr)
			}
		}
		return Draft{}, err
	}
	if err := s.markApplied(ctx, draft.ID); err != nil {
		return Draft{}, err
	}
	return s.GetDraft(ctx, id)
}

func (s *Store) prepareApply(ctx context.Context, id string) (Draft, error) {
	draft, err := s.GetDraft(ctx, id)
	if err != nil {
		return Draft{}, err
	}
	if draft.Status == Applied {
		return draft, nil
	}
	if draft.Status != Approved && draft.Status != Applying {
		return Draft{}, ErrStateConflict
	}
	if draft.ReviewedBy == "" || hashBody(draft.Body) != draft.BodySHA256 {
		return Draft{}, errors.New("rascunho sem revisão íntegra")
	}
	if err := validateSkillBody(draft.Body, draft.TargetSkill); err != nil {
		return Draft{}, err
	}
	if draft.Status == Approved {
		res, err := s.db.ExecContext(ctx, `UPDATE drafts SET status='applying',updated_at=?
 WHERE id=? AND status='approved'`, time.Now().UnixMilli(), id)
		if err != nil {
			return Draft{}, err
		}
		if n, err := res.RowsAffected(); err != nil {
			return Draft{}, err
		} else if n != 1 {
			return Draft{}, ErrStateConflict
		}
		draft.Status = Applying
	}
	return draft, nil
}

func (s *Store) skillPath(name string) string {
	return filepath.Join(s.skillsDir, name, "SKILL.md")
}

func (s *Store) publishSkill(ctx context.Context, draft Draft) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.MkdirAll(s.skillsDir, 0o700); err != nil {
		return err
	}
	if err := requireRealDir(s.skillsDir); err != nil {
		return err
	}
	dir := filepath.Join(s.skillsDir, draft.TargetSkill)
	if err := os.Mkdir(dir, 0o755); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	if err := requireRealDir(dir); err != nil {
		return err
	}
	path := s.skillPath(draft.TargetSkill)
	if current, err := regularFileHash(path); err == nil {
		if current == draft.BodySHA256 {
			return nil
		}
		return fmt.Errorf("SKILL.md já existe com outro conteúdo: %s", path)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".SKILL-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := io.WriteString(tmp, draft.Body); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.Link(tmpPath, path); err != nil {
		if !errors.Is(err, os.ErrExist) {
			return err
		}
		current, checkErr := regularFileHash(path)
		if checkErr != nil || current != draft.BodySHA256 {
			return fmt.Errorf("SKILL.md já existe com outro conteúdo: %s", path)
		}
	}
	directory, err := os.Open(dir)
	if err != nil {
		return err
	}
	if err := directory.Sync(); err != nil {
		directory.Close()
		return err
	}
	return directory.Close()
}

func requireRealDir(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("diretório de skills inválido: %s", path)
	}
	return nil
}

func regularFileHash(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Size() > maxSkillBodyBytes {
		return "", fmt.Errorf("arquivo de skill inválido: %s", path)
	}
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	sum := sha256.New()
	if _, err := io.Copy(sum, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(sum.Sum(nil)), nil
}

func (s *Store) markApplied(ctx context.Context, id string) error {
	now := time.Now().UnixMilli()
	res, err := s.db.ExecContext(ctx, `UPDATE drafts SET status='applied',applied_at=?,updated_at=?
 WHERE id=? AND status='applying'`, now, now, id)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n != 1 {
		return ErrStateConflict
	}
	return nil
}

func (s *Store) recoverApplying(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM drafts WHERE status='applying'`)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, id := range ids {
		draft, err := s.GetDraft(ctx, id)
		if err != nil {
			return err
		}
		current, err := regularFileHash(s.skillPath(draft.TargetSkill))
		if err == nil && current == draft.BodySHA256 {
			if err := s.markApplied(ctx, id); err != nil {
				return err
			}
			continue
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if _, err := s.db.ExecContext(ctx, `UPDATE drafts SET status='approved',updated_at=?
 WHERE id=? AND status='applying'`, time.Now().UnixMilli(), id); err != nil {
			return err
		}
	}
	return nil
}
