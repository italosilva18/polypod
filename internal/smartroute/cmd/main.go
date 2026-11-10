// polypod-routing-policy builds a model policy from real, paired evaluations.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/costa/polypod/internal/evaluation"
	"github.com/costa/polypod/internal/smartroute"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "Erro:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	flags := flag.NewFlagSet("polypod-routing-policy", flag.ContinueOnError)
	manifestPath := flags.String("manifest", "", "Manifesto com tarefas reais")
	provider := flags.String("provider", "", "Provedor primário configurado")
	defaultModel := flags.String("default-model", "", "Modelo primário configurado")
	allowed := flags.String("allowed-models", "", "Modelos candidatos no mesmo provedor, separados por vírgula")
	outputPath := flags.String("output", "", "Arquivo de política JSON")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *manifestPath == "" || *provider == "" || *defaultModel == "" || *allowed == "" || *outputPath == "" || len(flags.Args()) < 2 {
		return fmt.Errorf("informe -manifest, -provider, -default-model, -allowed-models, -output e ao menos dois arquivos de resultados")
	}
	manifestBytes, err := os.ReadFile(*manifestPath)
	if err != nil {
		return fmt.Errorf("ler manifesto: %w", err)
	}
	var manifest evaluation.Manifest
	if err := evaluation.Decode(bytes.NewReader(manifestBytes), &manifest); err != nil {
		return fmt.Errorf("manifesto: %w", err)
	}
	results := make([]evaluation.Results, 0, len(flags.Args()))
	for _, path := range flags.Args() {
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("ler resultados %s: %w", path, err)
		}
		var result evaluation.Results
		if err := evaluation.Decode(bytes.NewReader(data), &result); err != nil {
			return fmt.Errorf("resultados %s: %w", path, err)
		}
		results = append(results, result)
	}
	models := strings.Split(*allowed, ",")
	for i := range models {
		models[i] = strings.TrimSpace(models[i])
	}
	policy, err := smartroute.BuildCalibratedPolicy(manifest, results, *provider, *defaultModel, models)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(policy, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	file, err := os.CreateTemp(filepath.Dir(*outputPath), ".polypod-routing-*.tmp")
	if err != nil {
		return fmt.Errorf("criar política temporária: %w", err)
	}
	defer os.Remove(file.Name())
	if err := file.Chmod(0600); err != nil {
		file.Close()
		return err
	}
	if _, err := file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(file.Name(), *outputPath); err != nil {
		return fmt.Errorf("publicar política: %w", err)
	}
	return nil
}
