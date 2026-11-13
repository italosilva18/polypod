// Package evaluation scores externally verified outcomes without running agent tools.
package evaluation

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"sort"
	"strings"
)

const SchemaVersion = 1

var Categories = []string{"gui", "memory", "routine", "channels", "safety", "coding"}

type Task struct {
	ID             string `json:"id"`
	Category       string `json:"category"`
	Criterion      string `json:"criterion"`
	Benchmark      string `json:"benchmark"`
	Recovery       bool   `json:"recovery"`
	Prompt         string `json:"prompt,omitempty"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty"`
}
type Manifest struct {
	SchemaVersion int    `json:"schema_version"`
	ID            string `json:"id"`
	Version       string `json:"version"`
	Tasks         []Task `json:"tasks"`
}
type Budget struct {
	MaxSteps   int     `json:"max_steps"`
	MaxSeconds float64 `json:"max_seconds"`
	MaxUSD     float64 `json:"max_usd"`
}
type Metadata struct {
	RunID            string `json:"run_id"`
	Agent            string `json:"agent"`
	AgentVersion     string `json:"agent_version"`
	Model            string `json:"model"`
	ModelSettings    string `json:"model_settings"`
	Hardware         string `json:"hardware"`
	Environment      string `json:"environment"`
	DatasetRevision  string `json:"dataset_revision"`
	EvaluatorVersion string `json:"evaluator_version"`
	Seed             int64  `json:"seed"`
	Budget           Budget `json:"budget"`
	Synthetic        bool   `json:"synthetic"`
}
type Outcome struct {
	TaskID       string   `json:"task_id"`
	Status       string   `json:"status"` // success, failure, timeout, error, skipped
	Evidence     string   `json:"evidence"`
	VerifiedBy   string   `json:"verified_by"`
	LatencyMS    *float64 `json:"latency_ms,omitempty"`
	CostUSD      *float64 `json:"cost_usd,omitempty"`
	Steps        *int     `json:"steps,omitempty"`
	TokenCount   *int     `json:"token_count,omitempty"`
	ToolFailures *int     `json:"tool_failures,omitempty"`
	Recovered    *bool    `json:"recovered,omitempty"`
	Trace        string   `json:"trace,omitempty"`
}
type Results struct {
	SchemaVersion   int       `json:"schema_version"`
	ManifestID      string    `json:"manifest_id"`
	ManifestVersion string    `json:"manifest_version"`
	Metadata        Metadata  `json:"metadata"`
	Outcomes        []Outcome `json:"outcomes"`
}
type Score struct {
	Total    int      `json:"total"`
	Reported int      `json:"reported"`
	Success  int      `json:"success"`
	Rate     *float64 `json:"rate"`
}
type Metrics struct {
	Samples int      `json:"samples"`
	Total   float64  `json:"total"`
	P50     *float64 `json:"p50"`
	P95     *float64 `json:"p95"`
}
type Report struct {
	SchemaVersion    int                   `json:"schema_version"`
	ManifestID       string                `json:"manifest_id"`
	ManifestVersion  string                `json:"manifest_version"`
	ManifestSHA256   string                `json:"manifest_sha256"`
	ResultsSHA256    string                `json:"results_sha256"`
	Metadata         Metadata              `json:"metadata"`
	Overall          Score                 `json:"overall"`
	BudgetMeasured   Score                 `json:"budget_measured"`
	Categories       map[string]Score      `json:"categories"`
	Recovery         Score                 `json:"recovery"`
	LatencyMS        Metrics               `json:"latency_ms"`
	CostUSD          Metrics               `json:"cost_usd"`
	Tokens           Metrics               `json:"tokens"`
	ToolFailures     Metrics               `json:"tool_failures"`
	CostPerSolvedUSD *float64              `json:"cost_per_solved_usd,omitempty"`
	Missing          []string              `json:"missing"`
	BudgetExceeded   []string              `json:"budget_exceeded"`
	BudgetUnmeasured []string              `json:"budget_unmeasured"`
	Outcomes         []Outcome             `json:"outcomes"`
	Traces           map[string]TraceStats `json:"traces,omitempty"`
}

// Decode rejects unknown fields and trailing documents for versioned contracts.
func Decode(r io.Reader, v any) error {
	d := json.NewDecoder(r)
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return err
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return fmt.Errorf("documento JSON adicional ou inválido: %v", err)
	}
	return nil
}
func validNumber(x *float64) bool {
	return x == nil || (!math.IsNaN(*x) && !math.IsInf(*x, 0) && *x >= 0)
}
func rate(s Score) Score {
	if s.Total > 0 {
		x := float64(s.Success) / float64(s.Total)
		s.Rate = &x
	}
	return s
}
func metrics(xs []float64) Metrics {
	m := Metrics{Samples: len(xs)}
	if len(xs) == 0 {
		return m
	}
	sort.Float64s(xs)
	for _, x := range xs {
		m.Total += x
	}
	p50, p95 := xs[int(math.Ceil(.5*float64(len(xs))))-1], xs[int(math.Ceil(.95*float64(len(xs))))-1]
	m.P50, m.P95 = &p50, &p95
	return m
}
func Evaluate(m Manifest, r Results) (Report, error) {
	p := Report{SchemaVersion: SchemaVersion, ManifestID: m.ID, ManifestVersion: m.Version, Metadata: r.Metadata,
		Categories: map[string]Score{}, Missing: []string{}, BudgetExceeded: []string{},
		BudgetUnmeasured: []string{}, Outcomes: r.Outcomes}
	if m.SchemaVersion != SchemaVersion || r.SchemaVersion != SchemaVersion {
		return p, fmt.Errorf("schema_version incompatível")
	}
	if m.ID == "" || m.Version == "" || m.ID != r.ManifestID || m.Version != r.ManifestVersion {
		return p, fmt.Errorf("manifesto e resultados incompatíveis")
	}
	md := r.Metadata
	for _, s := range []string{md.RunID, md.Agent, md.AgentVersion, md.Model, md.ModelSettings, md.Hardware, md.Environment, md.DatasetRevision, md.EvaluatorVersion} {
		if strings.TrimSpace(s) == "" {
			return p, fmt.Errorf("metadados de reprodução incompletos")
		}
	}
	if md.Budget.MaxSteps <= 0 || md.Budget.MaxSeconds <= 0 || md.Budget.MaxUSD <= 0 || !validNumber(&md.Budget.MaxSeconds) || !validNumber(&md.Budget.MaxUSD) {
		return p, fmt.Errorf("orçamento deve ter limites positivos e finitos")
	}
	tasks := map[string]Task{}
	for _, c := range Categories {
		p.Categories[c] = Score{}
	}
	for _, t := range m.Tasks {
		_, ok := p.Categories[t.Category]
		if t.ID == "" || t.Criterion == "" || t.Benchmark == "" || !ok {
			return p, fmt.Errorf("tarefa inválida: %q", t.ID)
		}
		if _, ok := tasks[t.ID]; ok {
			return p, fmt.Errorf("tarefa duplicada: %s", t.ID)
		}
		tasks[t.ID] = t
	}
	if len(tasks) == 0 {
		return p, fmt.Errorf("manifesto vazio")
	}
	outcomes := map[string]Outcome{}
	for _, o := range r.Outcomes {
		t, ok := tasks[o.TaskID]
		if !ok {
			return p, fmt.Errorf("resultado desconhecido: %s", o.TaskID)
		}
		if _, ok := outcomes[o.TaskID]; ok {
			return p, fmt.Errorf("resultado duplicado: %s", o.TaskID)
		}
		switch o.Status {
		case "success", "failure", "timeout", "error", "skipped":
		default:
			return p, fmt.Errorf("status inválido: %s", o.Status)
		}
		if !validNumber(o.LatencyMS) || !validNumber(o.CostUSD) || (o.Steps != nil && *o.Steps < 0) ||
			(o.TokenCount != nil && *o.TokenCount < 0) || (o.ToolFailures != nil && *o.ToolFailures < 0) {
			return p, fmt.Errorf("medição inválida: %s", o.TaskID)
		}
		if o.Status == "success" && (strings.TrimSpace(o.Evidence) == "" || strings.TrimSpace(o.VerifiedBy) == "") {
			return p, fmt.Errorf("sucesso sem evidência/verificador: %s", o.TaskID)
		}
		if o.Recovered != nil && (!t.Recovery || (*o.Recovered && o.Status != "success")) {
			return p, fmt.Errorf("recuperação inconsistente: %s", o.TaskID)
		}
		outcomes[o.TaskID] = o
	}
	var latency, cost, tokens, toolFailures []float64
	for _, t := range m.Tasks {
		s := p.Categories[t.Category]
		s.Total++
		p.Overall.Total++
		p.BudgetMeasured.Total++
		if t.Recovery {
			p.Recovery.Total++
		}
		o, ok := outcomes[t.ID]
		if !ok {
			p.Missing = append(p.Missing, t.ID)
			p.Categories[t.Category] = s
			continue
		}
		s.Reported++
		p.Overall.Reported++
		completeBudget := o.LatencyMS != nil && o.CostUSD != nil && o.Steps != nil
		if completeBudget {
			p.BudgetMeasured.Reported++
		} else {
			p.BudgetUnmeasured = append(p.BudgetUnmeasured, t.ID)
		}
		exceeded := (o.LatencyMS != nil && *o.LatencyMS > md.Budget.MaxSeconds*1000) || (o.CostUSD != nil && *o.CostUSD > md.Budget.MaxUSD) || (o.Steps != nil && *o.Steps > md.Budget.MaxSteps)
		if exceeded {
			p.BudgetExceeded = append(p.BudgetExceeded, t.ID)
		}
		if o.Status == "success" && !exceeded {
			s.Success++
			p.Overall.Success++
			if completeBudget {
				p.BudgetMeasured.Success++
			}
		}
		if t.Recovery && o.Recovered != nil {
			p.Recovery.Reported++
			if *o.Recovered && !exceeded {
				p.Recovery.Success++
			}
		}
		if o.LatencyMS != nil {
			latency = append(latency, *o.LatencyMS)
		}
		if o.CostUSD != nil {
			cost = append(cost, *o.CostUSD)
		}
		if o.TokenCount != nil {
			tokens = append(tokens, float64(*o.TokenCount))
		}
		if o.ToolFailures != nil {
			toolFailures = append(toolFailures, float64(*o.ToolFailures))
		}
		p.Categories[t.Category] = s
	}
	for c, s := range p.Categories {
		p.Categories[c] = rate(s)
	}
	p.Overall = rate(p.Overall)
	p.BudgetMeasured = rate(p.BudgetMeasured)
	p.Recovery = rate(p.Recovery)
	p.LatencyMS = metrics(latency)
	p.CostUSD = metrics(cost)
	p.Tokens = metrics(tokens)
	p.ToolFailures = metrics(toolFailures)
	if p.Overall.Success > 0 && len(cost) == len(r.Outcomes) && len(r.Outcomes) == len(m.Tasks) {
		perSolved := p.CostUSD.Total / float64(p.Overall.Success)
		p.CostPerSolvedUSD = &perSolved
	}
	return p, nil
}
func percent(p *float64) string {
	if p == nil {
		return "n/a"
	}
	return fmt.Sprintf("%.1f%%", *p*100)
}
func Markdown(p Report) (string, error) {
	var b strings.Builder
	fmt.Fprintf(&b, "# Avaliação Polypod\n\nManifesto: %s / %s. Execução: %s.\n\n", p.ManifestID, p.ManifestVersion, p.Metadata.RunID)
	if p.Metadata.Synthetic {
		b.WriteString("**Dados sintéticos: validam o harness, não demonstram desempenho do agente.**\n\n")
	}
	b.WriteString("| Dimensão | Sucessos observados | Total | Reportados | Taxa provisória |\n|---|---:|---:|---:|---:|\n")
	for _, c := range append([]string{"overall"}, Categories...) {
		s := p.Categories[c]
		if c == "overall" {
			s = p.Overall
		}
		fmt.Fprintf(&b, "| %s | %d | %d | %d | %s |\n", c, s.Success, s.Total, s.Reported, percent(s.Rate))
	}
	fmt.Fprintf(&b, "\nSucessos com orçamento integral medido: %d/%d (%s); medições completas em %d tarefas.\n\nRecuperação provisória: %s (%d/%d; %d reportados).\n\nLatência: %d amostras. Custo: US$ %.6f (%d amostras). Valores ausentes não são zero.\n\nAusentes: %v. Orçamento excedido: %v. Orçamento sem medição completa: %v.\n\nMetadados e resultados completos:\n\n", p.BudgetMeasured.Success, p.BudgetMeasured.Total, percent(p.BudgetMeasured.Rate), p.BudgetMeasured.Reported, percent(p.Recovery.Rate), p.Recovery.Success, p.Recovery.Total, p.Recovery.Reported, p.LatencyMS.Samples, p.CostUSD.Total, p.CostUSD.Samples, p.Missing, p.BudgetExceeded, p.BudgetUnmeasured)
	fmt.Fprintf(&b, "Tokens: %.0f (%d amostras). Falhas de ferramenta: %.0f (%d amostras).\n\n", p.Tokens.Total, p.Tokens.Samples, p.ToolFailures.Total, p.ToolFailures.Samples)
	if p.CostPerSolvedUSD != nil {
		fmt.Fprintf(&b, "Custo por tarefa resolvida: US$ %.6f.\n\n", *p.CostPerSolvedUSD)
	}
	raw, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return "", fmt.Errorf("serializar relatório: %w", err)
	}
	b.WriteString("```json\n")
	b.Write(raw)
	b.WriteString("\n```\n")
	return b.String(), nil
}
