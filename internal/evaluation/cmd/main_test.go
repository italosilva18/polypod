package main

import (
	"bytes"
	"encoding/json"
	"github.com/costa/polypod/internal/evaluation"
	"testing"
)

func TestOfflineEndToEnd(t *testing.T) {
	var b bytes.Buffer
	args := []string{"-manifest", "../../../docs/evaluation/manifest.v1.json", "-results", "../testdata/results.v1.json"}
	if err := run(args, &b); err != nil {
		t.Fatal(err)
	}
	var p evaluation.Report
	if err := json.Unmarshal(b.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	if p.Overall.Success != 2 || len(p.ManifestSHA256) != 64 || len(p.ResultsSHA256) != 64 || p.Traces["gui-save"].ToolCalls != 1 {
		t.Fatalf("report: %+v", p)
	}
	b.Reset()
	if err := run(append(args, "-format", "markdown"), &b); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(b.Bytes(), []byte("Dados sintéticos")) {
		t.Fatal("fixture disclaimer missing")
	}
}
func TestInvalidCLI(t *testing.T) {
	for _, args := range [][]string{nil, {"-results", "x", "-format", "bad"}, {"-results", "missing.json"}} {
		var b bytes.Buffer
		if err := run(args, &b); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
}
