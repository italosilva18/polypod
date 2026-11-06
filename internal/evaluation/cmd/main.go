// polypod-eval is an offline report generator; it never executes the agent.
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/costa/polypod/internal/evaluation"
)

func run(args []string, out io.Writer) error {
	flags := flag.NewFlagSet("polypod-eval", flag.ContinueOnError)
	manifest := flags.String("manifest", "docs/evaluation/manifest.v1.json", "Manifesto de tarefas")
	results := flags.String("results", "", "Resultados verificados (JSON)")
	format := flags.String("format", "json", "Formato: json ou markdown")
	flags.SetOutput(out)
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *results == "" || flags.NArg() != 0 {
		return fmt.Errorf("informe -results arquivo.json")
	}
	if *format != "json" && *format != "markdown" {
		return fmt.Errorf("formato inválido: %s", *format)
	}
	mb, err := os.ReadFile(*manifest)
	if err != nil {
		return err
	}
	rb, err := os.ReadFile(*results)
	if err != nil {
		return err
	}
	var m evaluation.Manifest
	var r evaluation.Results
	if err := evaluation.Decode(bytes.NewReader(mb), &m); err != nil {
		return fmt.Errorf("manifesto: %w", err)
	}
	if err := evaluation.Decode(bytes.NewReader(rb), &r); err != nil {
		return fmt.Errorf("resultados: %w", err)
	}
	p, err := evaluation.Evaluate(m, r)
	if err != nil {
		return err
	}
	p.ManifestSHA256 = fmt.Sprintf("%x", sha256.Sum256(mb))
	p.ResultsSHA256 = fmt.Sprintf("%x", sha256.Sum256(rb))
	p.Traces = map[string]evaluation.TraceStats{}
	for _, o := range r.Outcomes {
		if o.Trace == "" {
			continue
		}
		path := o.Trace
		if !filepath.IsAbs(path) {
			path = filepath.Join(filepath.Dir(*results), path)
		}
		f, err := os.Open(path)
		if err != nil {
			return fmt.Errorf("trace %s: %w", o.TaskID, err)
		}
		stats, readErr := evaluation.ReadTrace(f)
		closeErr := f.Close()
		if readErr != nil {
			return readErr
		}
		if closeErr != nil {
			return closeErr
		}
		p.Traces[o.TaskID] = stats
	}
	if *format == "markdown" {
		markdown, err := evaluation.Markdown(p)
		if err != nil {
			return err
		}
		_, err = io.WriteString(out, markdown)
		return err
	}
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	return enc.Encode(p)
}
func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "Erro:", err)
		os.Exit(1)
	}
}
