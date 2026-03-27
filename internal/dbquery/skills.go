package dbquery

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/costa/polypod/internal/skill"
	"github.com/sashabaranov/go-openai/jsonschema"
)

// RegisterSkills registers database query skills.
func RegisterSkills(reg *skill.Registry) {
	reg.Register(&skill.Skill{
		Name:        "db_query",
		Description: "Executar uma query SQL em um banco de dados (postgres, mysql, sqlite)",
		Parameters: jsonschema.Definition{
			Type: jsonschema.Object,
			Properties: map[string]jsonschema.Definition{
				"dsn":      {Type: jsonschema.String, Description: "Connection string (postgres://... ou sqlite:///path ou mysql://...)"},
				"query":    {Type: jsonschema.String, Description: "Query SQL a executar"},
				"max_rows": {Type: jsonschema.String, Description: "Maximo de linhas (default: 50)"},
			},
			Required: []string{"dsn", "query"},
		},
		Execute: func(args map[string]string) (string, error) {
			dsn := args["dsn"]
			query := args["query"]
			maxRows := args["max_rows"]
			if maxRows == "" {
				maxRows = "50"
			}

			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			switch {
			case strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://"):
				limitedQuery := fmt.Sprintf("SELECT * FROM (%s) AS q LIMIT %s", strings.TrimSuffix(query, ";"), maxRows)
				cmd := exec.CommandContext(ctx, "psql", dsn, "-c", limitedQuery, "--csv")
				out, err := cmd.CombinedOutput()
				if err != nil {
					return string(out), nil
				}
				return formatCSV(string(out)), nil

			case strings.HasPrefix(dsn, "sqlite://") || strings.HasPrefix(dsn, "sqlite:///"):
				path := strings.TrimPrefix(strings.TrimPrefix(dsn, "sqlite:///"), "sqlite://")
				cmd := exec.CommandContext(ctx, "sqlite3", "-header", "-csv", path, query+" LIMIT "+maxRows+";")
				out, err := cmd.CombinedOutput()
				if err != nil {
					return string(out), nil
				}
				return formatCSV(string(out)), nil

			case strings.HasPrefix(dsn, "mysql://"):
				// Parse mysql://user:pass@host/db
				trimmed := strings.TrimPrefix(dsn, "mysql://")
				cmd := exec.CommandContext(ctx, "mysql", "-e", query, "--batch", "--table")
				cmd.Env = append(cmd.Env, "MYSQL_PWD=") // parse from DSN in real implementation
				_ = trimmed
				out, err := cmd.CombinedOutput()
				if err != nil {
					return string(out), nil
				}
				return string(out), nil

			default:
				return "", fmt.Errorf("tipo de banco nao suportado. Use postgres://, sqlite:/// ou mysql://")
			}
		},
	})

	reg.Register(&skill.Skill{
		Name:        "db_schema",
		Description: "Mostrar schema de um banco de dados",
		Parameters: jsonschema.Definition{
			Type: jsonschema.Object,
			Properties: map[string]jsonschema.Definition{
				"dsn":   {Type: jsonschema.String, Description: "Connection string"},
				"table": {Type: jsonschema.String, Description: "Tabela especifica (opcional)"},
			},
			Required: []string{"dsn"},
		},
		Execute: func(args map[string]string) (string, error) {
			dsn := args["dsn"]
			table := args["table"]
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()

			switch {
			case strings.HasPrefix(dsn, "postgres://"):
				query := "SELECT table_name, column_name, data_type FROM information_schema.columns WHERE table_schema='public'"
				if table != "" {
					query += " AND table_name='" + table + "'"
				}
				query += " ORDER BY table_name, ordinal_position"
				cmd := exec.CommandContext(ctx, "psql", dsn, "-c", query, "--csv")
				out, _ := cmd.CombinedOutput()
				return formatCSV(string(out)), nil

			case strings.HasPrefix(dsn, "sqlite://") || strings.HasPrefix(dsn, "sqlite:///"):
				path := strings.TrimPrefix(strings.TrimPrefix(dsn, "sqlite:///"), "sqlite://")
				query := "SELECT sql FROM sqlite_master WHERE type='table'"
				if table != "" {
					query += " AND name='" + table + "'"
				}
				cmd := exec.CommandContext(ctx, "sqlite3", path, query+";")
				out, _ := cmd.CombinedOutput()
				return string(out), nil

			default:
				return "", fmt.Errorf("tipo de banco nao suportado")
			}
		},
	})

	reg.Register(&skill.Skill{
		Name:        "db_tables",
		Description: "Listar tabelas de um banco de dados",
		Parameters: jsonschema.Definition{
			Type: jsonschema.Object,
			Properties: map[string]jsonschema.Definition{
				"dsn": {Type: jsonschema.String, Description: "Connection string"},
			},
			Required: []string{"dsn"},
		},
		Execute: func(args map[string]string) (string, error) {
			dsn := args["dsn"]
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			switch {
			case strings.HasPrefix(dsn, "postgres://"):
				cmd := exec.CommandContext(ctx, "psql", dsn, "-c",
					"SELECT table_name FROM information_schema.tables WHERE table_schema='public' ORDER BY 1", "--csv")
				out, _ := cmd.CombinedOutput()
				return formatCSV(string(out)), nil

			case strings.HasPrefix(dsn, "sqlite://") || strings.HasPrefix(dsn, "sqlite:///"):
				path := strings.TrimPrefix(strings.TrimPrefix(dsn, "sqlite:///"), "sqlite://")
				cmd := exec.CommandContext(ctx, "sqlite3", path, ".tables")
				out, _ := cmd.CombinedOutput()
				return string(out), nil

			default:
				return "", fmt.Errorf("tipo de banco nao suportado")
			}
		},
	})
}

func formatCSV(csv string) string {
	lines := strings.Split(strings.TrimSpace(csv), "\n")
	if len(lines) < 2 {
		return csv
	}

	// Convert CSV to markdown table
	var sb strings.Builder
	for i, line := range lines {
		cols := strings.Split(line, ",")
		sb.WriteString("| ")
		sb.WriteString(strings.Join(cols, " | "))
		sb.WriteString(" |\n")
		if i == 0 {
			sb.WriteString("|")
			for range cols {
				sb.WriteString("---|")
			}
			sb.WriteString("\n")
		}
	}
	return sb.String()
}
