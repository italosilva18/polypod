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
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	maxExperienceCandidates = 256
	maxExperienceRunes      = 1800
)

type appliedExperience struct {
	name     string
	pattern  string
	expected string
	score    int
	body     string
}

type verifiedExperience struct {
	pattern   string
	successes int
	total     int
	score     int
}

// SearchVerified summarizes repeated, independently checked outcomes without
// copying prompts, arguments, result text, file paths, or evidence references.
// It is advisory evidence and cannot register or execute a skill by itself.
func (s *Store) SearchVerified(ctx context.Context, query string, limit int) ([]string, error) {
	if limit < 1 || limit > 3 {
		return nil, fmt.Errorf("limite de experiências deve estar entre 1 e 3")
	}
	terms := experienceTerms(query)
	if len(terms) == 0 {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT pattern_key,SUM(success),COUNT(*) FROM outcomes
	 WHERE source_kind IN ('task','routine') AND verified_by IN
	 ('file_sha256_v1','http_status_v1','http_json_v1','sqlite_scalar_v1')
	 GROUP BY pattern_key HAVING SUM(success)>=? AND SUM(success)*4>=COUNT(*)*3
	 ORDER BY COUNT(*) DESC,pattern_key LIMIT ?`, minSuccesses, maxExperienceCandidates)
	if err != nil {
		return nil, err
	}
	var candidates []verifiedExperience
	for rows.Next() {
		var candidate verifiedExperience
		if err := rows.Scan(&candidate.pattern, &candidate.successes, &candidate.total); err != nil {
			rows.Close()
			return nil, err
		}
		for term := range experienceTerms(candidate.pattern) {
			if terms[term] {
				candidate.score += 2
			}
		}
		if candidate.score == 0 {
			continue
		}
		if strings.Contains(strings.ToLower(query), candidate.pattern) {
			candidate.score += 4
		}
		candidates = append(candidates, candidate)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].score != candidates[j].score {
			return candidates[i].score > candidates[j].score
		}
		return candidates[i].pattern < candidates[j].pattern
	})
	if len(candidates) > limit {
		candidates = candidates[:limit]
	}
	out := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		outcomes, err := s.Outcomes(ctx, candidate.pattern)
		if err != nil {
			return nil, err
		}
		verified := make([]Outcome, 0, len(outcomes))
		for _, outcome := range outcomes {
			if (outcome.SourceKind == "task" || outcome.SourceKind == "routine") && internalVerifier(outcome.VerifiedBy) {
				verified = append(verified, outcome)
			}
		}
		sequence, repeated := mostFrequentSequence(verified)
		if len(sequence) == 0 || repeated < 2 {
			continue
		}
		verifiers := make(map[string]bool)
		for _, outcome := range verified {
			if outcome.Success {
				verifiers[outcome.VerifiedBy] = true
			}
		}
		checkerNames := make([]string, 0, len(verifiers))
		for name := range verifiers {
			checkerNames = append(checkerNames, name)
		}
		sort.Strings(checkerNames)
		out = append(out, fmt.Sprintf("%d sucessos em %d execuções verificadas; sequência observada em %d sucessos: %s. Verificadores: %s.",
			candidate.successes, candidate.total, repeated, strings.Join(sequence, " → "), strings.Join(checkerNames, ", ")))
	}
	return out, nil
}

// SearchApplied retrieves only reviewed, applied skills that still match their
// approved file hash. Raw outcomes, tool arguments and private evidence never
// enter the model context. A category must have a meaningful lexical overlap
// with the current request before its procedure is suggested.
func (s *Store) SearchApplied(ctx context.Context, query string, limit int) ([]string, error) {
	if limit < 1 || limit > 3 {
		return nil, fmt.Errorf("limite de experiências deve estar entre 1 e 3")
	}
	terms := experienceTerms(query)
	if len(terms) == 0 {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT pattern_key,target_skill,body_sha256 FROM drafts
	 WHERE status='applied' ORDER BY updated_at DESC LIMIT ?`, maxExperienceCandidates)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var candidates []appliedExperience
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var candidate appliedExperience
		if err := rows.Scan(&candidate.pattern, &candidate.name, &candidate.expected); err != nil {
			return nil, err
		}
		if !toolNamePattern.MatchString(candidate.name) {
			continue
		}
		patternTerms := experienceTerms(candidate.pattern)
		for term := range patternTerms {
			if terms[term] {
				candidate.score += 2
			}
		}
		if candidate.score == 0 {
			continue
		}
		if strings.Contains(strings.ToLower(query), candidate.pattern) {
			candidate.score += 4
		}
		body, err := s.readAppliedBody(candidate.name)
		if err != nil {
			continue
		}
		sum := sha256.Sum256(body)
		if hex.EncodeToString(sum[:]) != candidate.expected {
			continue
		}
		candidate.body = string(body)
		candidates = append(candidates, candidate)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].score != candidates[j].score {
			return candidates[i].score > candidates[j].score
		}
		return candidates[i].name < candidates[j].name
	})
	if len(candidates) > limit {
		candidates = candidates[:limit]
	}
	out := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		body := candidate.body
		if utf8.RuneCountInString(body) > maxExperienceRunes {
			body = string([]rune(body)[:maxExperienceRunes]) + "…"
		}
		out = append(out, fmt.Sprintf("Skill revisada `%s`:\n%s", candidate.name, body))
	}
	return out, nil
}

func (s *Store) readAppliedBody(name string) ([]byte, error) {
	root, err := os.OpenRoot(s.skillsDir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	f, err := root.OpenFile(filepath.Join(name, "SKILL.md"), os.O_RDONLY|readOnlyNoFollowFlags, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > maxSkillBodyBytes {
		return nil, errors.New("skill aplicada não é arquivo regular dentro do limite")
	}
	body, err := io.ReadAll(io.LimitReader(f, maxSkillBodyBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxSkillBodyBytes {
		return nil, errors.New("skill aplicada excede limite")
	}
	return body, nil
}

var experienceStopWords = map[string]bool{
	"para": true, "com": true, "como": true, "uma": true, "um": true,
	"que": true, "por": true, "dos": true, "das": true, "está": true,
	"esta": true, "fazer": true, "faça": true, "faca": true, "preciso": true,
	"the": true, "and": true, "with": true, "from": true, "this": true,
}

func experienceTerms(value string) map[string]bool {
	terms := make(map[string]bool)
	var word []rune
	flush := func() {
		if len(word) >= 4 {
			term := strings.ToLower(string(word))
			if !experienceStopWords[term] {
				terms[term] = true
			}
		}
		word = word[:0]
	}
	for _, r := range value {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			word = append(word, r)
		} else {
			flush()
		}
	}
	flush()
	return terms
}
