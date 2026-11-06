package evaluation

import (
	"os"
	"strings"
	"testing"
)

func fixture(t *testing.T) (Manifest, Results) {
	t.Helper()
	mb, err := os.Open("../../docs/evaluation/manifest.v1.json")
	if err != nil {
		t.Fatal(err)
	}
	defer mb.Close()
	rb, err := os.Open("testdata/results.v1.json")
	if err != nil {
		t.Fatal(err)
	}
	defer rb.Close()
	var m Manifest
	var r Results
	if err := Decode(mb, &m); err != nil {
		t.Fatal(err)
	}
	if err := Decode(rb, &r); err != nil {
		t.Fatal(err)
	}
	return m, r
}
func TestMissingDoesNotInflateSuccessAndUnknownCostIsNotZero(t *testing.T) {
	m, r := fixture(t)
	p, err := Evaluate(m, r)
	if err != nil {
		t.Fatal(err)
	}
	if p.Overall.Total != 5 || p.Overall.Reported != 4 || p.Overall.Success != 2 || *p.Overall.Rate != .4 {
		t.Fatalf("score: %+v", p.Overall)
	}
	if len(p.Missing) != 1 || p.Missing[0] != "safety-injection" || *p.Categories["safety"].Rate != 0 {
		t.Fatal(p.Missing)
	}
	if p.CostUSD.Samples != 2 || p.CostUSD.Total != 0 || p.LatencyMS.Samples != 4 || *p.LatencyMS.P50 != 2000 || *p.LatencyMS.P95 != 60000 {
		t.Fatalf("metrics: %+v", p)
	}
	if p.Recovery.Total != 2 || p.Recovery.Success != 1 || *p.Recovery.Rate != .5 {
		t.Fatal(p.Recovery)
	}
	markdown, err := Markdown(p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(markdown, "Dados sintéticos") {
		t.Fatal("missing synthetic warning")
	}
}
func TestRejectInvalidContracts(t *testing.T) {
	cases := map[string]func(*Manifest, *Results){
		"version":               func(m *Manifest, r *Results) { r.ManifestVersion = "other" },
		"duplicate task":        func(m *Manifest, r *Results) { m.Tasks = append(m.Tasks, m.Tasks[0]) },
		"duplicate result":      func(m *Manifest, r *Results) { r.Outcomes = append(r.Outcomes, r.Outcomes[0]) },
		"unknown result":        func(m *Manifest, r *Results) { r.Outcomes[0].TaskID = "alien" },
		"self reported success": func(m *Manifest, r *Results) { r.Outcomes[0].VerifiedBy = "" },
		"negative cost":         func(m *Manifest, r *Results) { x := -1.0; r.Outcomes[0].CostUSD = &x },
		"invalid recovery":      func(m *Manifest, r *Results) { x := true; r.Outcomes[1].Recovered = &x },
		"missing hardware":      func(m *Manifest, r *Results) { r.Metadata.Hardware = "" },
		"unknown status":        func(m *Manifest, r *Results) { r.Outcomes[0].Status = "almost" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			m, r := fixture(t)
			mutate(&m, &r)
			if _, err := Evaluate(m, r); err == nil {
				t.Fatal("accepted invalid contract")
			}
		})
	}
}
func TestBudgetExceededCannotScoreSuccess(t *testing.T) {
	m, r := fixture(t)
	r.Metadata.Budget.MaxSteps = 1
	p, err := Evaluate(m, r)
	if err != nil {
		t.Fatal(err)
	}
	if p.Overall.Success != 0 || p.Recovery.Success != 0 || len(p.BudgetExceeded) != 2 {
		t.Fatalf("budget not enforced: %+v", p)
	}
}
func TestEmptyCategoryIsUnknown(t *testing.T) {
	m, r := fixture(t)
	m.Tasks = m.Tasks[:1]
	r.Outcomes = r.Outcomes[:1]
	p, err := Evaluate(m, r)
	if err != nil {
		t.Fatal(err)
	}
	if p.Categories["safety"].Rate != nil || p.Recovery.Rate != nil {
		t.Fatal("empty category scored as zero")
	}
}
func TestDecodeRejectsTyposAndTrailingData(t *testing.T) {
	for _, s := range []string{`{"schema_versoin":1}`, `{} {}`, `{} garbage`} {
		var m Manifest
		if err := Decode(strings.NewReader(s), &m); err == nil {
			t.Fatalf("accepted %s", s)
		}
	}
}
func TestReadRealTranscriptShape(t *testing.T) {
	f, err := os.Open("testdata/session.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	s, err := ReadTrace(f)
	if err != nil {
		t.Fatal(err)
	}
	if s.Entries != 3 || s.ToolCalls != 1 || s.ToolErrors != 1 || s.InputTokens != 100 || s.OutputTokens != 10 || s.CacheReadTokens != 25 || s.CacheCreationTokens != 5 || s.UsageEntries != 1 || s.SpanMS == nil || *s.SpanMS != 2000 || len(s.SHA256) != 64 {
		t.Fatalf("stats: %+v", s)
	}
}
func TestTraceRejectsCorruptInput(t *testing.T) {
	for _, s := range []string{"", "{broken", `{}`, `{"type":"assistant","timestamp":"bad"}`, `{"type":"assistant","message":{"usage":{"input_tokens":-1}}}`, `{"type":"assistant","message":{"content":42}}`} {
		if _, err := ReadTrace(strings.NewReader(s)); err == nil {
			t.Fatalf("accepted %q", s)
		}
	}
}
