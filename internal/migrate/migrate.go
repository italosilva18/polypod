package migrate

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/costa/polypod/internal/skill"
	"github.com/sashabaranov/go-openai/jsonschema"
)

// RegisterSkills registers database migration skills.
func RegisterSkills(reg *skill.Registry) {
	reg.Register(&skill.Skill{
		Name:        "migrate_diff",
		Description: "Gerar migration SQL a partir de mudancas no schema (usa Atlas se disponivel)",
		Parameters: jsonschema.Definition{
			Type: jsonschema.Object,
			Properties: map[string]jsonschema.Definition{
				"name": {Type: jsonschema.String, Description: "Nome da migration"},
				"dir":  {Type: jsonschema.String, Description: "Diretorio de migrations (default: migrations/)"},
			},
			Required: []string{"name"},
		},
		Execute: func(args map[string]string) (string, error) {
			name := args["name"]
			dir := args["dir"]
			if dir == "" {
				dir = "migrations"
			}
			os.MkdirAll(dir, 0755)

			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			// Try Atlas first
			if _, err := exec.LookPath("atlas"); err == nil {
				cmd := exec.CommandContext(ctx, "atlas", "migrate", "diff", name, "--dir", "file://"+dir)
				out, err := cmd.CombinedOutput()
				if err != nil {
					return string(out), nil
				}
				return fmt.Sprintf("Migration gerada com Atlas: %s/\n%s", dir, string(out)), nil
			}

			// Try golang-migrate
			if _, err := exec.LookPath("migrate"); err == nil {
				timestamp := time.Now().Format("20060102150405")
				upFile := filepath.Join(dir, fmt.Sprintf("%s_%s.up.sql", timestamp, name))
				downFile := filepath.Join(dir, fmt.Sprintf("%s_%s.down.sql", timestamp, name))

				os.WriteFile(upFile, []byte("-- Migration UP: "+name+"\n-- Adicione seu SQL aqui\n\n"), 0644)
				os.WriteFile(downFile, []byte("-- Migration DOWN: "+name+"\n-- Adicione rollback SQL aqui\n\n"), 0644)

				return fmt.Sprintf("Migration criada:\n- %s\n- %s\n\nEdite os arquivos com o SQL desejado.", upFile, downFile), nil
			}

			// Fallback: generate plain SQL files
			timestamp := time.Now().Format("20060102150405")
			upFile := filepath.Join(dir, fmt.Sprintf("%s_%s.up.sql", timestamp, name))
			downFile := filepath.Join(dir, fmt.Sprintf("%s_%s.down.sql", timestamp, name))

			os.WriteFile(upFile, []byte("-- Migration UP: "+name+"\n\n"), 0644)
			os.WriteFile(downFile, []byte("-- Migration DOWN: "+name+"\n\n"), 0644)

			return fmt.Sprintf("Migration criada:\n- %s\n- %s", upFile, downFile), nil
		},
	})

	reg.Register(&skill.Skill{
		Name:        "migrate_list",
		Description: "Listar migrations existentes",
		Parameters: jsonschema.Definition{
			Type: jsonschema.Object,
			Properties: map[string]jsonschema.Definition{
				"dir": {Type: jsonschema.String, Description: "Diretorio de migrations (default: migrations/)"},
			},
		},
		Execute: func(args map[string]string) (string, error) {
			dir := args["dir"]
			if dir == "" {
				dir = "migrations"
			}
			entries, err := os.ReadDir(dir)
			if err != nil {
				return fmt.Sprintf("Nenhuma migration encontrada em %s/", dir), nil
			}

			var sb strings.Builder
			fmt.Fprintf(&sb, "## Migrations em %s/\n\n", dir)
			for _, e := range entries {
				if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
					continue
				}
				info, _ := e.Info()
				size := int64(0)
				if info != nil {
					size = info.Size()
				}
				fmt.Fprintf(&sb, "- %s (%d bytes)\n", e.Name(), size)
			}
			return sb.String(), nil
		},
	})

	reg.Register(&skill.Skill{
		Name:        "migrate_apply",
		Description: "Aplicar migrations pendentes em um banco de dados",
		Parameters: jsonschema.Definition{
			Type: jsonschema.Object,
			Properties: map[string]jsonschema.Definition{
				"dsn":     {Type: jsonschema.String, Description: "Connection string do banco"},
				"dir":     {Type: jsonschema.String, Description: "Diretorio de migrations (default: migrations/)"},
				"dry_run": {Type: jsonschema.String, Description: "'true' para apenas mostrar o que seria executado"},
			},
			Required: []string{"dsn"},
		},
		Execute: func(args map[string]string) (string, error) {
			dsn := args["dsn"]
			dir := args["dir"]
			if dir == "" {
				dir = "migrations"
			}

			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()

			// Try Atlas
			if _, err := exec.LookPath("atlas"); err == nil {
				atlasArgs := []string{"migrate", "apply", "--dir", "file://" + dir, "--url", dsn}
				if args["dry_run"] == "true" {
					atlasArgs = append(atlasArgs, "--dry-run")
				}
				cmd := exec.CommandContext(ctx, "atlas", atlasArgs...)
				out, err := cmd.CombinedOutput()
				if err != nil {
					return string(out), nil
				}
				return string(out), nil
			}

			// Try golang-migrate
			if _, err := exec.LookPath("migrate"); err == nil {
				migrateArgs := []string{"-path", dir, "-database", dsn, "up"}
				cmd := exec.CommandContext(ctx, "migrate", migrateArgs...)
				out, err := cmd.CombinedOutput()
				if err != nil {
					return string(out), nil
				}
				return string(out), nil
			}

			return "", fmt.Errorf("nenhuma ferramenta de migration encontrada (instale atlas ou golang-migrate)")
		},
	})

	reg.Register(&skill.Skill{
		Name:        "migrate_lint",
		Description: "Validar migrations para problemas de seguranca (data loss, backward compat)",
		Parameters: jsonschema.Definition{
			Type: jsonschema.Object,
			Properties: map[string]jsonschema.Definition{
				"dir": {Type: jsonschema.String, Description: "Diretorio de migrations"},
			},
		},
		Execute: func(args map[string]string) (string, error) {
			dir := args["dir"]
			if dir == "" {
				dir = "migrations"
			}

			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			if _, err := exec.LookPath("atlas"); err == nil {
				cmd := exec.CommandContext(ctx, "atlas", "migrate", "lint", "--dir", "file://"+dir)
				out, _ := cmd.CombinedOutput()
				output := strings.TrimSpace(string(out))
				if output == "" {
					return "Migrations OK — nenhum problema encontrado.", nil
				}
				return "## Migration Lint\n\n" + output, nil
			}

			// Manual lint: check for dangerous patterns
			var issues []string
			entries, _ := os.ReadDir(dir)
			for _, e := range entries {
				if !strings.HasSuffix(e.Name(), ".up.sql") {
					continue
				}
				data, _ := os.ReadFile(filepath.Join(dir, e.Name()))
				content := strings.ToUpper(string(data))

				if strings.Contains(content, "DROP TABLE") {
					issues = append(issues, fmt.Sprintf("[WARN] %s: contem DROP TABLE (perda de dados)", e.Name()))
				}
				if strings.Contains(content, "DROP COLUMN") {
					issues = append(issues, fmt.Sprintf("[WARN] %s: contem DROP COLUMN (perda de dados)", e.Name()))
				}
				if strings.Contains(content, "TRUNCATE") {
					issues = append(issues, fmt.Sprintf("[WARN] %s: contem TRUNCATE (perda de dados)", e.Name()))
				}
				if strings.Contains(content, "NOT NULL") && !strings.Contains(content, "DEFAULT") {
					issues = append(issues, fmt.Sprintf("[INFO] %s: ADD NOT NULL sem DEFAULT pode falhar em tabela existente", e.Name()))
				}
			}

			if len(issues) == 0 {
				return "Migrations OK — nenhum problema encontrado.", nil
			}
			return "## Migration Lint\n\n" + strings.Join(issues, "\n"), nil
		},
	})
}
