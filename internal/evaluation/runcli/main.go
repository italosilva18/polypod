// polypod-eval-run executes real headless cases in caller-prepared workspaces.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/costa/polypod/internal/evaluation"
)

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "Erro:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("polypod-eval-run", flag.ContinueOnError)
	manifestPath := flags.String("manifest", "", "Manifesto com prompts reais")
	metadataPath := flags.String("metadata", "", "Metadados reproduzíveis")
	planPath := flags.String("plan", "", "Plano de comandos e verificadores externos")
	outputPath := flags.String("output", "", "Arquivo de resultados JSON")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *manifestPath == "" || *metadataPath == "" || *planPath == "" || *outputPath == "" || flags.NArg() != 0 {
		return fmt.Errorf("informe -manifest, -metadata, -plan e -output")
	}
	var manifest evaluation.Manifest
	var metadata evaluation.Metadata
	var plan evaluation.CommandPlan
	for _, input := range []struct {
		path  string
		value any
	}{
		{*manifestPath, &manifest},
		{*metadataPath, &metadata},
		{*planPath, &plan},
	} {
		data, err := os.ReadFile(input.path)
		if err != nil {
			return fmt.Errorf("ler %s: %w", input.path, err)
		}
		if err := evaluation.Decode(bytes.NewReader(data), input.value); err != nil {
			return fmt.Errorf("decodificar %s: %w", input.path, err)
		}
	}
	runner, err := evaluation.NewCommandRunner(manifest, plan)
	if err != nil {
		return err
	}
	outputAbs, err := filepath.Abs(*outputPath)
	if err != nil {
		return err
	}
	for _, input := range []string{*manifestPath, *metadataPath, *planPath} {
		inputAbs, err := filepath.Abs(input)
		if err != nil {
			return err
		}
		if inputAbs == outputAbs {
			return fmt.Errorf("o arquivo de resultados não pode sobrescrever uma entrada")
		}
	}
	file, err := os.CreateTemp(filepath.Dir(outputAbs), ".polypod-eval-*.tmp")
	if err != nil {
		return fmt.Errorf("criar resultados temporários: %w", err)
	}
	defer file.Close()
	defer os.Remove(file.Name())
	if err := file.Chmod(0600); err != nil {
		return err
	}
	results, err := evaluation.Run(ctx, manifest, metadata, evaluation.RunOptions{
		Executor: runner.Execute,
		Verifier: runner.Verify,
	})
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(results, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if _, err := file.Write(data); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(file.Name(), outputAbs); err != nil {
		return fmt.Errorf("publicar resultados: %w", err)
	}
	return nil
}
