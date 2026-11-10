package smartroute

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"

	"github.com/costa/polypod/internal/evaluation"
)

const PolicySchemaVersion = 1

// Minimum paired observations and seeds needed before a challenger can replace
// the configured model. These are conservative gates, not a claim that a small
// evaluation set generalizes to arbitrary user requests.
const (
	minimumCategorySamples = 10
	minimumPairedSeeds     = 3
)

type RouteEvidence struct {
	Model               string  `json:"model"`
	Samples             int     `json:"samples"`
	SuccessRate         float64 `json:"success_rate"`
	BaselineSuccessRate float64 `json:"baseline_success_rate"`
	MeanCostUSD         float64 `json:"mean_cost_usd"`
}

// CalibratedPolicy is generated offline from externally verified, comparable
// evaluation runs. Its routes only select model IDs served by the configured
// primary provider; runtime validation applies an explicit allowlist.
type CalibratedPolicy struct {
	SchemaVersion   int                      `json:"schema_version"`
	Provider        string                   `json:"provider"`
	DefaultModel    string                   `json:"default_model"`
	ManifestID      string                   `json:"manifest_id"`
	ManifestVersion string                   `json:"manifest_version"`
	Routes          map[string]RouteEvidence `json:"routes"`
}

func (p CalibratedPolicy) Validate(provider, defaultModel string, allowed []string) error {
	if p.SchemaVersion != PolicySchemaVersion || p.Provider != provider || p.DefaultModel != defaultModel ||
		strings.TrimSpace(p.ManifestID) == "" || strings.TrimSpace(p.ManifestVersion) == "" {
		return errors.New("política de modelos incompatível com o provedor ou modelo padrão")
	}
	allow := map[string]bool{defaultModel: true}
	for _, model := range allowed {
		if strings.TrimSpace(model) != "" {
			allow[model] = true
		}
	}
	validCategories := make(map[string]bool, len(evaluation.Categories))
	for _, category := range evaluation.Categories {
		validCategories[category] = true
	}
	for category, route := range p.Routes {
		if !validCategories[category] || !allow[route.Model] || route.Model == defaultModel ||
			route.Samples < minimumCategorySamples ||
			math.IsNaN(route.SuccessRate) || math.IsInf(route.SuccessRate, 0) ||
			math.IsNaN(route.BaselineSuccessRate) || math.IsInf(route.BaselineSuccessRate, 0) ||
			route.SuccessRate < 0 || route.SuccessRate > 1 ||
			route.BaselineSuccessRate < 0 || route.BaselineSuccessRate > 1 ||
			math.IsNaN(route.MeanCostUSD) || math.IsInf(route.MeanCostUSD, 0) || route.MeanCostUSD < 0 {
			return fmt.Errorf("rota de modelo inválida para %q", category)
		}
	}
	return nil
}

func (p CalibratedPolicy) ModelFor(category string) string {
	if route, ok := p.Routes[category]; ok {
		return route.Model
	}
	return ""
}

func (p CalibratedPolicy) Clone() CalibratedPolicy {
	copy := p
	copy.Routes = make(map[string]RouteEvidence, len(p.Routes))
	for category, route := range p.Routes {
		copy.Routes[category] = route
	}
	return copy
}

func ReadPolicy(path string) (CalibratedPolicy, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return CalibratedPolicy{}, fmt.Errorf("ler política de modelos: %w", err)
	}
	var policy CalibratedPolicy
	if err := evaluation.Decode(bytes.NewReader(data), &policy); err != nil {
		return CalibratedPolicy{}, fmt.Errorf("decodificar política de modelos: %w", err)
	}
	return policy, nil
}

type measurement struct {
	successes int
	total     int
	cost      float64
}

type runKey struct {
	model string
	seed  int64
}

// BuildCalibratedPolicy compares matched seeds and the same task IDs under
// fixed environment/settings/budgets. Every outcome must have independent
// success evidence and complete latency, cost and step measurements. The
// baseline remains selected until a challenger has a higher 95% Wilson lower
// bound than the baseline upper bound for a category.
func BuildCalibratedPolicy(manifest evaluation.Manifest, results []evaluation.Results,
	provider, defaultModel string, allowed []string) (CalibratedPolicy, error) {
	policy := CalibratedPolicy{
		SchemaVersion: PolicySchemaVersion, Provider: provider, DefaultModel: defaultModel,
		ManifestID: manifest.ID, ManifestVersion: manifest.Version,
		Routes: make(map[string]RouteEvidence),
	}
	if strings.TrimSpace(provider) == "" || strings.TrimSpace(defaultModel) == "" {
		return policy, errors.New("provedor e modelo padrão são obrigatórios")
	}
	if len(results) < 2 {
		return policy, errors.New("forneça execuções reais do modelo padrão e dos candidatos")
	}
	for _, task := range manifest.Tasks {
		if strings.TrimSpace(task.Prompt) == "" {
			return policy, fmt.Errorf("manifesto real exige prompt na tarefa %q", task.ID)
		}
	}
	allowedModels := map[string]bool{defaultModel: true}
	for _, model := range allowed {
		if strings.TrimSpace(model) != "" {
			allowedModels[model] = true
		}
	}
	byRun := make(map[runKey]evaluation.Results, len(results))
	modelSeeds := make(map[string]map[int64]bool)
	var common evaluation.Metadata
	for i, result := range results {
		report, err := evaluation.Evaluate(manifest, result)
		if err != nil {
			return policy, fmt.Errorf("avaliar execução %d: %w", i, err)
		}
		md := result.Metadata
		if md.Synthetic || md.Agent != "polypod" || !allowedModels[md.Model] || len(report.Missing) > 0 {
			return policy, fmt.Errorf("execução %d é sintética, incompleta ou usa modelo não permitido", i)
		}
		if i == 0 {
			common = md
		} else if !comparableMetadata(common, md) {
			return policy, fmt.Errorf("execução %d não é comparável ao protocolo inicial", i)
		}
		key := runKey{model: md.Model, seed: md.Seed}
		if _, exists := byRun[key]; exists {
			return policy, fmt.Errorf("modelo %q e seed %d duplicados", md.Model, md.Seed)
		}
		for _, outcome := range result.Outcomes {
			if outcome.LatencyMS == nil || outcome.CostUSD == nil || outcome.Steps == nil {
				return policy, fmt.Errorf("medição incompleta na execução %q, tarefa %q", md.RunID, outcome.TaskID)
			}
		}
		byRun[key] = result
		if modelSeeds[md.Model] == nil {
			modelSeeds[md.Model] = make(map[int64]bool)
		}
		modelSeeds[md.Model][md.Seed] = true
	}
	baselineSeeds := modelSeeds[defaultModel]
	if len(baselineSeeds) < minimumPairedSeeds {
		return policy, fmt.Errorf("modelo padrão exige pelo menos %d seeds", minimumPairedSeeds)
	}
	models := make([]string, 0, len(modelSeeds))
	for model := range modelSeeds {
		if model != defaultModel {
			models = append(models, model)
		}
	}
	sort.Strings(models)
	if len(models) == 0 {
		return policy, errors.New("nenhum modelo candidato avaliado")
	}
	for _, category := range evaluation.Categories {
		var best *RouteEvidence
		for _, model := range models {
			if !sameSeeds(baselineSeeds, modelSeeds[model]) {
				return policy, fmt.Errorf("seeds do modelo %q diferem do modelo padrão", model)
			}
			seeds := pairedSeeds(baselineSeeds, modelSeeds[model])
			if len(seeds) < minimumPairedSeeds {
				continue
			}
			var baseline, candidate measurement
			for _, seed := range seeds {
				baseline.addCategory(manifest, byRun[runKey{model: defaultModel, seed: seed}], category)
				candidate.addCategory(manifest, byRun[runKey{model: model, seed: seed}], category)
			}
			if baseline.total < minimumCategorySamples || candidate.total != baseline.total {
				continue
			}
			if wilsonLower(candidate.successes, candidate.total) <= wilsonUpper(baseline.successes, baseline.total) {
				continue
			}
			route := RouteEvidence{Model: model, Samples: candidate.total,
				SuccessRate:         float64(candidate.successes) / float64(candidate.total),
				BaselineSuccessRate: float64(baseline.successes) / float64(baseline.total),
				MeanCostUSD:         candidate.cost / float64(candidate.total)}
			if best == nil || route.SuccessRate > best.SuccessRate ||
				(route.SuccessRate == best.SuccessRate && route.MeanCostUSD < best.MeanCostUSD) {
				best = &route
			}
		}
		if best != nil {
			policy.Routes[category] = *best
		}
	}
	return policy, policy.Validate(provider, defaultModel, allowed)
}

func comparableMetadata(a, b evaluation.Metadata) bool {
	return a.Agent == b.Agent && a.AgentVersion == b.AgentVersion &&
		a.ModelSettings == b.ModelSettings && a.Hardware == b.Hardware &&
		a.Environment == b.Environment && a.DatasetRevision == b.DatasetRevision &&
		a.EvaluatorVersion == b.EvaluatorVersion && a.Budget == b.Budget
}

func pairedSeeds(a, b map[int64]bool) []int64 {
	var seeds []int64
	for seed := range a {
		if b[seed] {
			seeds = append(seeds, seed)
		}
	}
	sort.Slice(seeds, func(i, j int) bool { return seeds[i] < seeds[j] })
	return seeds
}

func sameSeeds(a, b map[int64]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for seed := range a {
		if !b[seed] {
			return false
		}
	}
	return true
}

func (m *measurement) addCategory(manifest evaluation.Manifest, results evaluation.Results, category string) {
	outcomes := make(map[string]evaluation.Outcome, len(results.Outcomes))
	for _, outcome := range results.Outcomes {
		outcomes[outcome.TaskID] = outcome
	}
	for _, task := range manifest.Tasks {
		if task.Category != category {
			continue
		}
		outcome := outcomes[task.ID]
		m.total++
		m.cost += *outcome.CostUSD
		if outcome.Status == "success" && *outcome.LatencyMS <= results.Metadata.Budget.MaxSeconds*1000 &&
			*outcome.CostUSD <= results.Metadata.Budget.MaxUSD && *outcome.Steps <= results.Metadata.Budget.MaxSteps {
			m.successes++
		}
	}
}

func wilsonLower(successes, total int) float64 {
	return wilson(successes, total, -1)
}

func wilsonUpper(successes, total int) float64 {
	return wilson(successes, total, 1)
}

func wilson(successes, total, direction int) float64 {
	if total == 0 {
		return 0
	}
	const z = 1.96
	n := float64(total)
	p := float64(successes) / n
	center := p + z*z/(2*n)
	spread := z * math.Sqrt(p*(1-p)/n+z*z/(4*n*n))
	return (center + float64(direction)*spread) / (1 + z*z/n)
}
