package projectchecks

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFastDiagnoseFile_GoJSONYAML(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	// Create a minimal Go module so scoped `go vet` also runs deterministically.
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/fastchecktest\n\ngo 1.22\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// 1. Broken Go syntax
	brokenSyntax := filepath.Join(dir, "broken_syntax.go")
	if err := os.WriteFile(brokenSyntax, []byte("package fastchecktest\n\nfunc Broken( {\n\treturn\n}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	diag := FastDiagnoseFile(ctx, brokenSyntax)
	if !strings.Contains(diag, AutoDiagnosticHeader) {
		t.Fatalf("expected AutoDiagnosticHeader on broken Go syntax, got %q", diag)
	}
	_ = os.Remove(brokenSyntax)

	// 2. Broken Go type/vet check (undefined symbol inside valid Go module)
	brokenType := filepath.Join(dir, "broken_type.go")
	if err := os.WriteFile(brokenType, []byte("package fastchecktest\n\nfunc Compute() int {\n\treturn missingSymbol + 1\n}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	diagType := FastDiagnoseFile(ctx, brokenType)
	if !strings.Contains(diagType, AutoDiagnosticHeader) || !strings.Contains(diagType, "missingSymbol") {
		t.Fatalf("expected go vet type diagnostic mentioning missingSymbol, got %q", diagType)
	}

	// 3. Clean Go file returns empty diagnostic
	if err := os.WriteFile(brokenType, []byte("package fastchecktest\n\nfunc Compute() int {\n\treturn 42\n}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if clean := FastDiagnoseFile(ctx, brokenType); clean != "" {
		t.Fatalf("expected clean Go file to return empty diagnostic, got %q", clean)
	}

	// 4. Invalid JSON & valid JSON
	badJSON := filepath.Join(dir, "config.json")
	if err := os.WriteFile(badJSON, []byte(`{"port": 8080,}`), 0644); err != nil {
		t.Fatal(err)
	}
	if d := FastDiagnoseFile(ctx, badJSON); !strings.Contains(d, AutoDiagnosticHeader) {
		t.Fatalf("expected JSON syntax diagnostic, got %q", d)
	}
	if err := os.WriteFile(badJSON, []byte(`{"port": 8080}`), 0644); err != nil {
		t.Fatal(err)
	}
	if d := FastDiagnoseFile(ctx, badJSON); d != "" {
		t.Fatalf("expected valid JSON to pass, got %q", d)
	}

	// 5. Invalid YAML & valid YAML
	badYAML := filepath.Join(dir, "verifiers.yaml")
	if err := os.WriteFile(badYAML, []byte("checks:\n  - name: [unclosed\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if d := FastDiagnoseFile(ctx, badYAML); !strings.Contains(d, AutoDiagnosticHeader) {
		t.Fatalf("expected YAML syntax diagnostic, got %q", d)
	}
	if err := os.WriteFile(badYAML, []byte("checks:\n  - name: vet\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if d := FastDiagnoseFile(ctx, badYAML); d != "" {
		t.Fatalf("expected valid YAML to pass, got %q", d)
	}
}

func TestAnalyzeEditImpactAndQuerySymbolImpact(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/impacttest\n\ngo 1.22\n"), 0644); err != nil {
		t.Fatal(err)
	}

	oldBilling := []byte(`package billing

func CalculateInvoiceTotal(cents int) int {
	return cents
}
`)
	newBilling := []byte(`package billing

func CalculateInvoiceTotal(cents int, taxBps int) (int, error) {
	return cents, nil
}
`)

	billingPath := filepath.Join(dir, "billing.go")
	callerPath := filepath.Join(dir, "checkout.go")
	testPath := filepath.Join(dir, "billing_test.go")

	if err := os.WriteFile(billingPath, newBilling, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(callerPath, []byte(`package billing

func RunCheckout() int {
	return CalculateInvoiceTotal(1500)
}
`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(testPath, []byte(`package billing

import "testing"

func TestCalculateInvoiceTotal(t *testing.T) {
	if got := CalculateInvoiceTotal(100); got != 100 {
		t.Fatal(got)
	}
}
`), 0644); err != nil {
		t.Fatal(err)
	}

	impact := AnalyzeEditImpact(ctx, billingPath, oldBilling, newBilling)
	if !strings.Contains(impact, ImpactHeader) {
		t.Fatalf("expected ImpactHeader, got %q", impact)
	}
	if !strings.Contains(impact, "CalculateInvoiceTotal") {
		t.Fatalf("expected changed signature in impact output, got %q", impact)
	}
	if !strings.Contains(impact, "checkout.go") {
		t.Fatalf("expected external caller checkout.go in impact output, got %q", impact)
	}
	if !strings.Contains(impact, "billing_test.go") {
		t.Fatalf("expected companion test billing_test.go in impact output, got %q", impact)
	}

	queryReport := QuerySymbolImpact(ctx, dir, "CalculateInvoiceTotal", "billing.go")
	if !strings.Contains(queryReport, "checkout.go") || !strings.Contains(queryReport, "billing_test.go") {
		t.Fatalf("expected QuerySymbolImpact to list both production caller and test reference, got:\n%s", queryReport)
	}
}
