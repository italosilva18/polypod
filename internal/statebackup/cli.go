package statebackup

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/costa/polypod/internal/config"
	"github.com/costa/polypod/internal/configmerge"
	"github.com/costa/polypod/internal/dotenv"
)

type cliOptions struct {
	configPath         string
	output             string
	archive            string
	projectDir         string
	dataDir            string
	databasePath       string
	agentsDir          string
	schedulerPath      string
	whatsAppSessionDir string
	only               []string
	includeSecrets     bool
	confirm            bool
	list               bool
}

// RunCLI handles offline backup commands without starting the agent runtime.
func RunCLI(ctx context.Context, args []string, out io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("uso: polypod backup create|inspect|restore [opções]")
	}
	action := args[0]
	if action != "create" && action != "inspect" && action != "restore" && action != "preview" {
		return fmt.Errorf("ação de backup desconhecida %q (use create, inspect ou restore)", action)
	}
	opt, err := parseCLI(args[1:])
	if err != nil {
		return err
	}
	if action == "inspect" {
		if opt.archive == "" {
			return fmt.Errorf("uso: polypod backup inspect <arquivo.zip>")
		}
		manifest, err := Inspect(ctx, opt.archive)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "Backup válido: %d arquivos, criado em %s, segredos incluídos: %t\n", len(manifest.Files), manifest.CreatedAt.Format(time.RFC3339), manifest.SecretsIncluded)
		if opt.list {
			for _, entry := range manifest.Files {
				fmt.Fprintf(out, "%s/%s (%d bytes)\n", entry.Root, entry.Path, entry.Size)
			}
		}
		return nil
	}
	projectDir := opt.projectDir
	if projectDir == "" {
		projectDir, err = os.Getwd()
		if err != nil {
			return err
		}
	}
	projectDir, err = filepath.Abs(projectDir)
	if err != nil {
		return err
	}
	if opt.configPath == "" {
		opt.configPath = "config.yaml"
	}
	configPath, err := absoluteFrom(projectDir, opt.configPath)
	if err != nil {
		return err
	}
	_, _ = dotenv.Load(projectDir)
	layout := Layout{ProjectDir: projectDir, ConfigPath: configPath}
	cfg, loadErr := loadEffectiveConfig(projectDir, configPath)
	if loadErr == nil {
		layout.DataDir = cfg.Data.Dir
		layout.DatabasePath = cfg.Database.Path
		layout.AgentsDir = cfg.Data.AgentsDir
		layout.SchedulerPath = cfg.Scheduler.DataFile
		layout.WhatsAppSessionDir = cfg.WhatsApp.SessionDir
	} else if action == "create" {
		return fmt.Errorf("carregando configuração para backup: %w", loadErr)
	}
	if opt.dataDir != "" {
		layout.DataDir = opt.dataDir
	}
	if opt.databasePath != "" {
		layout.DatabasePath = opt.databasePath
	}
	if opt.agentsDir != "" {
		layout.AgentsDir = opt.agentsDir
	}
	if opt.schedulerPath != "" {
		layout.SchedulerPath = opt.schedulerPath
	}
	if opt.whatsAppSessionDir != "" {
		layout.WhatsAppSessionDir = opt.whatsAppSessionDir
	}
	if loadErr != nil && layout.DataDir == "" {
		return fmt.Errorf("configuração ausente; informe --data-dir e os demais caminhos externos antes de restaurar: %w", loadErr)
	}
	if action == "create" {
		if opt.output == "" {
			opt.output = filepath.Join(projectDir, "polypod-backup-"+time.Now().Format("20060102-150405")+".zip")
		}
		manifest, err := Create(ctx, layout, opt.output, opt.includeSecrets)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "Backup criado: %s (%d arquivos; segredos incluídos: %t)\n", opt.output, len(manifest.Files), manifest.SecretsIncluded)
		return nil
	}
	if opt.archive == "" {
		return fmt.Errorf("uso: polypod backup restore <arquivo.zip> [--confirm]")
	}
	manifest, changes, err := PreviewSelected(ctx, layout, opt.archive, opt.only)
	if err != nil {
		return err
	}
	creates, replaces, unchanged := 0, 0, 0
	for _, change := range changes {
		switch change.Action {
		case "create":
			creates++
		case "replace":
			replaces++
		default:
			unchanged++
		}
		if change.Action != "unchanged" {
			fmt.Fprintf(out, "%s %s\n", change.Action, change.Target)
		}
	}
	fmt.Fprintf(out, "Prévia: %d novos, %d substituídos, %d iguais; segredos no arquivo: %t\n", creates, replaces, unchanged, manifest.SecretsIncluded)
	if action == "preview" || !opt.confirm {
		fmt.Fprintln(out, "Nenhum arquivo alterado. Pare o serviço e use --confirm para aplicar a restauração.")
		return nil
	}
	_, _, err = RestoreSelected(ctx, layout, opt.archive, opt.only)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "Restauração concluída: %d arquivos criados ou substituídos. Reinicie o serviço.\n", creates+replaces)
	return nil
}

func loadEffectiveConfig(projectDir, configPath string) (*config.Config, error) {
	cfg, err := config.Load(configPath)
	if err != nil {
		return nil, err
	}
	fragments, err := configmerge.MergeConfigDir(filepath.Join(projectDir, "config.d"))
	if err != nil {
		return nil, fmt.Errorf("lendo config.d: %w", err)
	}
	if len(fragments) == 0 {
		return cfg, nil
	}
	base, err := cfg.Marshal()
	if err != nil {
		return nil, fmt.Errorf("serializando configuração: %w", err)
	}
	merged, err := configmerge.MergeIntoConfig(base, fragments)
	if err != nil {
		return nil, fmt.Errorf("aplicando config.d: %w", err)
	}
	var effective config.Config
	if err := yaml.Unmarshal(merged, &effective); err != nil {
		return nil, fmt.Errorf("lendo configuração mesclada: %w", err)
	}
	return &effective, nil
}

func parseCLI(args []string) (cliOptions, error) {
	var opt cliOptions
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "--include-secrets":
			opt.includeSecrets = true
		case "--confirm":
			opt.confirm = true
		case "--list":
			opt.list = true
		case "--config", "--output", "--project", "--data-dir", "--database", "--agents-dir", "--scheduler", "--whatsapp-session-dir", "--only":
			if i+1 >= len(args) || strings.HasPrefix(args[i+1], "--") {
				return opt, fmt.Errorf("opção %s precisa de um valor", arg)
			}
			i++
			value := args[i]
			switch arg {
			case "--config":
				opt.configPath = value
			case "--output":
				opt.output = value
			case "--project":
				opt.projectDir = value
			case "--data-dir":
				opt.dataDir = value
			case "--database":
				opt.databasePath = value
			case "--agents-dir":
				opt.agentsDir = value
			case "--scheduler":
				opt.schedulerPath = value
			case "--whatsapp-session-dir":
				opt.whatsAppSessionDir = value
			case "--only":
				opt.only = append(opt.only, value)
			}
		case "-h", "--help":
			return opt, errors.New("uso: polypod backup create [--output arquivo.zip] [--include-secrets]; polypod backup inspect arquivo.zip; polypod backup restore arquivo.zip [--confirm]")
		default:
			if strings.HasPrefix(arg, "-") {
				return opt, fmt.Errorf("opção desconhecida: %s", arg)
			}
			if opt.archive != "" {
				return opt, fmt.Errorf("informe apenas um arquivo de backup")
			}
			opt.archive = arg
		}
	}
	return opt, nil
}
