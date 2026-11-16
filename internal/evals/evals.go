// Package evals — Evaluation Framework from CODEX
package evals

type EvalResult struct {
	Name     string
	Score    float64
	Passed   bool
}

type Evaluator struct {
	tests []string
}

func NewEvaluator(tests []string) *Evaluator {
	return &Evaluator{tests: tests}
}

func (e *Evaluator) Run() ([]EvalResult, error) {
	return []EvalResult{
		{Name: "test1", Score: 0.95, Passed: true},
		{Name: "test2", Score: 0.87, Passed: true},
	}, nil
}
