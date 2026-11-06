// Package commands — batch23_security.go
//
// Security slash commands for rodada 3 wave 1 batch 23.
//
// Territory asked for /perm-audit, /secret-scan and /vuln-check. Only
// /perm-audit has a real implementation wired into this batch: it reads the
// append-only JSON-lines audit trail produced by internal/approval (via
// approval.DefaultAuditLogPath) and prints a summary of the most recent
// approval decisions. /secret-scan and /vuln-check stay as placeholders
// until external tooling (gitleaks, govulncheck, semgrep...) is wired in a
// later rodada.
//
// Commands registered by RegisterBatch23Security:
//
//   - /perm-audit   — summarises the last 20 approval decisions on disk
//   - /secret-scan  — placeholder "scan de segredos pendente wiring"
//   - /vuln-check   — placeholder "verificacao de vulnerabilidades pendente wiring"
//
// Identifiers are suffixed with "B23" to keep them isolated from the rest of
// the package.
package commands

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/costa/polypod/internal/approval"
	"github.com/costa/polypod/internal/command"
)

// ---------------------------------------------------------------------------
// Placeholder display strings
// ---------------------------------------------------------------------------

const (
	secretScanPlaceholderB23 = "scan de segredos pendente wiring (use semgrep/gitleaks externamente)"
	vulnCheckPlaceholderB23  = "verificacao de vulnerabilidades pendente wiring (use govulncheck externamente)"
)

// permAuditWindowB23 is the number of most-recent entries summarised by
// /perm-audit. The territory brief pins this at 20.
const permAuditWindowB23 = 20

// permAuditTailB23 is how many of the most recent entries are printed in
// full (time / tool / decision). The brief pins this at 5.
const permAuditTailB23 = 5

// ---------------------------------------------------------------------------
// Registration
// ---------------------------------------------------------------------------

// RegisterBatch23Security registers the batch23 security-oriented commands on
// the supplied registry.
func RegisterBatch23Security(reg *command.Registry) {
	reg.Register(cmdPermAuditB23())
	reg.Register(cmdSecretScanB23())
	reg.Register(cmdVulnCheckB23())
}

// ---------------------------------------------------------------------------
// /perm-audit — summarise the approval audit log
// ---------------------------------------------------------------------------

func cmdPermAuditB23() *command.Command {
	return &command.Command{
		Name:        "perm-audit",
		Description: "Resumir decisoes recentes do log de aprovacao (approval-audit.jsonl)",
		Type:        command.CommandLocal,
		Source:      command.SourceBuiltin,
		Execute: func(args []string, ctx command.CommandContext) (*command.CommandResult, error) {
			path := approval.DefaultAuditLogPath()
			out, err := formatPermAuditB23(path)
			if err != nil {
				return errResult(err.Error()), nil
			}
			return okResult(out), nil
		},
	}
}

// formatPermAuditB23 reads the audit log at path and renders a Portuguese
// summary of the tail window. It handles the missing-file case gracefully by
// returning a friendly "nenhum registro" message rather than an error.
func formatPermAuditB23(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Sprintf(
				"Auditoria de permissoes\n\nNenhum registro encontrado em %s.\n(Log vazio ou ainda nao criado.)",
				path,
			), nil
		}
		return "", fmt.Errorf("falha ao ler log de auditoria %s: %w", path, err)
	}

	entries, parseErrs := parsePermAuditJSONLB23(data)

	total := len(entries)
	window := permAuditWindowB23
	if window > total {
		window = total
	}
	tail := entries
	if total > window {
		tail = entries[total-window:]
	}

	// Count by Decision across the tail window.
	counts := make(map[string]int, 4)
	for _, e := range tail {
		d := e.Decision
		if d == "" {
			d = "unknown"
		}
		counts[d]++
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Auditoria de permissoes\n\nArquivo: %s\n", path)
	fmt.Fprintf(&b, "Entradas totais: %d | janela analisada: %d\n", total, len(tail))
	if parseErrs > 0 {
		fmt.Fprintf(&b, "Linhas invalidas ignoradas: %d\n", parseErrs)
	}

	if len(tail) == 0 {
		b.WriteString("\nNenhuma decisao registrada ate o momento.")
		return b.String(), nil
	}

	// Stable order for the decision summary: descending by count, then name.
	type kv struct {
		k string
		v int
	}
	kvs := make([]kv, 0, len(counts))
	for k, v := range counts {
		kvs = append(kvs, kv{k, v})
	}
	sort.Slice(kvs, func(i, j int) bool {
		if kvs[i].v != kvs[j].v {
			return kvs[i].v > kvs[j].v
		}
		return kvs[i].k < kvs[j].k
	})

	b.WriteString("\nDecisoes na janela:\n")
	for _, p := range kvs {
		fmt.Fprintf(&b, "  %-10s %d\n", p.k, p.v)
	}

	// Print the last permAuditTailB23 entries in full.
	lastN := permAuditTailB23
	if lastN > len(tail) {
		lastN = len(tail)
	}
	last := tail[len(tail)-lastN:]
	fmt.Fprintf(&b, "\nUltimas %d entradas:\n", len(last))
	for _, e := range last {
		ts := e.Timestamp.Format("2006-01-02 15:04:05")
		tool := e.ToolName
		if tool == "" {
			tool = "(sem tool)"
		}
		decision := e.Decision
		if decision == "" {
			decision = "unknown"
		}
		risk := e.RiskLevel
		if risk == "" {
			risk = "-"
		}
		fmt.Fprintf(&b, "  [%s] %s risk=%s decisao=%s", ts, tool, risk, decision)
		if e.Reason != "" {
			fmt.Fprintf(&b, " motivo=%q", e.Reason)
		}
		b.WriteString("\n")
	}

	return strings.TrimRight(b.String(), "\n"), nil
}

// parsePermAuditJSONLB23 parses the audit file content as JSON-lines using
// bufio.Scanner. Blank lines and malformed lines are skipped; the number of
// malformed lines is returned so the caller can surface it to the user.
func parsePermAuditJSONLB23(data []byte) ([]approval.AuditEntry, int) {
	var entries []approval.AuditEntry
	parseErrs := 0

	scanner := bufio.NewScanner(bytes.NewReader(data))
	// Audit entries can include long Input maps; give the scanner room.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var entry approval.AuditEntry
		if err := json.Unmarshal(line, &entry); err != nil {
			parseErrs++
			continue
		}
		entries = append(entries, entry)
	}
	if err := scanner.Err(); err != nil {
		// A scanner error means we truncated mid-stream; treat the rest as
		// unparseable but keep what we already have.
		parseErrs++
	}
	return entries, parseErrs
}

// ---------------------------------------------------------------------------
// /secret-scan [path] — placeholder
// ---------------------------------------------------------------------------

func cmdSecretScanB23() *command.Command {
	return &command.Command{
		Name:        "secret-scan",
		Description: "Rodar scan de segredos no repositorio (em fila ate a integracao ser concluida)",
		Type:        command.CommandLocal,
		Source:      command.SourceBuiltin,
		Execute: func(args []string, ctx command.CommandContext) (*command.CommandResult, error) {
			return okResult(secretScanPlaceholderB23), nil
		},
	}
}

// ---------------------------------------------------------------------------
// /vuln-check [path] — placeholder
// ---------------------------------------------------------------------------

func cmdVulnCheckB23() *command.Command {
	return &command.Command{
		Name:        "vuln-check",
		Description: "Verificar vulnerabilidades em dependencias (em fila ate a integracao ser concluida)",
		Type:        command.CommandLocal,
		Source:      command.SourceBuiltin,
		Execute: func(args []string, ctx command.CommandContext) (*command.CommandResult, error) {
			return okResult(vulnCheckPlaceholderB23), nil
		},
	}
}
